package glob

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func Setup() (string, error) {
	args := flag.Args()
	if len(args) < 2 {
		return "", fmt.Errorf("not enough arguments provided")
	}
	glob := args[1]
	if !strings.Contains(glob, "*") {
		ancli.PrintWarn(fmt.Sprintf("found no '*' in glob: %v, has it already been expanded? Consider enclosing glob in single quotes\n", glob))
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found glob: %v\n", glob))
	}
	return glob, nil
}

func CreateChat(glob, systemPrompt string) (models.Chat, error) {
	fileMessages, err := parseGlob(glob)
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to parse glob string: '%v', err: %w", glob, err)
	}

	return models.Chat{
		ID:       fmt.Sprintf("glob_%v", glob),
		Messages: constructGlobMessages(fileMessages),
	}, nil
}

func constructGlobMessages(globMessages []models.Message) []models.Message {
	ret := make([]models.Message, 0, len(globMessages)+4)
	ret = append(ret, models.Message{
		Role:    "system",
		Content: "You will be given a series of messages each containing contents from files, then a message containing this: '#####'. Using the file content as context, perform the request given in the message after the '#####'.",
	})
	ret = append(ret, globMessages...)
	ret = append(ret, models.Message{
		Role:    "user",
		Content: "#####",
	})
	return ret
}

func parseGlob(glob string) ([]models.Message, error) {
	home, err := os.UserHomeDir()
	if err != nil && strings.Contains(glob, "~/") { // only fail if glob contains ~/ and home dir is not found
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}
	glob = strings.Replace(glob, "~", home, 1)
	files, err := filepath.Glob(glob)
	ret := make([]models.Message, 0, len(files))
	if err != nil {
		return nil, fmt.Errorf("failed to parse glob: %w", err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found %d files: %v\n", len(files), files))
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to read file: %v\n", err))
			continue
		}
		ret = append(ret, models.Message{
			Role:    "user",
			Content: fmt.Sprintf("{\"fileName\": \"%v\", \"data\": \"%v\"}", file, string(data)),
		})
	}
	return ret, nil
}
