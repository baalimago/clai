package text

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/remote"
)

// ContinueSender sends a prompt to the conversation a request names, in
// place, through the query command. It reads no directory binding, so a run
// that ends in the same directory meanwhile cannot take the prompt.
func ContinueSender(deps QueryCommandDeps) func(ctx context.Context, req chat.ContinueRequest) error {
	return func(ctx context.Context, req chat.ContinueRequest) error {
		if req.ChatID == "" {
			return errors.New("continue a chat: no chat id")
		}
		c := queryCommand(deps, req.ChatID)
		if err := c.Flagset().Parse(continueArgs(req)); err != nil {
			return diag.Errorf("parse continue flags: %w", err)
		}
		if err := c.Setup(ctx); err != nil {
			return diag.Errorf("set up continue: %w", err)
		}
		if err := c.Run(ctx); err != nil {
			return diag.Errorf("run continue: %w", err)
		}
		return nil
	}
}

func continueArgs(req chat.ContinueRequest) []string {
	var args []string
	if req.Raw {
		args = append(args, "-r")
	}
	if !slices.ContainsFunc(req.Flags, func(arg string) bool {
		name, _, _ := strings.Cut(arg, "=")
		return name == "-prp" || name == "-profile-path"
	}) {
		args = append(args, profileArgs(req.Profile)...)
	}
	args = append(args, req.Flags...)
	return append(args, "--", req.Prompt)
}

// profileArgs selects a conversation's stamped profile again: a remote
// target or absolute path through -prp, a local name through -p.
func profileArgs(profile string) []string {
	switch {
	case profile == "":
		return nil
	case remote.IsRemote(profile) || filepath.IsAbs(profile):
		return []string{"-prp", profile}
	}
	return []string{"-p", profile}
}
