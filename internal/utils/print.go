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

func WillBeNewLine(line, msg string, termWidth int) bool {
	return utf8.RuneCountInString(line+msg) > termWidth
}

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
	amNewlines := strings.Count(msg, "\n")
	if amNewlines == 0 && WillBeNewLine(*line, msg, termWidth) {
		amNewlines = 1
	}
	if amNewlines > 0 {
		*lineCount += amNewlines
		*line = ""
	} else {
		*line += msg
	}
}

// AttemptPrettyPrint by first checking if the glow command is available, and if so, pretty print the chat message
// if not found, simply print the message as is
func AttemptPrettyPrint(chatMessage models.Message, username string) error {
	role := chatMessage.Role
	color := ancli.BLUE
	switch chatMessage.Role {
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
	cmd.Stdin = bytes.NewBufferString(chatMessage.Content)
	cmd.Stdout = os.Stdout
	fmt.Printf("%v:", ancli.ColoredMessage(color, role))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run glow: %w", err)
	}
	return nil
}
