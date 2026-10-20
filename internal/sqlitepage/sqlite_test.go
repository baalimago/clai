package sqlitepage

import (
	"encoding/binary"
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

func TestWALReplay(t *testing.T) {
	db := openDB(t, "wal.db")
	_, rows := collect(t, db, "t")
	var got []string
	for _, r := range rows {
		got = append(got, r[0].Text)
	}
	want := []string{"checkpointed", "wal-one", "wal-two"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestWALMalformedRefused(t *testing.T) {
	_, err := Open(filepath.Join("testdata", "badwal.db"))
	if !errors.Is(err, ErrWALPresent) {
		t.Fatalf("want ErrWALPresent, got %v", err)
	}
}

func TestWALTornTailKeepsCommittedFrames(t *testing.T) {
	dir := copyWALFixture(t)
	truncateFile(t, filepath.Join(dir, "wal.db-wal"), walHeaderSize+walFrameSize+4096)
	db, err := Open(filepath.Join(dir, "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if got := tableTexts(t, db, "t"); fmt.Sprint(got) != fmt.Sprint([]string{"checkpointed", "wal-one"}) {
		t.Fatalf("rows = %v, want the first commit only", got)
	}
}

func TestWALTamperedFrameKeepsEarlierCommit(t *testing.T) {
	dir := copyWALFixture(t)
	p := filepath.Join(dir, "wal.db-wal")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Byte 900 of the second frame's page data: inside the first commit's
	// page range, so the checksum of frame two no longer matches.
	b[walHeaderSize+walFrameSize+4096+walFrameSize+900] ^= 0xff
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(filepath.Join(dir, "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if got := tableTexts(t, db, "t"); fmt.Sprint(got) != fmt.Sprint([]string{"checkpointed", "wal-one"}) {
		t.Fatalf("rows = %v, want the earlier commit", got)
	}
}

func TestWALBadHeaderRefused(t *testing.T) {
	tests := []struct {
		name  string
		off   int
		value []byte
	}{
		{"magic", 0, []byte{0, 0, 0, 0}},
		{"version", 4, []byte{0, 0, 0, 1}},
		{"page size", 8, []byte{0, 0, 0x20, 0}},
		{"checksum", 24, []byte{0, 0, 0, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyWALFixture(t)
			p := filepath.Join(dir, "wal.db-wal")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			copy(b[tc.off:], tc.value)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(filepath.Join(dir, "wal.db")); !errors.Is(err, ErrWALPresent) {
				t.Fatalf("want ErrWALPresent, got %v", err)
			}
		})
	}
}

func copyWALFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(filepath.Join("testdata", "wal.db"+suffix))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wal.db"+suffix), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func truncateFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

func tableTexts(t *testing.T, db *DB, table string) []string {
	t.Helper()
	_, rows := collect(t, db, table)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[0].Text)
	}
	return out
}

// walFixtureFrame is one frame read back from a fixture log.
type walFixtureFrame struct {
	pgno   uint32
	commit uint32
	page   []byte
}

func readWALFixture(t *testing.T) (uint32, uint32, []walFixtureFrame) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "wal.db-wal"))
	if err != nil {
		t.Fatal(err)
	}
	salt1 := binary.BigEndian.Uint32(b[16:20])
	salt2 := binary.BigEndian.Uint32(b[20:24])
	pages := make([]walFixtureFrame, 0, (len(b)-walHeaderSize)/(walFrameSize+4096))
	for off := walHeaderSize; off+walFrameSize+4096 <= len(b); off += walFrameSize + 4096 {
		pages = append(pages, walFixtureFrame{
			pgno:   binary.BigEndian.Uint32(b[off : off+4]),
			commit: binary.BigEndian.Uint32(b[off+4 : off+8]),
			page:   append([]byte(nil), b[off+walFrameSize:off+walFrameSize+4096]...),
		})
	}
	return salt1, salt2, pages
}

// writeWALFixture writes a log with the given magic, salts and frames, so a
// test can produce shapes a real writer rarely leaves behind.
func writeWALFixture(t *testing.T, path string, magic, salt1, salt2 uint32, frames []walFixtureFrame) {
	t.Helper()
	bigEndian := magic&1 == 1
	hdr := make([]byte, walHeaderSize)
	binary.BigEndian.PutUint32(hdr[0:4], magic)
	binary.BigEndian.PutUint32(hdr[4:8], walVersion)
	binary.BigEndian.PutUint32(hdr[8:12], 4096)
	binary.BigEndian.PutUint32(hdr[16:20], salt1)
	binary.BigEndian.PutUint32(hdr[20:24], salt2)
	s1, s2 := walChecksum(hdr[:24], 0, 0, bigEndian)
	binary.BigEndian.PutUint32(hdr[24:28], s1)
	binary.BigEndian.PutUint32(hdr[28:32], s2)
	out := append([]byte(nil), hdr...)
	for _, f := range frames {
		frame := make([]byte, walFrameSize)
		binary.BigEndian.PutUint32(frame[0:4], f.pgno)
		binary.BigEndian.PutUint32(frame[4:8], f.commit)
		binary.BigEndian.PutUint32(frame[8:12], salt1)
		binary.BigEndian.PutUint32(frame[12:16], salt2)
		fs1, fs2 := walChecksum(frame[:8], s1, s2, bigEndian)
		fs1, fs2 = walChecksum(f.page, fs1, fs2, bigEndian)
		binary.BigEndian.PutUint32(frame[16:20], fs1)
		binary.BigEndian.PutUint32(frame[20:24], fs2)
		s1, s2 = fs1, fs2
		out = append(out, frame...)
		out = append(out, f.page...)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openFixtureWithFrames(t *testing.T, frames []walFixtureFrame) *DB {
	t.Helper()
	dir := copyWALFixture(t)
	salt1, salt2, _ := readWALFixture(t)
	writeWALFixture(t, filepath.Join(dir, "wal.db-wal"), 0x377f0682, salt1, salt2, frames)
	db, err := Open(filepath.Join(dir, "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWALBigEndianChecksum(t *testing.T) {
	dir := copyWALFixture(t)
	salt1, salt2, frames := readWALFixture(t)
	writeWALFixture(t, filepath.Join(dir, "wal.db-wal"), 0x377f0683, salt1, salt2, frames)
	db, err := Open(filepath.Join(dir, "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if got := tableTexts(t, db, "t"); len(got) != 3 {
		t.Fatalf("rows = %v, want all three", got)
	}
}

func TestWALUncommittedTailIgnored(t *testing.T) {
	_, _, frames := readWALFixture(t)
	tail := walFixtureFrame{pgno: 2, commit: 0, page: make([]byte, 4096)}
	db := openFixtureWithFrames(t, append(frames, tail))
	if got := tableTexts(t, db, "t"); len(got) != 3 {
		t.Fatalf("rows = %v, want the committed three", got)
	}
}

func TestWALSaltMismatchStops(t *testing.T) {
	salt1, salt2, frames := readWALFixture(t)
	dir := copyWALFixture(t)
	p := filepath.Join(dir, "wal.db-wal")
	writeWALFixture(t, p, 0x377f0682, salt1, salt2, frames)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// A frame from another log generation: the salts no longer match, so its
	// zeroed page must never reach the overlay.
	frame := make([]byte, walFrameSize+4096)
	binary.BigEndian.PutUint32(frame[0:4], 2)
	binary.BigEndian.PutUint32(frame[4:8], 2)
	binary.BigEndian.PutUint32(frame[8:12], salt1+1)
	binary.BigEndian.PutUint32(frame[12:16], salt2)
	if err := os.WriteFile(p, append(b, frame...), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(filepath.Join(dir, "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if got := tableTexts(t, db, "t"); len(got) != 3 {
		t.Fatalf("rows = %v, want the committed three", got)
	}
}

func TestWALPageZeroStops(t *testing.T) {
	_, _, frames := readWALFixture(t)
	zero := walFixtureFrame{pgno: 0, commit: 2, page: make([]byte, 4096)}
	db := openFixtureWithFrames(t, append(frames, zero))
	if got := tableTexts(t, db, "t"); len(got) != 3 {
		t.Fatalf("rows = %v, want the committed three", got)
	}
}

// TestWALCommitSizeLimitsPages proves that a log declaring a smaller database
// hides the surviving pages of the file instead of serving them as if they
// were part of this snapshot.
func TestWALCommitSizeLimitsPages(t *testing.T) {
	_, _, frames := readWALFixture(t)
	first := frames[0]
	first.commit = 1
	db := openFixtureWithFrames(t, []walFixtureFrame{first})
	tbl, err := db.Table("t")
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	if err := tbl.Rows(func(int64, []Value) error { return nil }); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt reading past the committed size, got %v", err)
	}
}

func TestWALReadErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "db.db")
	b, err := os.ReadFile(filepath.Join("testdata", "basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p+"-wal", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("want an error for a log that cannot be read")
	}
}

func TestTableColumns(t *testing.T) {
	db := openDB(t, "basic.db")
	tbl, err := db.Table("kv")
	if err != nil {
		t.Fatal(err)
	}
	cols, err := tbl.Columns()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if fmt.Sprint(cols) != fmt.Sprint([]string{"key", "value"}) {
		t.Fatalf("columns = %v", cols)
	}
	if tbl.SQL == "" {
		t.Fatal("SQL is empty")
	}
}

func TestParseColumns(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{"quoted", "CREATE TABLE `t` (`a` text, `b` integer)", []string{"a", "b"}},
		{"constraint skipped", "CREATE TABLE t (a text, CONSTRAINT fk FOREIGN KEY (a) REFERENCES p(b), PRIMARY KEY (a))", []string{"a"}},
		{"nested parens", "CREATE TABLE t (a text check (a in ('x','y')), b int)", []string{"a", "b"}},
		{"no type", "CREATE TABLE t (a, b)", []string{"a", "b"}},
		{"brackets", "CREATE TABLE t ([a] text)", []string{"a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseColumns(tc.sql)
			if err != nil {
				t.Fatalf("parseColumns: %v", err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("columns = %v, want %v", got, tc.want)
			}
		})
	}
	for _, sql := range []string{"", "CREATE TABLE t", "CREATE TABLE t ()"} {
		if _, err := parseColumns(sql); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("parseColumns(%q) = %v, want ErrUnsupported", sql, err)
		}
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
