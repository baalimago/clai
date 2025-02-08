package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func ClearTermTo(termWidth, upTo int) {
	clearLine := strings.Repeat(" ", termWidth)
	// Move cursor up line by line and clear the line
	for upTo > 0 {
		fmt.Printf("\r%v", clearLine)
		fmt.Printf("\033[%dA", 1)
		upTo--
	}
	fmt.Printf("\r%v", clearLine)
	// Place cursor at start of line
	fmt.Printf("\r")
}

func countNewLines(msg string, termWidth int) int {
	amRunes := utf8.RuneCountInString(msg)
	amLines := int(amRunes / termWidth)
	return amLines
}

// UpdateMessageTerminalMetadata updates the terminal metadata. Meaning the lineCount, to eventually
// clear the terminal
func UpdateMessageTerminalMetadata(msg string, line *string, lineCount *int, termWidth int) {
	newlineSplit := strings.Split(*line+msg, "\n")
	amNewlines := 0
	for _, line := range newlineSplit {
		amNewlines += countNewLines(line, termWidth)
	}
	if amNewlines == 1 {
		amNewlines = 2
	}

	amNewlineChars := len(newlineSplit)
	if amNewlineChars == 1 {
		amNewlineChars = 0
	}

	*lineCount += amNewlines + amNewlineChars
	if *lineCount == 0 {
		*lineCount = 1
	}
	lastNewline := newlineSplit[len(newlineSplit)-1]
	if len(lastNewline) > termWidth {
		lastTokenWords := strings.Split(lastNewline, " ")
		lastWord := lastTokenWords[len(lastTokenWords)-1]
		if len(lastWord) > termWidth {
			trimmedWord := lastWord[termWidth:]
			*line = trimmedWord
		} else {
			*line = lastWord
		}
	} else {
		*line = lastNewline
	}
}

// AttemptPrettyPrint by first checking if the glow command is available, and if so, pretty print the chat message
// if not found, simply print the message as is
func AttemptPrettyPrint(chatMessage models.Message, username string, raw bool) error {
	if raw {
		fmt.Println(chatMessage.Content)
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
		fmt.Printf("%v: %v\n", ancli.ColoredMessage(color, role), chatMessage.Content)
		return nil
	}

	cmd = exec.Command("glow")
	inp := chatMessage.Content
	// For some reason glow hides specifically <thikning>. So, replace it to [thinking]
	inp = strings.ReplaceAll(inp, "<thinking>", "[thinking]")
	inp = strings.ReplaceAll(inp, "</thinking>", "[/thinking]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = os.Stdout
	fmt.Printf("%v:", ancli.ColoredMessage(color, role))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run glow: %w", err)
	}
	return nil
}
