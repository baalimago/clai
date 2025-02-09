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
