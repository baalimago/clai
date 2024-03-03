package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func (cq *chatModelQuerier) constructGlobMessages(glob string, args []string) ([]SystemMessage, error) {
	if !strings.Contains(glob, "*") {
		return nil, fmt.Errorf("glob must contain a wildcard character, enclose glob in single quotes 'like_this' or double quotes \"like_this\" to prevent shell expansion")
	}
	globMessages, err := parseGlob(glob)
	if err != nil {
		return nil, fmt.Errorf("failed to parse glob: %w", err)
	}
	ret := make([]SystemMessage, 0, len(globMessages)+4)
	ret = append(ret, SystemMessage{
		Role:    "system",
		Content: cq.systemPrompt,
	})
	ret = append(ret, SystemMessage{
		Role:    "system",
		Content: "You will be given a series of messages each containing contents from files, then a message containing this: '#####'. Using the file content as context, perform the request given in the message after the '#####'.",
	})
	ret = append(ret, globMessages...)
	ret = append(ret, SystemMessage{
		Role:    "user",
		Content: "#####",
	})
	ret = append(ret, SystemMessage{
		Role:    "user",
		Content: strings.Join(args, " "),
	})
	return ret, nil
}

func parseGlob(glob string) ([]SystemMessage, error) {
	files, err := filepath.Glob(glob)
	ret := make([]SystemMessage, 0, len(files))
	if err != nil {
		return nil, fmt.Errorf("failed to find files: %w", err)
	}
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("found %d files: %v\n", len(files), files))
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to read file: %v\n", err))
			continue
		}
		ret = append(ret, SystemMessage{
			Role:    "user",
			Content: string(data),
		})
	}
	return ret, nil
}
