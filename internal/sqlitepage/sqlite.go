package sqlitepage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const headerSize = 100

// DB is a read-only view of a SQLite database file.
type DB struct {
	f        *os.File
	pageSize int
	usable   int
	encoding uint32
	// overlay holds the pages a replayed write-ahead log supersedes.
	overlay map[uint32][]byte
	// pages is the committed page count a replayed log declares, or zero
	// when no log was read. Pages beyond it are not part of this snapshot.
	pages uint32
}

// Open opens path for reading. A non-empty -wal sidecar is replayed onto the
// file pages, so a database with a live writer reads as a best-effort
// snapshot: a checkpoint that lands between Open and a later page read can
// still leave the two sources at different commits. A log this reader cannot
// parse is refused: a read without it would silently return a stale snapshot.
func Open(path string) (*DB, error) {
	if fi, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	} else if fi.IsDir() {
		return nil, fmt.Errorf("%w: %q is a directory", ErrNotSQLite, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: short header", ErrNotSQLite)
	}
	if string(hdr[:16]) != "SQLite format 3\x00" {
		f.Close()
		return nil, fmt.Errorf("%w: bad magic", ErrNotSQLite)
	}
	ps := int(binary.BigEndian.Uint16(hdr[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps&(ps-1) != 0 {
		f.Close()
		return nil, fmt.Errorf("%w: page size %d", ErrCorrupt, ps)
	}
	reserved := int(hdr[20])
	usable := ps - reserved
	if usable < 480 {
		f.Close()
		return nil, fmt.Errorf("%w: usable page size %d", ErrCorrupt, usable)
	}
	enc := binary.BigEndian.Uint32(hdr[56:60])
	if enc < 1 || enc > 3 {
		enc = 1
	}
	db := &DB{f: f, pageSize: ps, usable: usable, encoding: enc}
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() > 0 {
		if err := db.applyWAL(path + "-wal"); err != nil {
			f.Close()
			return nil, err
		}
	}
	return db, nil
}

func (db *DB) Close() error { return db.f.Close() }

// PageSize reports the database page size in bytes.
func (db *DB) PageSize() int { return db.pageSize }

func (db *DB) readPage(pgno uint32) ([]byte, error) {
	if pgno == 0 {
		return nil, fmt.Errorf("%w: page 0", ErrCorrupt)
	}
	if db.pages > 0 && pgno > db.pages {
		return nil, fmt.Errorf("%w: page %d past committed size %d", ErrCorrupt, pgno, db.pages)
	}
	if page, ok := db.overlay[pgno]; ok {
		return bytes.Clone(page), nil
	}
	buf := make([]byte, db.pageSize)
	if _, err := db.f.ReadAt(buf, int64(pgno-1)*int64(db.pageSize)); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: page %d past end of file", ErrCorrupt, pgno)
		}
		return nil, fmt.Errorf("read page %d: %w", pgno, err)
	}
	return buf, nil
}

// Table is one rowid table resolved from sqlite_master.
type Table struct {
	db   *DB
	Name string
	Root uint32
	// SQL is the stored CREATE TABLE statement, verbatim.
	SQL string
}

// Table resolves name from sqlite_master. A missing table and a WITHOUT ROWID
// table both return ErrUnsupported.
func (db *DB) Table(name string) (*Table, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: empty table name", ErrUnsupported)
	}
	master := &Table{db: db, Name: "sqlite_master", Root: 1}
	var found *Table
	err := master.Rows(func(_ int64, vals []Value) error {
		if len(vals) < 5 || vals[0].Kind != Text || vals[1].Kind != Text {
			return nil
		}
		if vals[1].Text != name || vals[0].Text != "table" {
			return nil
		}
		if vals[3].Kind != Integer {
			return fmt.Errorf("%w: root page of %q", ErrCorrupt, name)
		}
		if vals[4].Text != "" && strings.Contains(strings.ToUpper(vals[4].Text), "WITHOUT ROWID") {
			return fmt.Errorf("%w: table %q is WITHOUT ROWID", ErrUnsupported, name)
		}
		found = &Table{db: db, Name: name, Root: uint32(vals[3].Int), SQL: vals[4].Text}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("%w: table %q not found", ErrUnsupported, name)
	}
	return found, nil
}

// Columns returns the table's column names in declaration order. A source
// reader uses it to prove that a positional read still addresses the columns
// it was written for, instead of trusting the column count.
func (t *Table) Columns() ([]string, error) {
	cols, err := parseColumns(t.SQL)
	if err != nil {
		return nil, fmt.Errorf("columns of %q: %w", t.Name, err)
	}
	return cols, nil
}

// TableWithColumns resolves name and proves that the positions a caller
// addresses still name the columns it was written for. See CheckColumns. A
// missing table returns ErrUnsupported, the same as Table.
func (db *DB) TableWithColumns(name string, want map[int]string) (*Table, error) {
	tbl, err := db.Table(name)
	if err != nil {
		return nil, err
	}
	cols, err := tbl.Columns()
	if err != nil {
		return nil, err
	}
	if err := CheckColumns(name, cols, want); err != nil {
		return nil, err
	}
	return tbl, nil
}

// CheckColumns proves that every position in want names the column a caller
// expects. A rebuilt table appends or reorders columns, so a mismatch must
// fail here instead of misreading a value.
func CheckColumns(name string, cols []string, want map[int]string) error {
	for i, wantName := range want {
		if i >= len(cols) {
			return fmt.Errorf("%w: %s column %d is absent, want %q", ErrUnsupported, name, i, wantName)
		}
		if cols[i] != wantName {
			return fmt.Errorf("%w: %s column %d is %q, want %q", ErrUnsupported, name, i, cols[i], wantName)
		}
	}
	return nil
}

func parseColumns(sql string) ([]string, error) {
	_, after, ok := strings.Cut(sql, "(")
	if !ok {
		return nil, fmt.Errorf("%w: table definition without a column list", ErrUnsupported)
	}
	defs := splitTopLevel(after)
	cols := make([]string, 0, len(defs))
	for _, def := range defs {
		name := strings.Fields(def)
		if len(name) == 0 {
			continue
		}
		switch strings.ToUpper(name[0]) {
		case "CONSTRAINT", "PRIMARY", "UNIQUE", "CHECK", "FOREIGN":
			continue
		}
		cols = append(cols, unquoteIdentifier(name[0]))
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%w: table definition without columns", ErrUnsupported)
	}
	return cols, nil
}

// splitTopLevel splits a column list at depth-one commas and stops at the
// parenthesis that closes it.
func splitTopLevel(body string) []string {
	defs := []string{}
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return append(defs, body[start:i])
			}
			depth--
		case ',':
			if depth == 0 {
				defs = append(defs, body[start:i])
				start = i + 1
			}
		}
	}
	return append(defs, body[start:])
}

func unquoteIdentifier(s string) string {
	if len(s) >= 2 {
		switch {
		case s[0] == '"' && s[len(s)-1] == '"',
			s[0] == '`' && s[len(s)-1] == '`',
			s[0] == '[' && s[len(s)-1] == ']':
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Rows walks the table b-tree in rowid order and calls fn once per row.
// The values and their []byte payloads are valid only until fn returns; a
// caller that keeps a value must copy it. Returning an error from fn stops the
// walk and hands that error back unchanged, which lets a caller apply a
// context or a key filter to a full scan.
func (t *Table) Rows(fn func(rowid int64, vals []Value) error) error {
	seen := map[uint32]bool{}
	var walk func(uint32) error
	walk = func(pgno uint32) error {
		if seen[pgno] {
			return fmt.Errorf("%w: b-tree cycle at page %d", ErrCorrupt, pgno)
		}
		seen[pgno] = true
		page, err := t.db.readPage(pgno)
		if err != nil {
			return err
		}
		h, err := pageHeaderAt(page, pgno)
		if err != nil {
			return err
		}
		switch h.typ {
		case 0x0d:
			for i := 0; i < h.nCells; i++ {
				off, err := h.cellOffset(page, i)
				if err != nil {
					return err
				}
				rowid, payload, err := t.db.readTableCell(page, off)
				if err != nil {
					return err
				}
				vals, err := t.db.decodeRecord(payload)
				if err != nil {
					return fmt.Errorf("page %d cell %d: %w", pgno, i, err)
				}
				if err := fn(rowid, vals); err != nil {
					return err
				}
			}
			return nil
		case 0x05:
			for i := 0; i < h.nCells; i++ {
				off, err := h.cellOffset(page, i)
				if err != nil {
					return err
				}
				if off+4 > len(page) {
					return fmt.Errorf("%w: interior cell offset %d", ErrCorrupt, off)
				}
				if err := walk(binary.BigEndian.Uint32(page[off : off+4])); err != nil {
					return err
				}
			}
			return walk(h.right)
		default:
			return fmt.Errorf("%w: page type %#x on page %d", ErrUnsupported, h.typ, pgno)
		}
	}
	return walk(t.Root)
}
