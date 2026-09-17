package vendors_test

import (
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
)

// TestLineFields_closedSet holds LineFields to the fields the worklog's
// parameters table declares. A field enters only when the chat list gains a
// capability that needs it eagerly, which is a worklog decision: this test
// fails until that decision is recorded.
func TestLineFields_closedSet(t *testing.T) {
	want := []string{
		"SessionID string",
		"Cwd string",
		"Timestamp time.Time",
		"Model string",
		"Role vendors.LineRole",
		"UserText string",
	}
	typ := reflect.TypeFor[vendors.LineFields]()
	got := make([]string, 0, typ.NumField())
	for f := range typ.Fields() {
		got = append(got, f.Name+" "+f.Type.String())
	}
	if len(got) != len(want) {
		t.Fatalf("LineFields has %d fields, want %d:\ngot  %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestJSONLSchema_surfaceIsLineOnly checks that the schema interface gives a
// vendor no way to reach a file. Only Fields takes an input at all, and that
// input is one line's bytes: no path, handle, reader or line index exists in
// the surface, so a vendor cannot decide how much of a file is read.
func TestJSONLSchema_surfaceIsLineOnly(t *testing.T) {
	want := map[string]string{
		"Fields":     "func([]uint8) vendors.LineFields",
		"Root":       "func() string",
		"SkipDirs":   "func() []string",
		"SourceName": "func() string",
	}
	typ := reflect.TypeFor[vendors.JSONLSchema]()
	if typ.NumMethod() != len(want) {
		t.Fatalf("JSONLSchema has %d methods, want %d", typ.NumMethod(), len(want))
	}
	for m := range typ.Methods() {
		sig, ok := want[m.Name]
		if !ok {
			t.Fatalf("unexpected method %s: the schema surface is closed", m.Name)
		}
		if got := m.Type.String(); got != sig {
			t.Errorf("%s has signature %q, want %q", m.Name, got, sig)
		}
		for in := range m.Type.Ins() {
			if m.Name != "Fields" || in != reflect.TypeFor[[]byte]() {
				t.Errorf("%s takes %v; only Fields may take input, and only one line's bytes", m.Name, in)
			}
		}
	}
}
