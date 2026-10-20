package sqlitepage

import (
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
}

// Open opens path for reading. It refuses a non-empty -wal sidecar because
// replaying the write-ahead log is outside this reader, and a read without it
// would silently return a stale snapshot.
func Open(path string) (*DB, error) {
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() > 0 {
		return nil, fmt.Errorf("%w: %s", ErrWALPresent, path+"-wal")
	}
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
	enc := binary.BigEndian.Uint32(hdr[56:60])
	if enc < 1 || enc > 3 {
		enc = 1
	}
	return &DB{f: f, pageSize: ps, usable: ps - reserved, encoding: enc}, nil
}

func (db *DB) Close() error { return db.f.Close() }

// PageSize reports the database page size in bytes.
func (db *DB) PageSize() int { return db.pageSize }

func (db *DB) readPage(pgno uint32) ([]byte, error) {
	if pgno == 0 {
		return nil, fmt.Errorf("%w: page 0", ErrCorrupt)
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
		found = &Table{db: db, Name: name, Root: uint32(vals[3].Int)}
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
