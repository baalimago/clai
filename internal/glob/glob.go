package glob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/runenv"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Setup the glob parsing. Currently this is a bit messy as it works
// both for flag glob and arg glob. Once arg glob is deprecated, this
// function may be cleaned up
func Setup(ctx context.Context, flagGlob string, args []string) (string, []string, error) {
	globArg := args[0] == "g" || args[0] == "glob"
	if globArg && len(args) < 2 {
		return "", args, fmt.Errorf("not enough arguments provided")
	}
	glob := args[1]
	if globArg {
		if flagGlob != "" {
			runenv.Warnf(ctx, runenv.SubsystemGlob, "both glob-arg and glob-flag is specified. This is confusing. Using glob-arg query: %v\n", glob)
		}
		args = args[1:]
	} else {
		glob = flagGlob
	}
	if !strings.Contains(glob, "*") {
		runenv.Warnf(ctx, runenv.SubsystemGlob, "found no '*' in glob: %v, has it already been expanded? Consider enclosing glob in single quotes\n", glob)
	}
	runenv.Debugf(ctx, runenv.SubsystemGlob, "found glob: %v\n", glob)
	return glob, args, nil
}

func CreateChat(ctx context.Context, glob, systemPrompt string) (pub_models.Chat, error) {
	fileMessages, err := parseGlob(ctx, glob)
	if err != nil {
		return pub_models.Chat{}, diag.Errorf("failed to parse glob string: '%v', err: %w", glob, err)
	}
	return FramedChat(glob, fileMessages), nil
}

// FramedChat is the glob chat for pattern: fileMessages between the glob system
// message and the "#####" user message.
func FramedChat(pattern string, fileMessages []pub_models.Message) pub_models.Chat {
	return pub_models.Chat{
		ID:       fmt.Sprintf("glob_%v", pattern),
		Messages: constructGlobMessages(fileMessages),
	}
}

func constructGlobMessages(globMessages []pub_models.Message) []pub_models.Message {
	ret := make([]pub_models.Message, 0, len(globMessages)+4)
	ret = append(ret, pub_models.Message{
		Role:    "system",
		Content: "You will be given a series of messages each containing contents from files, then a message containing this: '#####'. Using the file content as context, perform the request given in the message after the '#####'.",
	})
	ret = append(ret, globMessages...)
	ret = append(ret, pub_models.Message{
		Role:    "user",
		Content: "#####",
	})
	return ret
}

// ErrNoFiles is the glob path's error for a pattern that matches nothing.
var ErrNoFiles = errors.New("no files found")

// File is one file a glob matched, with its contents.
type File struct {
	Name string
	Data []byte
}

func parseGlob(ctx context.Context, glob string) ([]pub_models.Message, error) {
	files, err := ReadFiles(ctx, glob)
	if err != nil {
		return nil, err
	}
	ret := make([]pub_models.Message, 0, len(files))
	for _, f := range files {
		ret = append(ret, FileMessage(f))
	}
	return ret, nil
}

// ReadFiles returns the files glob matches, in match order. A match that cannot be
// read, such as a directory, is skipped with a warning.
func ReadFiles(ctx context.Context, glob string) ([]File, error) {
	glob, err := utils.ReplaceTildeWithHome(glob)
	if err != nil {
		return nil, diag.Errorf("parseGlob, ReplaceTildeWithHome: %w", err)
	}
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, diag.Errorf("failed to parse glob: %w", err)
	}
	runenv.Debugf(ctx, runenv.SubsystemGlob, "found %d files: %v\n", len(files), files)

	if len(files) == 0 {
		return nil, ErrNoFiles
	}

	ret := make([]File, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			runenv.Warnf(ctx, runenv.SubsystemGlob, "failed to read file: %v\n", err)
			continue
		}
		ret = append(ret, File{Name: file, Data: data})
	}
	return ret, nil
}

// FileMessage is the user message that carries one matched file.
func FileMessage(f File) pub_models.Message {
	return pub_models.Message{
		Role:    "user",
		Content: fmt.Sprintf("{\"fileName\": \"%v\", \"data\": \"%v\"}", f.Name, string(f.Data)),
	}
}
