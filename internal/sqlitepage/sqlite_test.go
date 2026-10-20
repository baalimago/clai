package sqlitepage

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func openDB(t *testing.T, name string) *DB {
	t.Helper()
	db, err := Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func collect(t *testing.T, db *DB, table string) ([]int64, [][]Value) {
	t.Helper()
	tbl, err := db.Table(table)
	if err != nil {
		t.Fatalf("table %s: %v", table, err)
	}
	var rowids []int64
	var rows [][]Value
	err = tbl.Rows(func(rowid int64, vals []Value) error {
		rowids = append(rowids, rowid)
		rows = append(rows, vals)
		return nil
	})
	if err != nil {
		t.Fatalf("rows %s: %v", table, err)
	}
	return rowids, rows
}

func TestKVValues(t *testing.T) {
	db := openDB(t, "basic.db")
	if db.PageSize() != 4096 {
		t.Fatalf("page size = %d, want 4096", db.PageSize())
	}
	_, rows := collect(t, db, "kv")
	got := map[string]Value{}
	for _, r := range rows {
		if len(r) != 2 {
			t.Fatalf("want 2 columns, got %d", len(r))
		}
		if r[0].Kind != Text {
			t.Fatalf("key kind %d", r[0].Kind)
		}
		got[r[0].Text] = r[1]
	}
	if len(got) != 9 {
		t.Fatalf("want 9 rows, got %d", len(got))
	}
	if v := got["small"]; v.Kind != Blob || string(v.Blob) != "hello" {
		t.Fatalf("small: %+v", v)
	}
	if v := got["unicode"]; v.Kind != Blob || string(v.Blob) != "näppäimistö 日本語" {
		t.Fatalf("unicode: %q", v.Blob)
	}
	if v := got["null"]; v.Kind != Null {
		t.Fatalf("null: %+v", v)
	}
	if v := got["int"]; v.Kind != Integer || v.Int != -1234567890123 {
		t.Fatalf("int: %+v", v)
	}
	if v := got["zero"]; v.Kind != Integer || v.Int != 0 {
		t.Fatalf("zero: %+v", v)
	}
	if v := got["one"]; v.Kind != Integer || v.Int != 1 {
		t.Fatalf("one: %+v", v)
	}
	if v := got["real"]; v.Kind != Real || v.Real != 3.5 {
		t.Fatalf("real: %+v", v)
	}
	if v := got["bigblob"]; v.Kind != Blob || len(v.Blob) != 307200 {
		t.Fatalf("bigblob: kind %d len %d", v.Kind, len(v.Blob))
	}
	big := got["bigblob"]
	if big.Blob[0] != 0 || big.Blob[255] != 255 || big.Blob[256] != 0 || big.Blob[307199] != 255 {
		t.Fatalf("bigblob content mismatch at boundaries")
	}
	if v := got["bigtext"]; v.Kind != Blob || len(v.Blob) != 100000 || v.Blob[99999] != 'x' {
		t.Fatalf("bigtext: kind %d len %d", v.Kind, len(v.Blob))
	}
}

func TestTypesTable(t *testing.T) {
	db := openDB(t, "basic.db")
	_, rows := collect(t, db, "types")
	if len(rows) != 8 {
		t.Fatalf("want 8 rows, got %d", len(rows))
	}
	want := []Value{
		{Kind: Null},
		{Kind: Integer, Int: 0},
		{Kind: Integer, Int: 1},
		{Kind: Integer, Int: -1},
		{Kind: Integer, Int: 1 << 40},
		{Kind: Real, Real: 3.25},
		{Kind: Blob, Blob: []byte("bytes")},
		{Kind: Text, Text: "text"},
	}
	for i := range want {
		if rows[i][0].Kind != want[i].Kind {
			t.Fatalf("row %d kind: got %v want %v", i, rows[i][0].Kind, want[i].Kind)
		}
		switch want[i].Kind {
		case Integer:
			if rows[i][0].Int != want[i].Int {
				t.Fatalf("row %d int: got %d want %d", i, rows[i][0].Int, want[i].Int)
			}
		case Real:
			if math.Abs(rows[i][0].Real-want[i].Real) > 1e-9 {
				t.Fatalf("row %d real: got %v want %v", i, rows[i][0].Real, want[i].Real)
			}
		case Blob:
			if string(rows[i][0].Blob) != string(want[i].Blob) {
				t.Fatalf("row %d blob: got %q", i, rows[i][0].Blob)
			}
		case Text:
			if rows[i][0].Text != want[i].Text {
				t.Fatalf("row %d text: got %q", i, rows[i][0].Text)
			}
		case Null:
		}
	}
}

func TestInteriorPages(t *testing.T) {
	db := openDB(t, "basic.db")
	rowids, rows := collect(t, db, "other")
	if len(rows) != 2000 {
		t.Fatalf("want 2000 rows, got %d", len(rows))
	}
	for i, r := range rows {
		// a is INTEGER PRIMARY KEY, an alias for the rowid: SQLite stores it as
		// NULL in the record and the value lives in the rowid.
		if len(r) < 2 || r[0].Kind != Null {
			t.Fatalf("row %d: %+v", i, r)
		}
		if r[1].Kind != Text || r[1].Text != fmt.Sprintf("row-%d", i) {
			t.Fatalf("row %d text %q", i, r[1].Text)
		}
		if rowids[i] != int64(i) {
			t.Fatalf("rowid %d: %d", i, rowids[i])
		}
	}
	for i := 1; i < len(rowids); i++ {
		if rowids[i] <= rowids[i-1] {
			t.Fatalf("rowids not ascending at %d: %d then %d", i, rowids[i-1], rowids[i])
		}
	}
}

func TestUTF16Text(t *testing.T) {
	db := openDB(t, "utf16.db")
	_, rows := collect(t, db, "t")
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("rows: %v", rows)
	}
	if rows[0][0].Kind != Text || rows[0][0].Text != "日本語 text" {
		t.Fatalf("text: %+v", rows[0][0])
	}
}

func TestWALRefused(t *testing.T) {
	_, err := Open(filepath.Join("testdata", "wal.db"))
	if !errors.Is(err, ErrWALPresent) {
		t.Fatalf("want ErrWALPresent, got %v", err)
	}
}

func TestOpenShortHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "short.db")
	if err := os.WriteFile(p, []byte("SQLite format 3\x00short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); !errors.Is(err, ErrNotSQLite) {
		t.Fatalf("want ErrNotSQLite, got %v", err)
	}
}

func TestOpenBadHeaderFields(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"page size", func(b []byte) { b[16], b[17] = 0x03, 0x00 }, ErrCorrupt},
		{"encoding", func(b []byte) { b[56], b[57], b[58], b[59] = 0, 0, 0, 0 }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := append([]byte(nil), src...)
			tc.mutate(b)
			p := filepath.Join(t.TempDir(), "mutated.db")
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			db, err := Open(p)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("want %v, got %v", tc.want, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			db.Close()
		})
	}
}

func TestCorruptCellPointer(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Page 1 holds sqlite_master; its first cell pointer starts after the
	// 100-byte file header and the 8-byte page header.
	src[108], src[109] = 0xff, 0xff
	p := filepath.Join(t.TempDir(), "bad-cell.db")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Table("kv"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestNotSQLite(t *testing.T) {
	_, err := Open("testdata/gen.py")
	if !errors.Is(err, ErrNotSQLite) {
		t.Fatalf("want ErrNotSQLite, got %v", err)
	}
}

func TestOpenDirectory(t *testing.T) {
	if _, err := Open("testdata"); !errors.Is(err, ErrNotSQLite) {
		t.Fatalf("want ErrNotSQLite, got %v", err)
	}
}

func TestOpenMissingFile(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

func TestMissingTable(t *testing.T) {
	db := openDB(t, "basic.db")
	if _, err := db.Table("nope"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	if _, err := db.Table("  "); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported for an empty name, got %v", err)
	}
}

func TestCorruptPageType(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	src[100] = 0x99
	p := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Table("kv"); !errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want a typed failure, got %v", err)
	}
}

func TestTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "short.db")
	if err := os.WriteFile(p, src[:4096+64], 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	tbl, err := db.Table("kv")
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	err = tbl.Rows(func(int64, []Value) error { return nil })
	if err == nil {
		t.Fatal("want an error walking a truncated file")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestRowsCallbackErrorReturned(t *testing.T) {
	db := openDB(t, "basic.db")
	tbl, err := db.Table("kv")
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	got := tbl.Rows(func(int64, []Value) error { return stop })
	if !errors.Is(got, stop) {
		t.Fatalf("want the callback error, got %v", got)
	}
}

func TestVarint(t *testing.T) {
	cases := []struct {
		in   []byte
		want int64
		used int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x81, 0x00}, 128, 2},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, -1, 9},
	}
	for _, tc := range cases {
		got, used, err := varint(tc.in)
		if err != nil {
			t.Fatalf("varint(%x): %v", tc.in, err)
		}
		if got != tc.want || used != tc.used {
			t.Fatalf("varint(%x) = %d,%d want %d,%d", tc.in, got, used, tc.want, tc.used)
		}
	}
	if _, _, err := varint([]byte{0x80}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want a truncated varint error, got %v", err)
	}
	if _, _, err := varint([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want a truncated nine-byte varint error, got %v", err)
	}
}

func TestReadPageZero(t *testing.T) {
	db := openDB(t, "basic.db")
	if _, err := db.readPage(0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestPageHeaderShort(t *testing.T) {
	if _, err := pageHeaderAt(make([]byte, 4), 1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if _, err := pageHeaderAt(make([]byte, 4), 2); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestDecodeRecordShortHeader(t *testing.T) {
	db := openDB(t, "basic.db")
	if _, err := db.decodeRecord([]byte{0x7f}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestDecodeValueReservedSerial(t *testing.T) {
	db := openDB(t, "basic.db")
	for _, st := range []int64{10, 11} {
		if _, _, err := db.decodeValue(st, nil); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("serial %d: want ErrCorrupt, got %v", st, err)
		}
	}
	if _, _, err := db.decodeValue(1, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short integer: want ErrCorrupt, got %v", err)
	}
	if _, _, err := db.decodeValue(7, make([]byte, 4)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short real: want ErrCorrupt, got %v", err)
	}
	if _, _, err := db.decodeValue(14, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short blob: want ErrCorrupt, got %v", err)
	}
	if _, _, err := db.decodeValue(15, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short text: want ErrCorrupt, got %v", err)
	}
}

func TestDecodeTextOddUTF16(t *testing.T) {
	db := &DB{encoding: 2}
	if got := db.decodeText([]byte{0x41}); got != "A" {
		t.Fatalf("odd UTF-16 must fall back to raw bytes, got %q", got)
	}
}
