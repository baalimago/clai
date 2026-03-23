package chatid

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNew_returnsUUIDv7FormattedID(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 uuid parts, got %d in %q", len(parts), id)
	}

	wantLens := []int{8, 4, 4, 4, 12}
	for i, want := range wantLens {
		if len(parts[i]) != want {
			t.Fatalf("part %d length: got %d want %d in %q", i, len(parts[i]), want, id)
		}
	}

	if parts[2][0] != '7' {
		t.Fatalf("expected version 7 in %q", id)
	}

	variantNibble, err := strconv.ParseUint(string(parts[3][0]), 16, 8)
	if err != nil {
		t.Fatalf("ParseUint(variant nibble): %v", err)
	}
	if variantNibble < 8 || variantNibble > 11 {
		t.Fatalf("expected RFC variant nibble in [8,b], got %x for %q", variantNibble, id)
	}
}

func TestNew_sameCallSiteProducesDistinctIDs(t *testing.T) {
	first, err := New()
	if err != nil {
		t.Fatalf("New first: %v", err)
	}
	second, err := New()
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	if first == second {
		t.Fatalf("expected unique ids, both were %q", first)
	}
}

func TestNew_encodesCurrentTimestampPrefix(t *testing.T) {
	before := time.Now().UnixMilli()
	id, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	after := time.Now().UnixMilli()

	prefix := strings.ReplaceAll(strings.Join(strings.Split(id, "-")[:2], ""), "-", "")
	gotMillis, err := strconv.ParseInt(prefix, 16, 64)
	if err != nil {
		t.Fatalf("ParseInt(timestamp prefix): %v", err)
	}

	if gotMillis < before || gotMillis > after {
		t.Fatalf("timestamp prefix out of range: got %d want between %d and %d for id %q", gotMillis, before, after, id)
	}
}
