package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// ClearTermTo a certain amount of rows upwards by printing termWidth amount of empty spaces.
//
// If w is nil, os.Stdout is used.
func ClearTermTo(w io.Writer, termWidth, upTo int) error {
	if w == nil {
		w = os.Stdout
	}
	if termWidth == -1 {
		t, err := TermWidth()
		if err != nil {
			return fmt.Errorf("failed to find term width: %w", err)
		}
		termWidth = t
	}
	clearLine := strings.Repeat(" ", termWidth)
	// Move cursor up line by line and clear the line
	for upTo > 0 {
		fmt.Fprintf(w, "\r%v", clearLine)
		fmt.Fprintf(w, "\033[%dA", 1)
		upTo--
	}
	fmt.Fprintf(w, "\r%v", clearLine)
	// Place cursor at start of line
	fmt.Fprintf(w, "\r")
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
	color := ancli.BLUE
	switch chatMessage.Role {
	case "tool":
		color = ancli.MAGENTA
	case "user":
		color = ancli.CYAN
		role = username
	case "system":
		color = ancli.BLUE
	}
	cmd := exec.Command("glow", "--version")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "%v: %v\n", ancli.ColoredMessage(color, role), chatMessage.Content)
		return nil
	}

	cmd = exec.Command("glow")
	inp := chatMessage.Content
	// For some reason glow hides specifically <thikning>. So, replace it to [thinking]
	inp = strings.ReplaceAll(inp, "<thinking>", "[thinking]")
	inp = strings.ReplaceAll(inp, "</thinking>", "[/thinking]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = w
	fmt.Fprintf(w, "%v:", ancli.ColoredMessage(color, role))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run glow: %w", err)
	}
	return nil
}

func WidthAppropriateStringTrunk(toShorten, prefix string, padding int) (string, error) {
	toShorten = strings.ReplaceAll(toShorten, "\n", "\\n")
	toShorten = strings.ReplaceAll(toShorten, "\t", "\\t")
	termWidth, err := TermWidth()
	if err != nil {
		return "", fmt.Errorf("failed to get termWidth: %w", err)
	}

	return fillRemainderOfTermWidth(prefix, toShorten, termWidth, padding), nil
}

func fillRemainderOfTermWidth(prefix, remainder string, termWidth, padding int) string {
	infix := " ... "
	infixLen := utf8.RuneCountInString(infix)
	remainingWidth := termWidth - utf8.RuneCountInString(prefix) - padding
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	widthAdjustedRemainder := ""
	r := []rune(remainder)
	if remainingWidth == 0 {
		widthAdjustedRemainder = ""
	} else if len(r) <= remainingWidth {
		widthAdjustedRemainder = remainder
	} else if remainingWidth <= infixLen {
		widthAdjustedRemainder = string(r[:remainingWidth])
	} else {
		avail := remainingWidth - infixLen
		startLen := avail / 2
		endLen := avail - startLen
		if endLen < 0 {
			endLen = 0
		}
		if startLen < 0 {
			startLen = 0
		}
		if startLen > len(r) {
			startLen = len(r)
		}
		if endLen > len(r)-startLen {
			endLen = len(r) - startLen
		}
		endStart := len(r) - endLen
		if endStart < 0 {
			endStart = 0
		}
		widthAdjustedRemainder = string(r[:startLen]) +
			infix +
			string(r[endStart:])
	}

	return prefix + widthAdjustedRemainder
}
