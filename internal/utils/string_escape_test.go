package utils

import "testing"

func TestEditorStringEscapeRoundTrip(t *testing.T) {
	raw := "line1\nline2\tX"
	if got := UnescapeEditorString(EscapeEditorString(raw)); got != raw {
		t.Fatalf("roundtrip mismatch: want %q got %q", raw, got)
	}
}

func TestUnescapeEditorString(t *testing.T) {
	got := UnescapeEditorString("a\\nb\\tZ")
	want := "a\nb\tZ"
	if got != want {
		t.Fatalf("unexpected unescape: want %q got %q", want, got)
	}
}
