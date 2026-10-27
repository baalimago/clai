package text

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/remote"
)

// DirReplySender sends a prompt to the conversation bound to the working
// directory, through the same command `clai [-r] [-p profile] -dre query`
// runs.
func DirReplySender(deps QueryCommandDeps) func(ctx context.Context, req chat.DirReplyRequest) error {
	return func(ctx context.Context, req chat.DirReplyRequest) error {
		args := []string{"-dre"}
		if req.Raw {
			args = append(args, "-r")
		}
		args = append(args, profileArgs(req.Profile)...)
		args = append(args, "--", req.Prompt)
		c := QueryCommand(deps)
		if err := c.Flagset().Parse(args); err != nil {
			return fmt.Errorf("parse dir reply flags: %w", err)
		}
		if err := c.Setup(ctx); err != nil {
			return fmt.Errorf("set up dir reply: %w", err)
		}
		if err := c.Run(ctx); err != nil {
			return fmt.Errorf("run dir reply: %w", err)
		}
		return nil
	}
}

// profileArgs selects a conversation's stamped profile again: a remote
// target, as a -prp run stamps it, through -prp, a name through -p.
func profileArgs(profile string) []string {
	switch {
	case profile == "":
		return nil
	case remote.IsRemote(profile):
		return []string{"-prp", profile}
	}
	return []string{"-p", profile}
}
