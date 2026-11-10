package runenv

import "testing"

func TestOpenRow(t *testing.T) {
	New(Config{}).MarkRow(true)
	row := NewOpenRow()
	env := New(Config{Row: row})
	if row.End() {
		t.Fatal("a fresh row is open")
	}
	env.MarkRow(true)
	if !row.End() || row.End() {
		t.Fatal("want End to report the open row once")
	}
	env.MarkRow(true)
	env.MarkRow(false)
	if row.End() {
		t.Fatal("a row ended by its text is still open")
	}
}
