package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
	"golang.org/x/text/width"
)

var (
	ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCPattern    = regexp.MustCompile(`\x1b\][^\a\x1b]*(?:\a|\x1b\\)`)
)

const MaxShortenedNewlines = 5

// UpdateMessageTerminalMetadata updates the terminal metadata. Meaning the lineCount, to eventually
// clear the terminal
func UpdateMessageTerminalMetadata(msg string, line *string, lineCount *int, termWidth int) {
	if termWidth <= 0 {
		termWidth = 1
	}

	newlineSplit := strings.Split(*line+msg, "\n")
	*lineCount = TerminalRows(*line+msg, termWidth)

	lastSegment := newlineSplit[len(newlineSplit)-1]
	if len(lastSegment) > termWidth {
		lastWords := strings.Split(lastSegment, " ")
		lastWord := lastWords[len(lastWords)-1]
		if len(lastWord) > termWidth {
			*line = lastWord[len(lastWord)-termWidth:]
		} else {
			*line = lastWord
		}
	} else {
		*line = lastSegment
	}
}

// AttemptPrettyPrint by first checking if the glow command is available, and if so, pretty print the chat message.
// If not found, simply print the message as is.
// If the message has ReasoningContent, it is rendered with reasoning color before the main content.
//
// If w is nil, os.Stdout is used.
func AttemptPrettyPrint(w io.Writer, chatMessage pub_models.Message, username string, raw bool) error {
	if w == nil {
		w = os.Stdout
	}

	content := chatMessage.Content

	if raw {
		if chatMessage.ReasoningContent != "" {
			fmt.Fprintln(w, "[thinking]")
			fmt.Fprintln(w, chatMessage.ReasoningContent)
			fmt.Fprintln(w, "[/thinking]")
		}
		fmt.Fprintln(w, content)
		return nil
	}

	role := chatMessage.Role
	if chatMessage.Role == "user" {
		role = username
	}

	// Respect NO_COLOR.
	if table.NoColor() {
		if chatMessage.ReasoningContent != "" {
			if _, err := fmt.Fprintf(w, "[thinking]\n%v\n[/thinking]\n%v: %v\n", chatMessage.ReasoningContent, role, content); err != nil {
				return fmt.Errorf("write chat message with reasoning: %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(w, "%v: %v\n", role, content); err != nil {
			return fmt.Errorf("write chat message: %w", err)
		}
		return nil
	}

	roleCol := RoleColor(chatMessage.Role)
	coloredRole := table.Colorize(roleCol, role)

	// Glow is an interactive terminal renderer. For captured output (pipes,
	// files, test buffers) spawning it adds subprocess latency and ANSI noise,
	// so only a terminal destination gets the glow path; captured output and
	// machines without glow share the plain ANSI fallback.
	if !isTerminalWriter(w) || !glowAvailable() {
		// No glow: print with ANSI coloring.
		if chatMessage.ReasoningContent != "" {
			reasoningCol := RoleColor("reasoning")
			if _, err := fmt.Fprintf(w, "%v:\n%v\n", coloredRole,
				table.Colorize(reasoningCol, "[thinking]\n"+chatMessage.ReasoningContent+"\n[/thinking]\n"+content)); err != nil {
				return fmt.Errorf("write chat message (no glow, reasoning): %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(w, "%v: %v\n", coloredRole, content); err != nil {
			return fmt.Errorf("write chat message (no glow): %w", err)
		}
		return nil
	}

	// Glow available: print reasoning with ANSI coloring, then run glow on content.
	if _, err := fmt.Fprintf(w, "%v:", coloredRole); err != nil {
		return fmt.Errorf("write role prefix: %w", err)
	}

	if chatMessage.ReasoningContent != "" {
		reasoningCol := RoleColor("reasoning")
		if _, err := fmt.Fprintf(w, "\n%v", table.Colorize(reasoningCol, "[thinking]\n"+chatMessage.ReasoningContent+"\n[/thinking]")); err != nil {
			return fmt.Errorf("write reasoning content: %w", err)
		}
	}

	termWidth := SessionDimensions(w).Width

	cmd := exec.Command("glow", glowRenderArgs(termWidth)...)
	inp := content
	// For some reason glow hides specifically <thikning>. So, replace it to [thinking]
	inp = strings.ReplaceAll(inp, "<thinking>", "[thinking]")
	inp = strings.ReplaceAll(inp, "</thinking>", "[/thinking]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run glow: %w", err)
	}
	return nil
}

// isTerminalWriter reports whether w is a character device — the standard
// heuristic for "this writer is a terminal" (internal/utils/prompt.go uses
// the same check for stdin). A nil writer resolves to os.Stdout.
func isTerminalWriter(w io.Writer) bool {
	if w == nil {
		w = os.Stdout
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// IsTerminalWriter reports whether w resolves to a terminal writer.
func IsTerminalWriter(w io.Writer) bool { return isTerminalWriter(w) }

// glowAvailable reports whether the glow markdown renderer is installed. The
// probe spawns a subprocess, so it runs once per process.
var glowAvailable = sync.OnceValue(func() bool {
	return exec.Command("glow", "--version").Run() == nil
})

// glowRenderArgs builds the glow renderer arguments for the given terminal
// width, leaving five columns clear for the role prefix.
func glowRenderArgs(termWidth int) []string {
	return []string{"-w", strconv.Itoa(max(termWidth-5, 1))}
}

// ShortenedOutput returns a shortened version of the output
func ShortenedOutput(out string, maxShortenedNewlines int) string {
	maxTokens := 20
	maxRunes := 100
	outSplit := strings.Split(out, " ")
	outNewlineSplit := strings.Split(out, "\n")
	firstTokens := GetFirstTokens(outSplit, maxTokens)
	amRunes := utf8.RuneCountInString(out)
	if len(firstTokens) < maxTokens && len(outNewlineSplit) < maxShortenedNewlines && amRunes < maxRunes {
		return out
	}
	firstTokensStr := strings.Join(firstTokens, " ")
	amLeft := len(outSplit) - maxTokens
	abbreviationType := "tokens"
	if len(outNewlineSplit) > maxShortenedNewlines {
		firstTokensStr = strings.Join(GetFirstTokens(outNewlineSplit, maxShortenedNewlines), "\n")
		amLeft = len(outNewlineSplit) - maxShortenedNewlines
		abbreviationType = "lines"
		return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
	}
	if amRunes > maxRunes {
		end, runes := len(out), 0
		for i := range out {
			if runes == maxRunes {
				end = i
				break
			}
			runes++
		}
		return fmt.Sprintf("%v\n...[and %v more runes]", out[:end], amRunes-maxRunes)
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

// PrintToolActivity prints one non-raw tool execution as a header and its result.
// The model transcript remains independent from this display-only representation.
func PrintToolActivity(w io.Writer, call pub_models.Call, content string, termWidth, maxRows int) error {
	return PrintToolBlock(w, call, content, nil, termWidth, maxRows)
}

// PrintToolBlock prints a tool block as PrintToolActivity does, then rows as they are,
// indented but never compacted, cut or sanitized, so the terminal wraps them; callers
// pass only text they produced or sanitized. Empty content with rows prints no content
// row.
func PrintToolBlock(w io.Writer, call pub_models.Call, content string, rows []string, termWidth, maxRows int) error {
	if w == nil {
		w = os.Stdout
	}
	header := truncateTerminalRow(toolActivityHeader(call), max(termWidth, 1))
	if _, err := fmt.Fprintln(w, table.Colorize(TableTheme().Primary, header)); err != nil {
		return fmt.Errorf("write tool activity header: %w", err)
	}
	if content != "" || len(rows) == 0 {
		if err := printToolContent(w, call, content, termWidth, maxRows); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "  %s\n", row); err != nil {
			return fmt.Errorf("write tool block row: %w", err)
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func printToolContent(w io.Writer, call pub_models.Call, content string, termWidth, maxRows int) error {
	content = SummarizeAsyncToolResult(call.Name, content)
	content = SanitizeTerminalText(content)
	contentWidth := max(termWidth-2, 1)
	rows := compactTerminalRows(content, contentWidth, maxRows)
	for i, row := range rows {
		isMarker := isToolOutputMarker(row)
		row = truncateTerminalRow(row, contentWidth)
		if isMarker {
			row = table.Colorize(TableTheme().Secondary, row)
		}
		if i == 0 && isToolError(content) {
			row = table.Colorize(RoleColor("tool"), truncateTerminalRow("✗ "+row, contentWidth))
		}
		if _, err := fmt.Fprintf(w, "  %s\n", row); err != nil {
			return fmt.Errorf("write tool output: %w", err)
		}
	}
	return nil
}

func toolActivityHeader(call pub_models.Call) string {
	var header strings.Builder
	header.WriteString("▸ ")
	header.WriteString(SanitizeTerminalRow(displayToolName(call.Name)))
	for _, key := range sortedInputKeys(call.Inputs) {
		fmt.Fprintf(&header, "  %s=%s", SanitizeTerminalRow(key), SanitizeTerminalRow(displayInputValue((*call.Inputs)[key])))
	}
	return strings.TrimSpace(header.String())
}

func displayToolName(name string) string {
	if !strings.HasPrefix(name, "mcp_") {
		return name
	}
	name = strings.TrimPrefix(name, "mcp_")
	server, tool, found := strings.Cut(name, "_")
	if found {
		return server + "." + tool
	}
	return name
}

func sortedInputKeys(inputs *pub_models.Input) []string {
	if inputs == nil {
		return nil
	}
	keys := make([]string, 0, len(*inputs))
	for key := range *inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func displayInputValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.Join(strings.Fields(SanitizeTerminalText(text)), " ")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// TerminalRows is how many terminal rows text takes at width once each line wraps;
// escape sequences take no cells and a tab moves to the next tab stop. A width of zero
// or less wraps nothing.
func TerminalRows(text string, width int) int {
	rows := 0
	for line := range strings.SplitSeq(text, "\n") {
		rows += lineRows(line, width)
	}
	return rows
}

// tabStop is the column interval of a terminal's tab stops.
const tabStop = 8

// wraps reports whether a rune cells wide, written at col, starts the next row.
func wraps(col, cells, width int) bool { return col > 0 && col+cells > width }

// lineRows is how many rows a terminal width columns wide draws line in; a tab
// stops at the last column, and a wide rune that does not fit starts the next row.
func lineRows(line string, width int) int {
	rows, col := 1, 0
	for _, r := range ansiOSCPattern.ReplaceAllString(ansiEscapePattern.ReplaceAllString(line, ""), "") {
		switch {
		case r == '\t':
			col = (col/tabStop + 1) * tabStop
			if width > 0 {
				col = min(col, width-1)
			}
		case r == '\r':
			col = 0
		case unicode.IsControl(r):
		default:
			w := terminalRuneWidth(r)
			if width > 0 && wraps(col, w, width) {
				rows++
				col = 0
			}
			col += w
		}
	}
	return rows
}

// SanitizeTerminalText removes escape sequences and control characters from text
// that will be written to a terminal; newlines stay and a tab becomes a space.
func SanitizeTerminalText(text string) string {
	text = ansiEscapePattern.ReplaceAllString(text, "")
	text = ansiOSCPattern.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		switch r {
		case '\n':
			return r
		case '\t':
			return ' '
		case '\r':
			return -1
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	return text
}

// SanitizeTerminalRow makes untrusted text safe for a single terminal row.
func SanitizeTerminalRow(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' {
			return ' '
		}
		return r
	}, SanitizeTerminalText(strings.NewReplacer("\r\n", "\n", "\r", " ").Replace(text))))
}

// ActivityViewport keeps a bounded, display-only view of streamed reasoning,
// assistant prose, and tool activity. It never changes model or tool source data.
//
// Storage is logical, not visual: each block keeps the full sanitized content
// (the source of truth) plus its display policy (kind, header and role colours,
// 2-space body indentation, and tool output-marker and ERROR: handling).
// Colours and indentation are re-derived at rewrap time, so a width change
// rewraps every retained block instead of reconstructing content from already
// wrapped rows. The active reasoning and text blocks coalesce streamed tokens
// into one open block; finished blocks keep their logical content until the
// retention policy evicts them (maxActivityBlocks blocks, each bounded by
// maxActivityBlockRunes). A terminal-height grow can therefore bring retained
// content back into view, bounded by retention, not by the last visible row
// count.
type ActivityViewport struct {
	width   int
	maxRows int
	// height is the effective height: min(configured cap, terminal height).
	height int
	// header is the pinned block above every activity row. Each line is
	// sanitized to one logical line and wrapped at the window width. The
	// header is never evicted, trimmed by the height bound, or scrolled out
	// of the window (worklog 2026-10-21-clai-loop, loop progress).
	header     []HeaderLine
	headerRows []activityRow
	blocks     []*activityBlock
	// activeReasoning and activeText are the open coalescing blocks. They are
	// always the last blocks in the history and are never evicted.
	activeReasoning *activityBlock
	activeText      *activityBlock
	rows            []activityRow
	drawnRows       int
	lastRendered    []string
	// dirty marks that the on-screen region no longer matches the viewport
	// state (after Resize or a partial write); the next Render emits a complete
	// atomic frame instead of a diff.
	dirty bool
}

// maxActivityBlocks bounds the number of logical blocks retained in the
// activity history. Finished blocks keep their logical content until this
// retention policy evicts them; the open coalescing blocks always survive.
const maxActivityBlocks = 16

// maxActivityBlockRunes bounds the sanitized content retained per logical
// block. The bound exists to keep memory bounded; realistic activity blocks
// never reach it. Content beyond the budget is reduced to its head and tail
// (mirroring the display policy), so a later rewrap still has the content the
// policy would show.
const maxActivityBlockRunes = 64 * 1024

// maxActivityHeaderRows bounds the pinned header block. A long header line
// wraps over several rows. The bound leaves at least one row for activity
// whenever the window is taller than one row.
const maxActivityHeaderRows = 4

// HeaderLine is one logical line of the pinned header, built from coloured
// spans. The spans are sanitized to one logical line and wrapped together at
// the window width, so a wrapped row keeps the colour of each of its parts.
type HeaderLine struct {
	Spans []HeaderSpan
}

// HeaderSpan is one coloured run of a header line.
type HeaderSpan struct {
	Text  string
	Color string
}

// contentTruncationMarker separates the retained head and tail of an
// over-budget block.
const contentTruncationMarker = "\n...[content truncated]...\n"

// blockKind identifies the display policy of a logical activity block.
type blockKind uint8

const (
	blockReasoning blockKind = iota
	blockText
	blockTool
	blockMcpLog
)

// activityBlock is one logical activity unit. Content is the sanitized source
// of truth; rows is a display cache re-derived at rewrap time (Resize and
// streaming updates).
type activityBlock struct {
	kind    blockKind
	content string
	// call is the tool-call metadata used to render the tool header.
	call pub_models.Call
	// server is the MCP server name used to render a server-log header.
	server string
	// toolBodyRows is the body compaction budget of tool blocks.
	toolBodyRows int
	rows         []activityRow
}

// activityRow is one physical row of the viewport. A row with one colour keeps
// it in color; a row that carries several coloured runs keeps them in spans and
// leaves color empty.
type activityRow struct {
	content string
	color   string
	spans   []activitySpan
}

// activitySpan is one coloured run of a row.
type activitySpan struct {
	text  string
	color string
}

// coloredSpans returns the coloured runs of the row. A single-colour row is one
// run.
func (r activityRow) coloredSpans() []activitySpan {
	if len(r.spans) == 0 {
		return []activitySpan{{text: r.content, color: r.color}}
	}
	return r.spans
}

// render applies the row's colours.
func (r activityRow) render() string {
	if len(r.spans) == 0 {
		return table.Colorize(r.color, r.content)
	}
	var b strings.Builder
	for _, span := range r.spans {
		b.WriteString(table.Colorize(span.color, span.text))
	}
	return b.String()
}

// appendSpan appends text to spans, merging it into the last run when the
// colour matches.
func appendSpan(spans []activitySpan, text, color string) []activitySpan {
	if text == "" {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].color == color {
		spans[n-1].text += text
		return spans
	}
	return append(spans, activitySpan{text: text, color: color})
}

// rowFromSpans builds a row from coloured runs, collapsing a single run to the
// plain content-and-colour form.
func rowFromSpans(spans []activitySpan) activityRow {
	var merged []activitySpan
	for _, span := range spans {
		merged = appendSpan(merged, span.text, span.color)
	}
	if len(merged) == 1 {
		return activityRow{content: merged[0].text, color: merged[0].color}
	}
	var content strings.Builder
	for _, span := range merged {
		content.WriteString(span.text)
	}
	return activityRow{content: content.String(), spans: merged}
}

// NewActivityViewport creates a viewport for all transient model activity.
// maxRows is the configured rolling-window cap; terminalHeight is the raw
// terminal height at creation. The effective height is
// min(maxRows, max(terminalHeight-1, 1)) from the start, so the viewport never
// fills the terminal: a frame that ends on the last row scrolls the screen by
// the trailing newline, which strands the frame's first row in the scrollback
// and desyncs the clear. Resize takes raw terminal dimensions and keeps the
// same bound on later dimension changes.
func NewActivityViewport(width, maxRows, terminalHeight int) *ActivityViewport {
	capRows := max(maxRows, 1)
	return &ActivityViewport{
		width:   max(width, 1),
		maxRows: capRows,
		height:  min(capRows, terminalFrameHeight(terminalHeight)),
	}
}

// terminalFrameHeight returns the tallest frame a terminal of height rows can
// hold without the frame's trailing newline scrolling the screen.
func terminalFrameHeight(terminalHeight int) int {
	return max(terminalHeight-1, 1)
}

// SetHeader replaces the pinned header block. Every span is sanitized to one
// logical line and the line is wrapped at the window width. The block is capped
// at maxActivityHeaderRows and marked with an ellipsis when clipped. An empty
// slice removes the header.
func (v *ActivityViewport) SetHeader(lines []HeaderLine) {
	if v == nil {
		return
	}
	v.header = v.header[:0]
	for _, line := range lines {
		spans := sanitizeHeaderSpans(line.Spans)
		if len(spans) == 0 {
			continue
		}
		v.header = append(v.header, HeaderLine{Spans: spans})
	}
	v.rewrap()
}

// sanitizeHeaderSpans sanitizes a line's spans to one logical line, trims the
// line edges, and merges runs that share a colour.
func sanitizeHeaderSpans(spans []HeaderSpan) []HeaderSpan {
	var runes []rune
	var colors []string
	for _, span := range spans {
		for _, r := range SanitizeTerminalText(span.Text) {
			if r == '\n' {
				r = ' '
			}
			runes = append(runes, r)
			colors = append(colors, span.Color)
		}
	}
	start, end := 0, len(runes)
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	var sanitized []HeaderSpan
	for i := start; i < end; {
		j := i + 1
		for j < end && colors[j] == colors[i] {
			j++
		}
		sanitized = append(sanitized, HeaderSpan{Text: string(runes[i:j]), Color: colors[i]})
		i = j
	}
	return sanitized
}

// CollapseToHeader removes every retained activity block, leaving only the
// pinned header block. The next Render clears the block rows in place, so the
// header stays as the record of the finished activity.
func (v *ActivityViewport) CollapseToHeader() {
	if v == nil {
		return
	}
	v.blocks = nil
	v.activeReasoning = nil
	v.activeText = nil
	v.rewrap()
}

// AppendReasoning adds a reasoning block. The warm colour applies only to its
// header; its indented body has the same neutral presentation as tool output.
// An open assistant-prose block is closed first so the reasoning starts a new
// block.
func (v *ActivityViewport) AppendReasoning(content string) {
	if v == nil {
		return
	}
	v.FinishText()
	v.appendCoalescing(&v.activeReasoning, blockReasoning, content)
}

// AppendText adds an assistant-prose block to the same viewport. Streamed
// tokens coalesce into one block until FinishText or RemoveTextBlock closes it.
func (v *ActivityViewport) AppendText(content string) {
	if v == nil {
		return
	}
	v.FinishReasoning()
	v.appendCoalescing(&v.activeText, blockText, content)
}

// appendCoalescing appends streamed content to the open block of the given
// kind, creating it (and evicting the oldest finished block) when needed. The
// open block is always the last retained block.
func (v *ActivityViewport) appendCoalescing(active **activityBlock, kind blockKind, content string) {
	if *active == nil {
		*active = &activityBlock{kind: kind}
		v.blocks = append(v.blocks, *active)
		v.evict()
	}
	(*active).content = boundContent((*active).content + SanitizeTerminalText(content))
	(*active).rows = v.wrapBlock(*active)
	v.rewrap()
}

// FinishReasoning closes the current reasoning block. Its visible rows stay in
// the activity history, while the next reasoning event starts a new block.
func (v *ActivityViewport) FinishReasoning() {
	if v == nil {
		return
	}
	v.activeReasoning = nil
}

// FinishText closes the active assistant-prose block. Its visible rows stay in
// the activity history, while the next prose stream starts a new block.
func (v *ActivityViewport) FinishText() {
	if v == nil {
		return
	}
	v.activeText = nil
}

// RemoveTextBlock removes the active assistant-prose block from the activity
// history and returns the number of rows removed. The final answer is removed
// this way so it prints below the window instead of remaining inside it. The
// active text block is the last retained block, so its rows are the cache tail.
func (v *ActivityViewport) RemoveTextBlock() int {
	if v == nil || v.activeText == nil {
		return 0
	}
	removed := min(max(len(v.rows)-len(v.headerRows), 0), len(v.activeText.rows))
	for i, block := range v.blocks {
		if block == v.activeText {
			v.blocks = append(v.blocks[:i], v.blocks[i+1:]...)
			break
		}
	}
	v.activeText = nil
	v.rewrap()
	return removed
}

// TextBlockActive reports whether an assistant-prose block is currently open.
func (v *ActivityViewport) TextBlockActive() bool {
	return v != nil && v.activeText != nil
}

// AppendTool adds a compacted tool block to the same viewport. An open
// assistant-prose or reasoning block is closed first so the tool starts a new
// block.
func (v *ActivityViewport) AppendTool(call pub_models.Call, content string, maxRows int) {
	if v == nil {
		return
	}
	v.FinishText()
	v.FinishReasoning()
	block := &activityBlock{
		kind:         blockTool,
		content:      boundContent(SanitizeTerminalText(content)),
		call:         call,
		toolBodyRows: maxRows,
	}
	v.blocks = append(v.blocks, block)
	block.rows = v.wrapBlock(block)
	v.evict()
	v.rewrap()
}

// AppendMcpLogBlock adds one compacted server-log block to the viewport. The
// block carries the same body budget as a tool block; lines that match the
// MCP log error heuristic are styled as errors inside the block.
func (v *ActivityViewport) AppendMcpLogBlock(server, content string, maxRows int) {
	if v == nil {
		return
	}
	v.FinishText()
	v.FinishReasoning()
	block := &activityBlock{
		kind:         blockMcpLog,
		content:      boundContent(SanitizeTerminalText(content)),
		server:       server,
		toolBodyRows: maxRows,
	}
	v.blocks = append(v.blocks, block)
	block.rows = v.wrapBlock(block)
	v.evict()
	v.rewrap()
}

// evict drops the oldest finished blocks once the retained history exceeds
// maxActivityBlocks. The open coalescing blocks always survive; finished
// blocks keep their logical content until this policy evicts them.
func (v *ActivityViewport) evict() {
	if len(v.blocks) <= maxActivityBlocks {
		return
	}
	excess := len(v.blocks) - maxActivityBlocks
	kept := make([]*activityBlock, 0, len(v.blocks)-excess)
	dropped := 0
	for _, block := range v.blocks {
		if dropped < excess && block != v.activeReasoning && block != v.activeText {
			dropped++
			continue
		}
		kept = append(kept, block)
	}
	v.blocks = kept
}

// wrapBlock derives the display rows of one block at the current width,
// re-applying the block's display policy. The "keep header and trailing rows,
// drop middle" policy is width-stable: the same policy runs at every width,
// even though the visible rows change because wrapping changes.
func (v *ActivityViewport) wrapBlock(block *activityBlock) []activityRow {
	switch block.kind {
	case blockReasoning:
		rows := []activityRow{{content: "∴ thinking", color: RoleColor("reasoning")}}
		for _, row := range terminalRows(block.content, max(v.width-2, 1)) {
			rows = append(rows, activityRow{content: "  " + row})
		}
		return compactActivityBlock(rows, v.height)
	case blockText:
		rows := []activityRow{{content: "assistant", color: RoleColor("assistant")}}
		for _, row := range terminalRows(block.content, max(v.width-2, 1)) {
			rows = append(rows, activityRow{content: "  " + row})
		}
		return compactActivityBlock(rows, v.height)
	case blockTool:
		rows := []activityRow{{content: truncateTerminalRow(toolActivityHeader(block.call), v.width), color: TableTheme().Primary}}
		for _, row := range compactTerminalRows(block.content, max(v.width-2, 1), block.toolBodyRows) {
			color := ""
			if isToolOutputMarker(row) {
				color = TableTheme().Secondary
			}
			if strings.HasPrefix(row, "ERROR:") {
				row = truncateTerminalRow("✗ "+row, max(v.width-2, 1))
				color = RoleColor("tool")
			}
			rows = append(rows, activityRow{content: "  " + row, color: color})
		}
		return append(rows, activityRow{})
	case blockMcpLog:
		rows := []activityRow{{content: truncateTerminalRow(mcpLogBlockHeader(block.server), v.width), color: TableTheme().Secondary}}
		rows = append(rows, mcpLogBodyRows(block.content, max(v.width-2, 1), block.toolBodyRows)...)
		return append(rows, activityRow{})
	}
	return nil
}

// compactActivityBlock keeps a wrapped block's header and trailing rows and
// drops its middle once it exceeds the row budget.
func compactActivityBlock(rows []activityRow, budget int) []activityRow {
	if budget <= 1 {
		if len(rows) > 1 {
			return rows[:1]
		}
		return rows
	}
	if len(rows) <= budget {
		return rows
	}
	compacted := make([]activityRow, 0, budget)
	compacted = append(compacted, rows[0])
	compacted = append(compacted, rows[len(rows)-(budget-1):]...)
	return compacted
}

// boundContent caps a block's retained content at maxActivityBlockRunes,
// keeping the head and tail so a later rewrap still has the content the
// display policy would show.
func boundContent(content string) string {
	if utf8.RuneCountInString(content) <= maxActivityBlockRunes {
		return content
	}
	runes := []rune(content)
	markerLen := utf8.RuneCountInString(contentTruncationMarker)
	head := (maxActivityBlockRunes - markerLen) / 2
	tail := maxActivityBlockRunes - markerLen - head
	return string(runes[:head]) + contentTruncationMarker + string(runes[len(runes)-tail:])
}

// rewrap rebuilds the visible row cache from the retained blocks. The pinned
// header block always survives; only the body is trimmed to the effective
// height. Earlier body history stays in the blocks for a later height-grow
// reappearance.
func (v *ActivityViewport) rewrap() {
	v.headerRows = v.wrapHeader()
	rows := make([]activityRow, 0, v.height)
	rows = append(rows, v.headerRows...)
	bodyHeight := max(v.height-len(v.headerRows), 0)
	body := make([]activityRow, 0, bodyHeight)
	for _, block := range v.blocks {
		body = append(body, block.rows...)
	}
	if len(body) > bodyHeight {
		body = body[len(body)-bodyHeight:]
	}
	v.rows = append(rows, body...)
}

// wrapHeader wraps every header line at the current width and caps the block.
// The cap reserves one row for activity when the window is taller than one
// row. A clipped block ends with an ellipsis so the cut is visible.
func (v *ActivityViewport) wrapHeader() []activityRow {
	budget := min(maxActivityHeaderRows, max(v.height-1, 1))
	rows := make([]activityRow, 0, budget)
	for _, line := range v.header {
		rows = append(rows, v.wrapHeaderLine(line)...)
	}
	if len(rows) > budget {
		rows = rows[:budget]
		rows[budget-1] = clipActivityRow(rows[budget-1], v.width, "…")
	}
	return rows
}

// wrapHeaderLine wraps one header line at the window width, keeping the colour
// of every span across the wrap. The spans of one line share the wrap, so a row
// can carry the end of one span and the start of the next.
func (v *ActivityViewport) wrapHeaderLine(line HeaderLine) []activityRow {
	var rows []activityRow
	var row []activitySpan
	rowWidth := 0
	flush := func() {
		if len(row) == 0 {
			return
		}
		rows = append(rows, rowFromSpans(row))
		row = nil
		rowWidth = 0
	}
	for _, span := range line.Spans {
		for _, r := range span.Text {
			cellWidth := terminalRuneWidth(r)
			if cellWidth > v.width {
				r = '…'
				cellWidth = 1
			}
			if rowWidth > 0 && rowWidth+cellWidth > v.width {
				flush()
			}
			row = appendSpan(row, string(r), span.Color)
			rowWidth += cellWidth
		}
	}
	flush()
	return rows
}

// clipActivityRow truncates row to the width, ending it with marker. Every
// surviving span keeps its colour; the marker takes the colour of the run it
// cuts.
func clipActivityRow(row activityRow, width int, marker string) activityRow {
	var clipped []activitySpan
	used := 0
	markerColor := row.color
	cut := false
	for _, span := range row.coloredSpans() {
		var kept strings.Builder
		for _, r := range span.text {
			cellWidth := terminalRuneWidth(r)
			if used+cellWidth+terminalStringWidth(marker) > width {
				cut = true
				break
			}
			kept.WriteRune(r)
			used += cellWidth
		}
		markerColor = span.color
		clipped = appendSpan(clipped, kept.String(), span.color)
		if cut {
			break
		}
	}
	clipped = appendSpan(clipped, marker, markerColor)
	return rowFromSpans(clipped)
}

// Content returns the sanitized content of the open reasoning block, or ""
// when no reasoning block is open.
func (v *ActivityViewport) Content() string {
	if v == nil || v.activeReasoning == nil {
		return ""
	}
	return v.activeReasoning.content
}

// Rows returns the physical terminal rows currently visible in the viewport.
func (v *ActivityViewport) Rows() []string {
	if v == nil {
		return nil
	}
	rows := make([]string, 0, len(v.rows))
	for _, row := range v.rows {
		rows = append(rows, row.content)
	}
	return rows
}

// Resize applies new terminal dimensions to the viewport. It mutates state
// only: it stores the new width, computes the effective height as
// min(configured cap, terminal height), rewraps all retained blocks at the new
// width, invalidates the render bookkeeping, and marks the viewport dirty. It
// never writes to the writer and is a no-op when the supplied dimensions equal
// the current ones. Resize is invoked only from the serialized session loop,
// never from a signal callback; mutation and rendering stay on that loop, so
// no mutex is needed.
func (v *ActivityViewport) Resize(width, terminalHeight int) {
	if v == nil {
		return
	}
	width = max(width, 1)
	height := min(v.maxRows, terminalFrameHeight(terminalHeight))
	if width == v.width && height == v.height {
		return
	}
	v.width = width
	v.height = height
	for _, block := range v.blocks {
		block.rows = v.wrapBlock(block)
	}
	v.rewrap()
	v.dirty = true
}

// Render redraws the viewport in its existing terminal region. A viewport
// that holds only the pinned header block is not drawn: the header belongs to
// live activity, and drawing it alone would leave a stray copy in scrollback
// when unrelated output moves the cursor before the first body frame.
// RenderPinned draws that frame explicitly. A dirty viewport (after
// Resize or a partial write) emits one complete atomic frame: move up the
// previously drawn rows, clear down, and rewrite the full window. The diff
// path is used only for normal streaming appends: pure appends print below the
// previously drawn region without clearing, and a changed tail clears from the
// first changed row down. The whole update lands in one write, so the terminal
// never paints an intermediate blank frame. The cursor is left below the
// viewport so subsequent tool activity or answer text follows it. A partial or
// failed write leaves the viewport dirty; the next Render retries the full
// frame.
func (v *ActivityViewport) Render(w io.Writer) error {
	return v.render(w, false)
}

// RenderPinned draws the viewport even when only the pinned header block
// remains. FinishLoopIteration uses it when an iteration ends without activity:
// the header is then the iteration's progress record. MCP error elevation uses
// it to restore the window below the elevated block.
func (v *ActivityViewport) RenderPinned(w io.Writer) error {
	return v.render(w, true)
}

// PrintAbove writes line into the scrollback above the drawn window: the frame
// is erased, the line printed where the frame started, and the frame redrawn
// below it. A viewport that has drawn nothing prints the line at the cursor.
// The frame's cursor bookkeeping survives, so the next clear still lands on the
// frame's first row. A direct write to the terminal would move the cursor
// behind that bookkeeping and strand a row on the next clear.
func (v *ActivityViewport) PrintAbove(w io.Writer, line string) error {
	if v == nil {
		return nil
	}
	text := SanitizeTerminalRow(line)
	if text == "" {
		return nil
	}
	if w == nil {
		w = os.Stdout
	}
	var buf bytes.Buffer
	drawn := v.drawnRows > 0
	if drawn {
		fmt.Fprintf(&buf, "\x1b[%dA\r\x1b[J", v.drawnRows)
	}
	fmt.Fprintln(&buf, truncateTerminalRow(text, v.width))
	var rendered []string
	if drawn {
		rendered = v.renderedRows()
		for _, row := range rendered {
			fmt.Fprintln(&buf, row)
		}
	}
	if err := writeFrame(w, buf.Bytes()); err != nil {
		v.dirty = true
		return fmt.Errorf("print above activity viewport: %w", err)
	}
	if drawn {
		v.markDrawn(rendered)
	}
	return nil
}

// renderedRows returns the frame's rows with their colours applied.
func (v *ActivityViewport) renderedRows() []string {
	rendered := make([]string, 0, len(v.rows))
	for _, row := range v.rows {
		rendered = append(rendered, row.render())
	}
	return rendered
}

// markDrawn records frame as the region now on screen.
func (v *ActivityViewport) markDrawn(rendered []string) {
	v.drawnRows = len(rendered)
	v.lastRendered = rendered
	v.dirty = false
}

// writeFrame writes one frame in a single call and reports a short write as an
// error, so the caller can retry with a complete frame.
func writeFrame(w io.Writer, frame []byte) error {
	n, err := w.Write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return fmt.Errorf("short write (%d of %d bytes)", n, len(frame))
	}
	return nil
}

func (v *ActivityViewport) render(w io.Writer, drawHeaderOnly bool) error {
	if v == nil {
		return nil
	}
	if !drawHeaderOnly && v.drawnRows == 0 && len(v.rows) == len(v.headerRows) {
		return nil
	}
	if w == nil {
		w = os.Stdout
	}
	rendered := v.renderedRows()
	var buf bytes.Buffer
	if v.dirty {
		if v.drawnRows > 0 {
			fmt.Fprintf(&buf, "\x1b[%dA\r\x1b[J", v.drawnRows)
		}
		for _, row := range rendered {
			fmt.Fprintln(&buf, row)
		}
	} else {
		start := 0
		clearUp := 0
		if v.drawnRows > 0 {
			common := commonRowPrefix(v.lastRendered, rendered)
			switch {
			case common == len(v.lastRendered) && common == len(rendered):
				return nil // content unchanged, nothing to redraw
			case common == len(v.lastRendered):
				start = len(v.lastRendered) // pure append, no clear needed
			default:
				start = common
				clearUp = len(v.lastRendered) - common
			}
		}
		if clearUp > 0 {
			fmt.Fprintf(&buf, "\x1b[%dA\r\x1b[J", clearUp)
		}
		for _, row := range rendered[start:] {
			fmt.Fprintln(&buf, row)
		}
	}
	if buf.Len() == 0 {
		if v.dirty {
			// The empty frame is complete: nothing was drawn before, nothing
			// needs clearing, and the viewport is clean again.
			v.markDrawn(rendered)
		}
		return nil
	}
	if err := writeFrame(w, buf.Bytes()); err != nil {
		v.dirty = true
		return fmt.Errorf("write activity viewport: %w", err)
	}
	v.markDrawn(rendered)
	return nil
}

// commonRowPrefix returns the number of leading rows shared by two renderings.
func commonRowPrefix(a, b []string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// DetachRenderedRegion transfers ownership of the rows already drawn to the
// caller. The retained activity history will render at the current cursor
// position on the next Render call.
func (v *ActivityViewport) DetachRenderedRegion() int {
	if v == nil {
		return 0
	}
	drawnRows := v.drawnRows
	v.drawnRows = 0
	v.lastRendered = nil
	v.dirty = false
	return drawnRows
}

func isToolError(content string) bool {
	return strings.Contains(content, "ERROR:")
}

// mcpLogErrorKeywords is the comprehensive severity keyword set for MCP server
// stderr lines. A match marks the line as an error: the line is styled with a
// ✗ marker in the rolling window and is elevated outside the window. The set is
// deliberately broad (warn included) so real server problems are never missed.
var mcpLogErrorKeywords = []string{
	"error", "fatal", "panic", "fail", "exception", "denied",
	"refused", "unable", "cannot", "could not", "warn", "timeout", "unreachable",
}

// mcpLogAuthChallengeKeywords are the high-confidence subset of
// mcpLogAuthKeywords: imperative prompts and named credential failures that
// are not substrings of unrelated text. A bare "401"/"403" is deliberately
// excluded here (mcpLogAuthKeywords only): those numeric fragments collide
// with ordinary text (a port number, a line count) far too easily to
// justify reclassifying a connect failure's error type, which is the one
// use IsMcpLogAuthChallengeLine serves.
var mcpLogAuthChallengeKeywords = []string{
	"please authorize", "please authenticate", "please visit",
	"please sign in", "please log in", "please login",
	"to authenticate", "to authorize",
	"sign in to", "sign in at", "log in to", "log in at", "login required",
	"not authenticated", "not logged in", "unauthenticated",
	"no credentials", "credentials expired", "session expired",
	"missing api key", "invalid api key",
	"token expired", "token has expired",
	"enter the code", "enter code", "one-time code",
	"device code", "verification code",
	"unauthorized", "forbidden",
}

// mcpLogAuthKeywords marks MCP server stderr lines that ask the user to act
// on an auth flow. Auth prompts elevate like errors: a suppressed prompt
// leaves the server waiting on an auth flow the user never sees. The set is
// deliberately imperative ("please authorize", "not logged in") so OAuth
// machinery status chatter ("Discovering OAuth server configuration",
// "Initializing auth coordination") stays quiet. It is broader than
// mcpLogAuthChallengeKeywords (it also matches bare "401"/"403"), which is
// fine for deciding whether to render a pre-emptive prompt window, but too
// loose for reclassifying a connect failure's error type (R1-01).
var mcpLogAuthKeywords = append(append([]string{}, mcpLogAuthChallengeKeywords...), "401", "403")

// mcpLogAuthURLMarkers are URL path fragments that mark a line carrying an
// auth flow URL the user must visit. Matched only on lines containing "://",
// so prose mentioning oauth stays unmatched.
var mcpLogAuthURLMarkers = []string{
	"/auth", "/login", "/oauth", "/device", "/activate",
	"/signin", "/sign-in", "/verify",
}

// IsMcpLogErrorLine reports whether an MCP server stderr line looks like an
// error. The match is case-insensitive substring matching against
// mcpLogErrorKeywords.
func IsMcpLogErrorLine(line string) bool {
	return matchesAnyKeyword(line, mcpLogErrorKeywords)
}

// IsMcpLogAuthLine reports whether an MCP server stderr line asks the user to
// act on an auth flow: an imperative prompt, an auth failure, or a URL whose
// path looks like an auth endpoint.
func IsMcpLogAuthLine(line string) bool {
	if matchesAnyKeyword(line, mcpLogAuthKeywords) {
		return true
	}
	return strings.Contains(line, "://") && matchesAnyKeyword(line, mcpLogAuthURLMarkers)
}

// IsMcpLogAuthChallengeLine reports whether a line is a high-confidence
// authorization prompt, fit for reclassifying a connect failure's error
// type rather than only for opening a pre-emptive prompt window. See
// IsMcpLogAuthLine's doc comment for why a bare "401"/"403" is not enough
// here (R1-01).
func IsMcpLogAuthChallengeLine(line string) bool {
	if matchesAnyKeyword(line, mcpLogAuthChallengeKeywords) {
		return true
	}
	return strings.Contains(line, "://") && matchesAnyKeyword(line, mcpLogAuthURLMarkers)
}

// IsMcpLogAuthPayloadLine reports whether a line following an auth prompt
// carries its actionable payload: any URL, or a short uppercase user code like
// "ABCD-1234". Everything else after a prompt is machinery chatter.
func IsMcpLogAuthPayloadLine(line string) bool {
	if strings.Contains(line, "://") {
		return true
	}
	line = strings.TrimSpace(line)
	if len(line) < 4 || len(line) > 16 {
		return false
	}
	digitOrUpper := false
	for _, r := range line {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			digitOrUpper = true
		case r == '-':
		default:
			return false
		}
	}
	return digitOrUpper
}

func matchesAnyKeyword(line string, keywords []string) bool {
	lower := strings.ToLower(line)
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// mcpLogBodyRows wraps MCP log content into styled body rows: auth
// prompts get the » marker in the user-role color, errors the magenta ✗
// marker, other lines stay plain. Markers are prepended before wrapping and
// rows wrap instead of truncating, so payloads like OAuth URLs keep every
// character. maxRows > 0 compacts the middle rows like compactTerminalRows.
// Auth wins over error so lines like "401 Unauthorized" read as an action
// prompt rather than a failure to ignore.
func mcpLogBodyRows(content string, width, maxRows int) []activityRow {
	var rows []activityRow
	for line := range strings.SplitSeq(content, "\n") {
		marker, color := "", ""
		switch {
		case IsMcpLogAuthLine(line):
			marker, color = "» ", RoleColor("user")
		case IsMcpLogErrorLine(line):
			marker, color = "✗ ", RoleColor("tool")
		}
		for _, row := range cellChunks(marker+line, max(width, 1)) {
			rows = append(rows, activityRow{content: "  " + row, color: color})
		}
	}
	if maxRows <= 0 || len(rows) <= maxRows {
		return rows
	}
	head := maxRows / 2
	tail := maxRows - head - 1
	marker := fmt.Sprintf("  ... [%d terminal rows omitted] ...", len(rows)-head-tail)
	compacted := make([]activityRow, 0, maxRows)
	compacted = append(compacted, rows[:head]...)
	compacted = append(compacted, activityRow{content: marker})
	return append(compacted, rows[len(rows)-tail:]...)
}

// mcpLogBlockHeader renders the rolling-window header for one MCP server log
// block, following the server.tool display convention of tool activity.
func mcpLogBlockHeader(server string) string {
	return "▸ mcp." + SanitizeTerminalRow(server) + " log"
}

// PrintMcpLogHeader prints the rolling-window style block header for one MCP
// server's log lines.
func PrintMcpLogHeader(w io.Writer, server string, termWidth int) error {
	header := truncateTerminalRow(mcpLogBlockHeader(server), max(termWidth, 1))
	if _, err := fmt.Fprintln(w, table.Colorize(TableTheme().Secondary, header)); err != nil {
		return fmt.Errorf("write mcp log block header: %w", err)
	}
	return nil
}

// PrintMcpLogLine prints one styled MCP log line under a block header,
// following the mcpLogBodyRows styling and wrapping rules.
func PrintMcpLogLine(w io.Writer, line string, termWidth int) error {
	for _, row := range mcpLogBodyRows(SanitizeTerminalRow(line), max(termWidth-2, 1), 0) {
		if _, err := fmt.Fprintln(w, table.Colorize(row.color, row.content)); err != nil {
			return fmt.Errorf("write mcp log block line: %w", err)
		}
	}
	return nil
}

// PrintMcpErrorBlock prints one elevated MCP server error block below the
// rolling window. The block is display-only: it never enters the chat history
// or model context.
func PrintMcpErrorBlock(w io.Writer, server string, lines []string, termWidth int) error {
	if w == nil {
		w = os.Stdout
	}
	if err := PrintMcpLogHeader(w, server, termWidth); err != nil {
		return err
	}
	for _, line := range lines {
		if err := PrintMcpLogLine(w, line, termWidth); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// SummarizeAsyncToolResult returns the concise interactive representation of
// async tool output. Raw and redirected output bypass this helper.
func SummarizeAsyncToolResult(name, content string) string {
	if !strings.HasPrefix(name, "async_cmd") {
		return content
	}
	var result map[string]any
	if json.Unmarshal([]byte(content), &result) != nil {
		return content
	}
	if name == "async_cmd_logs" {
		return summarizeAsyncLogs(result)
	}
	return summarizeAsyncStatus(result)
}

func summarizeAsyncStatus(result map[string]any) string {
	var parts []string
	if value := stringField(result, "result"); value != "" {
		parts = append(parts, "result="+value)
	}
	if value := stringField(result, "status"); value != "" {
		parts = append(parts, "status="+value)
	}
	if value := stringField(result, "async_cmd_id"); value != "" {
		parts = append(parts, "id="+value)
	}
	if value, ok := result["exit_code"]; ok && value != nil {
		parts = append(parts, "exit="+fmt.Sprint(value))
	}
	if value := stringField(result, "error"); value != "" {
		parts = append(parts, "error="+value)
	}
	if commands, ok := result["async_cmds"].([]any); ok {
		for _, command := range commands {
			if status, ok := command.(map[string]any); ok {
				parts = append(parts, summarizeAsyncStatus(status))
			}
		}
	}
	return strings.Join(parts, " ")
}

func summarizeAsyncLogs(result map[string]any) string {
	var summary strings.Builder
	summary.WriteString(summarizeAsyncStatus(result))
	for _, streamName := range []string{"stdout", "stderr"} {
		stream, ok := result[streamName].(map[string]any)
		if !ok {
			continue
		}
		preview := stringField(stream, "preview")
		if preview == "" {
			continue
		}
		summary.WriteString("\n" + streamName + ":\n" + preview)
	}
	return summary.String()
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func compactTerminalRows(content string, width, maxRows int) []string {
	rows := terminalRows(content, width)
	if maxRows <= 0 || len(rows) <= maxRows {
		return rows
	}
	head := maxRows / 2
	tail := maxRows - head - 1
	marker := fmt.Sprintf("... [%d terminal rows omitted] ...", len(rows)-head-tail)
	compacted := make([]string, 0, maxRows)
	compacted = append(compacted, rows[:head]...)
	compacted = append(compacted, marker)
	compacted = append(compacted, rows[len(rows)-tail:]...)
	return compacted
}

// terminalRows wraps content into rows of at most width cells, breaking at
// the last space that fits; a word wider than a row breaks by cell.
func terminalRows(content string, width int) []string {
	width = max(width, 1)
	var rows []string
	for line := range strings.SplitSeq(content, "\n") {
		rows = append(rows, wordWrapLine(line, width)...)
	}
	return rows
}

func wordWrapLine(line string, width int) []string {
	var (
		rows  []string
		row   strings.Builder
		used  int
		fresh bool
	)
	for i, word := range strings.Split(line, " ") {
		wordWidth := terminalStringWidth(word)
		sep := i > 0 && !fresh
		need := wordWidth
		if sep {
			need++
		}
		if used > 0 && used+need > width {
			rows = append(rows, strings.TrimRight(row.String(), " "))
			row.Reset()
			used, sep, fresh = 0, false, true
		}
		if fresh && word == "" {
			continue
		}
		fresh = false
		if sep {
			row.WriteByte(' ')
			used++
		}
		if used+wordWidth <= width {
			row.WriteString(word)
			used += wordWidth
			continue
		}
		chunks := cellChunks(word, width)
		for _, chunk := range chunks[:len(chunks)-1] {
			rows = append(rows, chunk)
		}
		row.Reset()
		row.WriteString(chunks[len(chunks)-1])
		used = terminalStringWidth(chunks[len(chunks)-1])
	}
	return append(rows, row.String())
}

// cellChunks breaks s into rows of at most width cells, so joining the rows
// gives s back; a rune wider than a row becomes an ellipsis.
func cellChunks(word string, width int) []string {
	var (
		chunks []string
		row    strings.Builder
		used   int
	)
	for _, r := range word {
		cellWidth := terminalRuneWidth(r)
		if cellWidth > width {
			r = '…'
			cellWidth = 1
		}
		if wraps(used, cellWidth, width) {
			chunks = append(chunks, row.String())
			row.Reset()
			used = 0
		}
		row.WriteRune(r)
		used += cellWidth
	}
	return append(chunks, row.String())
}

func isToolOutputMarker(row string) bool {
	return strings.HasPrefix(row, "... [") && strings.HasSuffix(row, " terminal rows omitted] ...")
}

func truncateTerminalRow(content string, width int) string {
	width = max(width, 1)
	if terminalStringWidth(content) <= width {
		return content
	}
	if width == 1 {
		return "…"
	}
	var truncated strings.Builder
	used := 0
	for _, r := range content {
		runeWidth := terminalRuneWidth(r)
		if used+runeWidth > width-1 {
			break
		}
		truncated.WriteRune(r)
		used += runeWidth
	}
	truncated.WriteRune('…')
	return truncated.String()
}

func terminalStringWidth(content string) int {
	cellWidth := 0
	for _, r := range content {
		cellWidth += terminalRuneWidth(r)
	}
	return cellWidth
}

func terminalRuneWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianFullwidth, width.EastAsianWide:
		return 2
	}
	if r >= 0x1f000 && r <= 0x1faff {
		return 2
	}
	return 1
}

func PrepareDisplayMessage(msg pub_models.Message) pub_models.Message {
	display := msg
	if display.Role == "tool" {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
		return display
	}
	if display.Role == "assistant" && display.ReasoningContent == "" {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
	}
	return display
}

// PinWideRunes returns token, streamed after prior on rows width cells wide,
// so that terminals which draw a wide rune one cell wide still break rows
// where TerminalRows counts them: each wide rune is followed by a move to the
// column it ends on, and a rune that starts a new row after a full one is
// preceded by an explicit row break. Text holding escape sequences passes
// through unchanged.
func PinWideRunes(prior, token string, width int) string {
	if width <= 0 || strings.ContainsRune(token, '\x1b') {
		return token
	}
	last := prior
	if i := strings.LastIndexByte(prior, '\n'); i >= 0 {
		last = prior[i+1:]
	}
	if strings.ContainsRune(last, '\x1b') {
		return token
	}
	// filledByWide marks a row a wide rune filled: a terminal that drew it
	// narrow has no pending wrap, so the next rune needs an explicit break.
	col, filledByWide := 0, false
	for _, r := range last {
		col = advanceColumn(col, r, width)
		filledByWide = col == width && terminalRuneWidth(r) == 2
	}
	var b strings.Builder
	for _, r := range token {
		wide := terminalRuneWidth(r) == 2
		printable := r != '\t' && !unicode.IsControl(r)
		if printable && (filledByWide || (wide && col > 0 && col+2 > width)) {
			b.WriteString("\r\n")
			col = 0
		}
		b.WriteRune(r)
		col = advanceColumn(col, r, width)
		filledByWide = col == width && wide
		if wide && col < width {
			fmt.Fprintf(&b, "\x1b[%dG", col+1)
		}
	}
	return b.String()
}

// advanceColumn is the column after r is written at col, as lineRows counts
// it; width means the row is full.
func advanceColumn(col int, r rune, width int) int {
	switch {
	case r == '\n' || r == '\r':
		return 0
	case r == '\t':
		return min((col/tabStop+1)*tabStop, width-1)
	case unicode.IsControl(r):
		return col
	}
	w := terminalRuneWidth(r)
	if wraps(col, w, width) {
		col = 0
	}
	return col + w
}
