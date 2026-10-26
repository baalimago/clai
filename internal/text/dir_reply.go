package text

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/chat"
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
		if req.Profile != "" {
			args = append(args, "-p", req.Profile)
		}
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
