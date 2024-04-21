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
	msgSplit := strings.Split(*line+msg, "\n")
	amNewlines := 0
	for _, line := range msgSplit {
		amNewlines += countNewLines(line, termWidth)
	}
	if amNewlines == 0 {
		amNewlines = 1
	}
	amNewlines += len(msgSplit) - 1

	*lineCount += amNewlines
	lastToken := msgSplit[len(msgSplit)-1]
	if len(lastToken) > termWidth {
		lastTokenWords := strings.Split(lastToken, " ")
		lastWord := lastTokenWords[len(lastTokenWords)-1]
		if len(lastWord) > termWidth {
			trimmedWord := lastWord[termWidth:]
			*line = trimmedWord
		} else {
			*line = lastWord
		}
	} else {
		*line = lastToken
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
	// Glow does not like < and >, it treats them (perhaps rightfully so) as
	// xml-like elements, so we replace them with [ and ]
	inp = strings.ReplaceAll(inp, "<", "[")
	inp = strings.ReplaceAll(inp, ">", "]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = os.Stdout
	fmt.Printf("%v:", ancli.ColoredMessage(color, role))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run glow: %w", err)
	}
	return nil
}
