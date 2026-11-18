package chat

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

// layoutSample has fixed cell widths: index 12 ("2 [group:12]"), source 11,
// created 19 (short 14), messages 8 (its header), profile 20, model 15,
// cost 6 and tokens 6.
func layoutSample() []chatListRow {
	created := time.Date(2026, 11, 18, 9, 49, 35, 0, time.UTC)
	return []chatListRow{
		{
			Kind: chatRowNative, Created: created, ChatID: "native",
			Profile: "cchub:clai/summarizer@1", Model: "gpt-6.1-sol", MessageCount: 56,
			TotalTokens: 313_000, TotalCostUSD: 0.132,
			Title: "Summarize the edge router incident timeline for the weekly report",
		},
		{
			Kind: chatRowForeign, Created: created, Source: "claude-code", SourceID: "s1",
			Model: "claude-opus-5-5", MessageCount: 44, FirstUserMessage: "foreign first message",
		},
		{
			Kind: chatRowGroup, Created: created, Source: "claude-code", GroupMemberCount: 12,
			Model: "claude-opus-5-5", MessageCount: 6368, FirstUserMessage: "grouped first message",
		},
	}
}

func headerColumns(header string) []string {
	var cols []string
	for c := range strings.SplitSeq(header, "|") {
		cols = append(cols, strings.TrimSpace(c))
	}
	return cols
}

func TestListLayout_dropsColumnsByPriority(t *testing.T) {
	all := []string{"Index", "Source", "Created", "Messages", "Profile", "Model", "Cost", "Tokens", "About"}
	noTokens := []string{"Index", "Source", "Created", "Messages", "Profile", "Model", "Cost", "About"}
	noMessages := []string{"Index", "Source", "Created", "Profile", "Model", "Cost", "About"}
	noProfile := []string{"Index", "Source", "Created", "Model", "Cost", "About"}
	noCost := []string{"Index", "Source", "Created", "Model", "About"}
	noModel := []string{"Index", "Source", "Created", "About"}
	// The widest label takes 65 cells; About wants it, capped at 40 cells
	// or 40% of the width, whichever is more.
	for _, tc := range []struct {
		width int
		want  []string
	}{
		{0, all},
		{300, all},
		{187, all},
		{186, noTokens},
		{178, noTokens},
		{177, noMessages},
		{167, noMessages},
		{166, noProfile},
		{131, noProfile},
		{130, noCost},
		{116, noCost},
		{115, noCost},
		{107, noCost},
		{106, noModel},
		{87, noModel},
		{86, []string{"Index", "Created", "About"}},
		{73, []string{"Index", "Created", "About"}},
		{72, []string{"Index", "About"}},
		{60, []string{"Index", "About"}},
	} {
		got := headerColumns(newListLayout(layoutSample(), tc.width).header())
		if !slices.Equal(got, tc.want) {
			t.Errorf("width %d: columns %q, want %q", tc.width, got, tc.want)
		}
	}
}

func TestListLayout_shortLabelsKeepTheColumns(t *testing.T) {
	rows := layoutSample()
	rows[0].Title = "edge router incident"
	layout := newListLayout(rows, 145)
	if got := headerColumns(layout.header()); !slices.Contains(got, "Tokens") {
		t.Fatalf("labels of at most 21 cells fit beside every column at 145, got %q", got)
	}
	if row := layout.row(0, rows[0]); !strings.HasSuffix(row, "| edge router incident") {
		t.Fatalf("the short label should show whole, got %q", row)
	}
}

func TestListLayout_createdShortensBeforeModelDrops(t *testing.T) {
	rows := layoutSample()
	wide := newListLayout(rows, 116).row(0, rows[0])
	if !strings.Contains(wide, "2026-11-18 09:49:35") {
		t.Fatalf("at 116 columns the full timestamp should show, got %q", wide)
	}
	short := newListLayout(rows, 115).row(0, rows[0])
	if strings.Contains(short, "2026-11-18") || !strings.Contains(short, "26-11-18 09:49") || !strings.Contains(short, "gpt-6.1-sol") {
		t.Fatalf("at 115 columns the short timestamp should replace the full one before the model drops, got %q", short)
	}
}

func TestListLayout_rowsFitAndShowTheLabel(t *testing.T) {
	rows := layoutSample()
	for width := 60; width <= 240; width++ {
		layout := newListLayout(rows, width)
		lines := []string{layout.header()}
		for i, r := range rows {
			lines = append(lines, layout.row(i, r))
		}
		for _, line := range lines {
			if cells := utils.TerminalStringWidth(line); cells > width-listRowMargin {
				t.Fatalf("width %d: line is %d cells, wraps: %q", width, cells, line)
			}
		}
		if !strings.Contains(lines[1], "| Summarize the edge") {
			t.Fatalf("width %d: the label is missing from the row: %q", width, lines[1])
		}
	}
}

func TestListLayout_aboutKeepsItsShare(t *testing.T) {
	rows := layoutSample()
	full := utils.TerminalStringWidth(labelFor(rows[0].Title, rows[0].FirstUserMessage))
	for width := 56; width <= 300; width++ {
		layout := newListLayout(rows, width)
		label := strings.TrimPrefix(layout.row(0, rows[0]), layout.prefix(0, rows[0]))
		want := min(full, max(listAboutMinWidth, width*listAboutWidthPercent/100))
		if got := utils.TerminalStringWidth(label); got < want {
			t.Fatalf("width %d: About got %d cells, want at least %d: %q", width, got, want, label)
		}
	}
}

func TestListLayout_aboutTruncatesAtTheTail(t *testing.T) {
	rows := layoutSample()
	row := newListLayout(rows, 100).row(0, rows[0])
	if !strings.HasSuffix(row, "…") || !strings.Contains(row, "| Summarize the edge router") {
		t.Fatalf("expected the head of the title and a trailing ellipsis, got %q", row)
	}
	if strings.Contains(row, " ... ") {
		t.Fatalf("the label must not be cut in the middle, got %q", row)
	}
}

func TestListLayout_unknownWidthKeepsTheWholeLabel(t *testing.T) {
	rows := layoutSample()
	row := newListLayout(rows, 0).row(0, rows[0])
	if !strings.HasSuffix(row, rows[0].Title) {
		t.Fatalf("an unknown width must not truncate the label, got %q", row)
	}
}

func TestListLayout_wideRunesCountAsTwoCells(t *testing.T) {
	rows := layoutSample()
	rows[0].Title = strings.Repeat("界", 80)
	for _, width := range []int{80, 101, 133} {
		row := newListLayout(rows, width).row(0, rows[0])
		if cells := utils.TerminalStringWidth(row); cells > width-listRowMargin {
			t.Fatalf("width %d: a wide-rune label takes %d cells: %q", width, cells, row)
		}
	}
}

func TestListLayout_capsLongCellsAndKeepsAlignment(t *testing.T) {
	rows := layoutSample()
	rows[0].Model = "a-model-name-far-longer-than-its-column"
	rows[1].Source = "a-source-name-longer-than-fifteen"
	layout := newListLayout(rows, 200)
	header := layout.header()
	for i, r := range rows {
		row := layout.row(i, r)
		for n := 1; n <= 8; n++ {
			if cellIndex(row, "|", n) != cellIndex(header, "|", n) {
				t.Fatalf("separator %d misaligned:\n%s\n%s", n, header, row)
			}
		}
	}
	if row := layout.row(0, rows[0]); !strings.Contains(row, "a-model-name-far-longer…") {
		t.Fatalf("expected the model cut to %d cells, got %q", listModelMaxWidth, row)
	}
	if row := layout.row(1, rows[1]); !strings.Contains(row, "a-source-name-…") {
		t.Fatalf("expected the source cut to %d cells, got %q", listSourceMaxWidth, row)
	}
}

func TestListLayout_foreignRowsShowNoUsage(t *testing.T) {
	rows := layoutSample()
	row := newListLayout(rows, 200).row(1, rows[1])
	cols := headerColumns(row)
	// Index, Source, Created, Messages, Profile, Model, Cost, Tokens, About
	if cols[4] != "N/A" || cols[6] != "N/A" || cols[7] != "N/A" {
		t.Fatalf("a foreign row has no profile, cost or tokens, got %q", cols)
	}
	if cols[0] != "1" || cols[1] != "claude-code" || cols[3] != "44" {
		t.Fatalf("unexpected foreign row cells %q", cols)
	}
}

func TestListLayout_groupRowIndexAndUsage(t *testing.T) {
	rows := layoutSample()
	rows[2].TotalTokens, rows[2].TotalCostUSD = 18_000_000, 4.359
	cols := headerColumns(newListLayout(rows, 200).row(2, rows[2]))
	if cols[0] != "2 [group:12]" || cols[6] != "$4.359" || cols[7] != "18.0M" {
		t.Fatalf("unexpected group row cells %q", cols)
	}
}

// cellIndex is the terminal column of the nth sep in s.
func cellIndex(s, sep string, n int) int {
	at := nthIndex(s, sep, n)
	if at < 0 {
		return -1
	}
	return utils.TerminalStringWidth(s[:at])
}
