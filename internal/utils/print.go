package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const MaxShortenedNewlines = 5

// ClearLine writes a carriage return followed by the ANSI "clear to end of line"
// escape, so the next write starts at column 0 on a clean line. Useful for
// single-line progress indicators that may vary in length.
func ClearLine(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprint(w, "\r\x1b[K")
}

// ClearTermTo clears upTo lines upwards, leaving the cursor at column 0
// of the last cleared line. Each line is cleared via ClearLine.
//
// If w is nil, os.Stdout is used.
func ClearTermTo(w io.Writer, upTo int) error {
	if w == nil {
		w = os.Stdout
	}
	// Move cursor up line by line and clear each.
	for upTo > 0 {
		ClearLine(w)
		fmt.Fprintf(w, "\033[%dA", 1)
		upTo--
	}
	ClearLine(w)
	return nil
}

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
//
// If w is nil, os.Stdout is used.
func AttemptPrettyPrint(w io.Writer, chatMessage pub_models.Message, username string, raw bool) error {
	if w == nil {
		w = os.Stdout
	}
	if raw {
		fmt.Fprintln(w, chatMessage.Content)
		return nil
	}

	role := chatMessage.Role
	if chatMessage.Role == "user" {
		role = username
	}

	// Respect NO_COLOR.
	if NoColor() {
		if _, err := fmt.Fprintf(w, "%v: %v\n", role, chatMessage.Content); err != nil {
			return fmt.Errorf("write chat message: %w", err)
		}
		return nil
	}

	roleCol := RoleColor(chatMessage.Role)
	coloredRole := Colorize(roleCol, role)

	cmd := exec.Command("glow", "--version")
	if err := cmd.Run(); err != nil {
		if _, err := fmt.Fprintf(w, "%v: %v\n", coloredRole, chatMessage.Content); err != nil {
			return fmt.Errorf("write chat message (no glow): %w", err)
		}
		return nil
	}

	termWidth, err := TermWidth()
	if err != nil {
		return fmt.Errorf("get terminal width for glow: %w", err)
	}
	glowWidth := max(termWidth-5, 1)

	cmd = exec.Command("glow", "-w", strconv.Itoa(glowWidth))
	inp := chatMessage.Content
	// For some reason glow hides specifically <thikning>. So, replace it to [thinking]
	inp = strings.ReplaceAll(inp, "<thinking>", "[thinking]")
	inp = strings.ReplaceAll(inp, "</thinking>", "[/thinking]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = w
	cmd.Stderr = w
	if _, err := fmt.Fprintf(w, "%v:", coloredRole); err != nil {
		return fmt.Errorf("write role prefix: %w", err)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run glow: %w", err)
	}
	return nil
}

func WidthAppropriateStringTrunc(toShorten, prefix string, padding int) (string, error) {
	return WidthAppropriateStringTruncColored(toShorten, prefix, "", "", padding)
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

func PrepareDisplayMessage(msg pub_models.Message) pub_models.Message {
	display := msg
	if display.Role == "tool" && !strings.Contains(display.Content, "mcp_") {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
		return display
	}
	if display.Role == "assistant" && display.ReasoningContent == "" {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
	}
	return display
}
