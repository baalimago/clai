package chat

import (
	"math"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// A layout gives up columns until About is listAboutMinWidth cells or
// listAboutWidthPercent of the width wide, whichever is more, or as wide as
// the widest label when that is less.
const (
	listAboutMinWidth     = 40
	listAboutWidthPercent = 40
)

// listRowMargin keeps rows off the terminal's last column.
const listRowMargin = 1

const (
	listSourceMaxWidth = 15
	listModelMaxWidth  = 24
)

const listColumnSep = " | "

type listColumn struct {
	head string
	cell func(i int, r chatListRow) string
	// maxWidth caps the column; 0 leaves it as wide as its widest cell.
	maxWidth int
}

func listIndexCell(i int, r chatListRow) string {
	if r.Kind == chatRowGroup {
		return strconv.Itoa(i) + " [group:" + strconv.Itoa(r.GroupMemberCount) + "]"
	}
	return strconv.Itoa(i)
}

func hasUsage(r chatListRow) bool { return r.Kind != chatRowForeign }

var (
	listIndexColumn  = listColumn{head: "Index", cell: listIndexCell}
	listSourceColumn = listColumn{head: "Source", maxWidth: listSourceMaxWidth, cell: func(_ int, r chatListRow) string {
		return r.displaySource()
	}}
	listCreatedColumn = listColumn{head: "Created", cell: func(_ int, r chatListRow) string {
		return r.Created.Format("2006-01-02 15:04:05")
	}}
	listCreatedShortColumn = listColumn{head: "Created", cell: func(_ int, r chatListRow) string {
		return r.Created.Format("06-01-02 15:04")
	}}
	listMessagesColumn = listColumn{head: "Messages", cell: func(_ int, r chatListRow) string {
		return strconv.Itoa(r.MessageCount)
	}}
	listProfileColumn = listColumn{head: "Profile", cell: func(_ int, r chatListRow) string {
		if !hasUsage(r) {
			return "N/A"
		}
		return listProfileDisplay(r.Profile)
	}}
	listModelColumn = listColumn{head: "Model", maxWidth: listModelMaxWidth, cell: func(_ int, r chatListRow) string {
		return r.Model
	}}
	listCostColumn = listColumn{head: "Cost", cell: func(_ int, r chatListRow) string {
		if !hasUsage(r) {
			return "N/A"
		}
		return chatIndexCostStr(chatIndexRow{TotalCostUSD: r.TotalCostUSD})
	}}
	listTokensColumn = listColumn{head: "Tokens", cell: func(_ int, r chatListRow) string {
		if !hasUsage(r) {
			return "N/A"
		}
		return chatIndexTokenStr(chatIndexRow{TotalTokens: r.TotalTokens})
	}}
)

// listColumnSets are the columns between Index and About, from the widest
// set to the narrowest; a layout takes the first that leaves About its
// share. The detail view shows every field a narrow set drops.
var listColumnSets = [][]*listColumn{
	{&listSourceColumn, &listCreatedColumn, &listMessagesColumn, &listProfileColumn, &listModelColumn, &listCostColumn, &listTokensColumn},
	{&listSourceColumn, &listCreatedColumn, &listMessagesColumn, &listProfileColumn, &listModelColumn, &listCostColumn},
	{&listSourceColumn, &listCreatedColumn, &listProfileColumn, &listModelColumn, &listCostColumn},
	{&listSourceColumn, &listCreatedColumn, &listModelColumn, &listCostColumn},
	{&listSourceColumn, &listCreatedColumn, &listModelColumn},
	{&listSourceColumn, &listCreatedShortColumn, &listModelColumn},
	{&listSourceColumn, &listCreatedShortColumn},
	{&listCreatedShortColumn},
	{},
}

// listLayout is the column set and widths of one chat list render.
type listLayout struct {
	columns    []*listColumn
	widths     []int
	aboutWidth int
}

// newListLayout fits the columns to rows on a terminal width cells wide; an
// unknown width (zero or less) shows every column and whole labels.
func newListLayout(rows []chatListRow, width int) listLayout {
	measured := map[*listColumn]int{}
	widthOf := func(c *listColumn) int {
		if w, ok := measured[c]; ok {
			return w
		}
		w := utils.TerminalStringWidth(c.head)
		for i, r := range rows {
			w = max(w, utils.TerminalStringWidth(c.cell(i, r)))
		}
		if c.maxWidth > 0 {
			w = min(w, c.maxWidth)
		}
		measured[c] = w
		return w
	}
	widestLabel := 0
	for _, r := range rows {
		widestLabel = max(widestLabel, utils.TerminalStringWidth(labelFor(r.Title, r.FirstUserMessage)))
	}
	aboutShare := min(widestLabel, max(listAboutMinWidth, width*listAboutWidthPercent/100))
	var layout listLayout
	for _, set := range listColumnSets {
		columns := append([]*listColumn{&listIndexColumn}, set...)
		widths := make([]int, len(columns))
		prefix := 0
		for i, c := range columns {
			widths[i] = widthOf(c)
			prefix += widths[i] + len(listColumnSep)
		}
		layout = listLayout{columns: columns, widths: widths, aboutWidth: width - listRowMargin - prefix}
		if width <= 0 {
			layout.aboutWidth = math.MaxInt
		}
		if layout.aboutWidth >= aboutShare {
			return layout
		}
	}
	return layout
}

func (l listLayout) line(cells []string, about string) string {
	var b strings.Builder
	for i, cell := range cells {
		cell = utils.TruncateTerminalRow(cell, l.widths[i])
		b.WriteString(cell)
		b.WriteString(strings.Repeat(" ", l.widths[i]-utils.TerminalStringWidth(cell)))
		b.WriteString(listColumnSep)
	}
	b.WriteString(about)
	return b.String()
}

func (l listLayout) header() string {
	heads := make([]string, len(l.columns))
	for i, c := range l.columns {
		heads[i] = c.head
	}
	return l.line(heads, "About")
}

// prefix is the row up to its About cell.
func (l listLayout) prefix(i int, r chatListRow) string {
	cells := make([]string, len(l.columns))
	for c, col := range l.columns {
		cells[c] = col.cell(i, r)
	}
	return l.line(cells, "")
}

func (l listLayout) row(i int, r chatListRow) string {
	return l.prefix(i, r) + utils.TruncateTerminalRow(labelFor(r.Title, r.FirstUserMessage), l.aboutWidth)
}
