package utils

import (
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestPageCount_RoundsUpForPartialLastPage(t *testing.T) {
	tests := []struct {
		name     string
		items    int
		pageSize int
		want     int
	}{
		{
			name:     "no items still has a single page",
			items:    0,
			pageSize: 10,
			want:     0,
		},
		{
			name:     "exact page fits exactly",
			items:    10,
			pageSize: 10,
			want:     0,
		},
		{
			name:     "partial last page rounds up",
			items:    11,
			pageSize: 10,
			want:     1,
		},
		{
			name:     "multiple full pages zero-based last page index",
			items:    20,
			pageSize: 10,
			want:     1,
		},
		{
			name:     "multiple full pages plus remainder",
			items:    21,
			pageSize: 10,
			want:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pageCount(tt.items, tt.pageSize)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPrintSelectItemOptions_UsesRoundedPageCountForPrompt(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	globalTheme = Theme{
		Primary:   "<PRIMARY>",
		Secondary: "<SECONDARY>",
		Breadtext: "<BREADTEXT>",
	}

	items := make([]string, 11)
	for i := range items {
		items[i] = "item"
	}

	format := func(i int, s string) (string, error) {
		return strings.Repeat("-", 20), nil
	}

	out := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := printSelectItemOptions(
			1, 10, len(items), items, "[page %d/%d] Select item, [p]rev, [q]uit: ", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	if !strings.Contains(out, "[page 1/1]") {
		t.Fatalf("got output %q, want page indicator for rounded last page", out)
	}
}

func TestPrintSelectItemOptions_DoesNotFormatNonTemplatedPrompt(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	globalTheme = Theme{
		Primary:   "<PRIMARY>",
		Secondary: "<SECONDARY>",
		Breadtext: "<BREADTEXT>",
	}

	items := []string{"a", "b"}
	format := func(i int, s string) (string, error) {
		return s, nil
	}

	out := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := printSelectItemOptions(
			0, 10, len(items), items, "Select category, [p]rev, [q]uit: ", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	if strings.Contains(out, "%!(EXTRA") {
		t.Fatalf("unexpected fmt extra output: %q", out)
	}
	if !strings.Contains(out, "Select category, [p]rev, [q]uit: ") {
		t.Fatalf("missing prompt in output: %q", out)
	}
}

func TestPrintSelectItemOptions_DoesNotDuplicatePrevQuitOnPagedPrompt(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	globalTheme = Theme{
		Primary:   "<PRIMARY>",
		Secondary: "<SECONDARY>",
		Breadtext: "<BREADTEXT>",
	}

	items := make([]string, 11)
	for i := range items {
		items[i] = "item"
	}
	format := func(i int, s string) (string, error) {
		return s, nil
	}

	out := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := printSelectItemOptions(
			1, 2, len(items), items, "select config, [p]rev, [q]uit: ", format,
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	if strings.Count(out, "[p]rev") != 1 {
		t.Fatalf("expected single [p]rev occurrence, got %q", out)
	}
	if strings.Count(out, "[q]uit") != 1 {
		t.Fatalf("expected single [q]uit occurrence, got %q", out)
	}
}
