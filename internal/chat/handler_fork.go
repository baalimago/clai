package chat

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
)

const (
	forkNeedsPrompt = "chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'"
	forkNoSender    = "chat fork: this build cannot send a prompt"
)

// forkParentNotFoundError is a parent token that names no chat.
type forkParentNotFoundError struct {
	token string
	cause error
}

func (e *forkParentNotFoundError) Error() string {
	return "chat fork: could not find chat " + strconv.Quote(e.token)
}
func (e *forkParentNotFoundError) Unwrap() error { return e.cause }

func (cq *ChatHandler) fork(ctx context.Context) error {
	args := strings.TrimSpace(cq.prompt)
	token, rest, _ := strings.Cut(args, " ")
	if strings.TrimSpace(rest) == "" {
		return errors.New(forkNeedsPrompt)
	}
	if cq.sendPrompt == nil {
		return errors.New(forkNoSender)
	}
	parent, err := cq.findChatByRef(ctx, args, foreignClone)
	if err != nil {
		return cq.forkLookupError(token, err)
	}
	prompt := strings.TrimSpace(cq.prompt)
	if prompt == "" {
		return errors.New(forkNeedsPrompt)
	}
	if parent.Profile == "" {
		parent.Profile = cq.profile
	}
	forker := cq.forker
	if forker == nil {
		forker = NewForker(convPersister(cq.convDir), chatid.New, time.Now)
	}
	fork, err := forker.Fork(ctx, parent)
	if err != nil {
		return diag.Errorf("chat fork: %w", err)
	}
	runenv.Noticef(ctx, runenv.SubsystemChat, "fork %s of %s", fork.ID, parent.ID)
	if err := cq.UpdateDirScopeFromCWD(ctx, fork.ID); err != nil {
		return diag.Errorf("failed to update directory-scoped binding: %w", err)
	}
	req := ContinueRequest{ChatID: fork.ID, Prompt: prompt, Profile: parent.Profile, Raw: cq.raw, Flags: slices.Clone(cq.queryFlags)}
	if err := cq.sendPrompt(ctx, req); err != nil {
		return diag.Errorf("failed to send prompt to chat %s: %w", fork.ID, err)
	}
	return nil
}

func (cq *ChatHandler) forkLookupError(token string, err error) error {
	switch {
	case isCloudRefFailure(err):
		return err
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, errIndexOutOfRange):
		return &forkParentNotFoundError{token: token, cause: err}
	}
	return diag.Errorf("chat fork: %w", err)
}
