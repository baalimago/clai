package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
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

	termWidth, err := table.TermWidth()
	if err != nil {
		return fmt.Errorf("get terminal width for glow: %w", err)
	}

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

// PrintCompactToolActivity prints non-raw tool output without markdown rendering.
// It uses at most maxRows terminal rows after the coloured role prefix.
func PrintCompactToolActivity(w io.Writer, role, content string, termWidth, maxRows int) error {
	if w == nil {
		w = os.Stdout
	}
	prefix := compactRolePrefix(role, termWidth)
	contentWidth := max(termWidth-utf8.RuneCountInString(prefix), 1)
	rows := compactTerminalRows(content, contentWidth, maxRows)
	for i, row := range rows {
		if i > 0 {
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return fmt.Errorf("write tool output newline: %w", err)
			}
		}
		if i == 0 {
			if _, err := fmt.Fprint(w, table.Colorize(RoleColor(role), prefix)); err != nil {
				return fmt.Errorf("write tool output prefix: %w", err)
			}
		}
		isMarker := isToolOutputMarker(row)
		row = truncateTerminalRow(row, contentWidth)
		if isMarker {
			row = table.Colorize(TableTheme().Secondary, row)
		}
		if _, err := fmt.Fprint(w, row); err != nil {
			return fmt.Errorf("write tool output: %w", err)
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// PrintCompactToolCall prints one non-raw tool-call status line without glow.
func PrintCompactToolCall(w io.Writer, role, content string, termWidth int) error {
	if w == nil {
		w = os.Stdout
	}
	prefix := compactRolePrefix(role, termWidth)
	content = truncateTerminalRow(content, max(termWidth-utf8.RuneCountInString(prefix), 1))
	_, err := fmt.Fprintf(w, "%s%s\n", table.Colorize(RoleColor(role), prefix), content)
	return err
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
		runes := []rune(line)
		if len(runes) == 0 {
			rows = append(rows, "")
			continue
		}
		for len(runes) > width {
			rows = append(rows, string(runes[:width]))
			runes = runes[width:]
		}
		rows = append(rows, string(runes))
	}
	return rows
}

func isToolOutputMarker(row string) bool {
	return strings.HasPrefix(row, "... [") && strings.HasSuffix(row, " terminal rows omitted] ...")
}

func truncateTerminalRow(content string, width int) string {
	runes := []rune(content)
	if len(runes) <= width {
		return content
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func compactRolePrefix(role string, width int) string {
	if width <= 1 {
		return ""
	}
	return truncateTerminalRow(role+": ", width-1)
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
