package glob

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Setup the glob parsing. Currently this is a bit messy as it works
// both for flag glob and arg glob. Once arg glob is deprecated, this
// function may be cleaned up
func Setup(flagGlob string, args []string) (string, []string, error) {
	globArg := args[0] == "g" || args[0] == "glob"
	if globArg && len(args) < 2 {
		return "", args, fmt.Errorf("not enough arguments provided")
	}
	glob := args[1]
	if globArg {
		if flagGlob != "" {
			ancli.PrintWarn(fmt.Sprintf("both glob-arg and glob-flag is specified. This is confusing. Using glob-arg query: %v\n", glob))
		}
		args = args[1:]
	} else {
		glob = flagGlob
	}
	if !strings.Contains(glob, "*") {
		ancli.PrintWarn(fmt.Sprintf("found no '*' in glob: %v, has it already been expanded? Consider enclosing glob in single quotes\n", glob))
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found glob: %v\n", glob))
	}
	return glob, args, nil
}

func CreateChat(glob, systemPrompt string) (models.Chat, error) {
	fileMessages, err := parseGlob(glob)
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to parse glob string: '%v', err: %w", glob, err)
	}

	return models.Chat{
		ID:       fmt.Sprintf("glob_%v", filepath.Base(glob)),
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
	glob, err := utils.ReplaceTildeWithHome(glob)
	if err != nil {
		return nil, fmt.Errorf("parseGlob, ReplaceTildeWithHome: %w", err)
	}
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
