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
	*lineCount = 0

	for _, segment := range newlineSplit {
		if len(segment) == 0 {
			*lineCount++
			continue
		}

		runeCount := utf8.RuneCountInString(segment)
		fullLines := runeCount / termWidth
		if runeCount%termWidth > 0 {
			fullLines++
		}
		*lineCount += fullLines
	}

	if *lineCount == 0 {
		*lineCount = 1
	}

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
		return fmt.Sprintf("%v\n...[and %v more runes]", out[:maxRunes], amRunes-maxRunes)
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

// PrintToolActivity prints one non-raw tool execution as a header and its result.
// The model transcript remains independent from this display-only representation.
func PrintToolActivity(w io.Writer, call pub_models.Call, content string, termWidth, maxRows int) error {
	if w == nil {
		w = os.Stdout
	}
	header := truncateTerminalRow(toolActivityHeader(call), max(termWidth, 1))
	if _, err := fmt.Fprintln(w, table.Colorize(TableTheme().Primary, header)); err != nil {
		return fmt.Errorf("write tool activity header: %w", err)
	}
	content = SummarizeAsyncToolResult(call.Name, content)
	content = sanitizeTerminalText(content)
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
	_, err := fmt.Fprintln(w)
	return err
}

func toolActivityHeader(call pub_models.Call) string {
	var header strings.Builder
	header.WriteString("▸ ")
	header.WriteString(sanitizeTerminalRow(displayToolName(call.Name)))
	for _, key := range sortedInputKeys(call.Inputs) {
		fmt.Fprintf(&header, "  %s=%s", sanitizeTerminalRow(key), sanitizeTerminalRow(displayInputValue((*call.Inputs)[key])))
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
		return strings.Join(strings.Fields(sanitizeTerminalText(text)), " ")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func sanitizeTerminalText(text string) string {
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
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, text)
	return text
}

// sanitizeTerminalRow makes untrusted text safe for a single terminal row.
func sanitizeTerminalRow(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, sanitizeTerminalText(text)))
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
	blocks []*activityBlock
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

type activityRow struct {
	content string
	color   string
}

// NewActivityViewport creates a viewport for all transient model activity.
// maxRows is the configured rolling-window cap; terminalHeight is the raw
// terminal height at creation. The effective height is
// min(maxRows, max(terminalHeight, 1)) from the start, so the viewport never
// renders taller than the terminal it writes to (R5-01). Resize takes raw
// terminal dimensions and keeps the same min(cap, terminal height) bound on
// later dimension changes.
func NewActivityViewport(width, maxRows, terminalHeight int) *ActivityViewport {
	capRows := max(maxRows, 1)
	return &ActivityViewport{
		width:   max(width, 1),
		maxRows: capRows,
		height:  min(capRows, max(terminalHeight, 1)),
	}
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
	(*active).content = boundContent((*active).content + sanitizeTerminalText(content))
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
	removed := min(len(v.rows), len(v.activeText.rows))
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
		content:      boundContent(sanitizeTerminalText(content)),
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
		content:      boundContent(sanitizeTerminalText(content)),
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

// rewrap rebuilds the visible row cache from the retained blocks. Only the
// trailing effective-height rows are kept; earlier history stays in the blocks
// for a later height-grow reappearance.
func (v *ActivityViewport) rewrap() {
	rows := make([]activityRow, 0, v.height)
	for _, block := range v.blocks {
		rows = append(rows, block.rows...)
	}
	if len(rows) > v.height {
		rows = rows[len(rows)-v.height:]
	}
	v.rows = rows
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
	height := min(v.maxRows, max(terminalHeight, 1))
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

// Render redraws the viewport in its existing terminal region. A dirty
// viewport (after Resize or a partial write) emits one complete atomic frame:
// move up the previously drawn rows, clear down, and rewrite the full window.
// The diff path is used only for normal streaming appends: pure appends print
// below the previously drawn region without clearing, and a changed tail
// clears from the first changed row down. The whole update lands in one write,
// so the terminal never paints an intermediate blank frame. The cursor is left
// below the viewport so subsequent tool activity or answer text follows it. A
// partial or failed write leaves the viewport dirty; the next Render retries
// the full frame.
func (v *ActivityViewport) Render(w io.Writer) error {
	if v == nil {
		return nil
	}
	if w == nil {
		w = os.Stdout
	}
	rendered := make([]string, 0, len(v.rows))
	for _, row := range v.rows {
		rendered = append(rendered, table.Colorize(row.color, row.content))
	}
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
			v.drawnRows = len(rendered)
			v.lastRendered = rendered
			v.dirty = false
		}
		return nil
	}
	n, err := w.Write(buf.Bytes())
	if err != nil {
		v.dirty = true
		return fmt.Errorf("write activity viewport: %w", err)
	}
	if n != buf.Len() {
		v.dirty = true
		return fmt.Errorf("write activity viewport: short write (%d of %d bytes)", n, buf.Len())
	}
	v.drawnRows = len(rendered)
	v.lastRendered = rendered
	v.dirty = false
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

// mcpLogAuthKeywords marks MCP server stderr lines that ask the user to act
// on an auth flow. Auth prompts elevate like errors: a suppressed prompt
// leaves the server waiting on an auth flow the user never sees. The set is
// deliberately imperative ("please authorize", "not logged in") so OAuth
// machinery status chatter ("Discovering OAuth server configuration",
// "Initializing auth coordination") stays quiet.
var mcpLogAuthKeywords = []string{
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
	"401", "403", "unauthorized", "forbidden",
}

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
		for _, row := range terminalRows(marker+line, width) {
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
	return "▸ mcp." + sanitizeTerminalRow(server) + " log"
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
	for _, row := range mcpLogBodyRows(sanitizeTerminalRow(line), max(termWidth-2, 1), 0) {
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

func terminalRows(content string, width int) []string {
	width = max(width, 1)
	var rows []string
	for line := range strings.SplitSeq(content, "\n") {
		if line == "" {
			rows = append(rows, "")
			continue
		}
		var row strings.Builder
		rowWidth := 0
		for _, r := range line {
			cellWidth := terminalRuneWidth(r)
			if cellWidth > width {
				r = '…'
				cellWidth = 1
			}
			if rowWidth > 0 && rowWidth+cellWidth > width {
				rows = append(rows, row.String())
				row.Reset()
				rowWidth = 0
			}
			row.WriteRune(r)
			rowWidth += cellWidth
		}
		rows = append(rows, row.String())
	}
	return rows
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
