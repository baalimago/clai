package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/runenv"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const cloudRefPrefix = "cc:"

// ChatRef is a chat argument: a local ID or index, or a cloud reference.
type ChatRef struct {
	Cloud bool   // the argument was cc:<value>
	Value string // the ID for a cloud reference; the whole token otherwise
}

// ParseChatRef splits the first token of a chat argument; "cc:" with no ID is refused by the resolver (chatref.4).
func ParseChatRef(token string) ChatRef {
	if id, ok := strings.CutPrefix(token, cloudRefPrefix); ok {
		return ChatRef{Cloud: true, Value: id}
	}
	return ChatRef{Value: token}
}

// CloudResolver makes this machine's copy of a cloud conversation current and returns it.
type CloudResolver interface {
	ResolveCloud(ctx context.Context, id string) (CloudResolution, error)
}

// CloudResolution is the local chat after resolution and the notice to report, "" for none.
type CloudResolution struct {
	Chat   pub_models.Chat
	Notice string
}

// CloudRefError is a cc: reference that could not be resolved; Error() is the cause's text.
type CloudRefError struct {
	ChatID string
	Cause  error
}

func (e *CloudRefError) Error() string { return e.Cause.Error() }
func (e *CloudRefError) Unwrap() error { return e.Cause }

// ErrCloudRefsUnavailable is a cc: reference with no resolver injected.
var ErrCloudRefsUnavailable = errors.New("clai.chat references are not available in this build")

// refRefusal is a cc: reference the verb refuses before any resolution.
type refRefusal struct {
	verb   string
	reason string
	cause  error
}

func (e *refRefusal) Error() string { return "chat " + e.verb + ": " + e.reason }
func (e *refRefusal) Unwrap() error { return e.cause }

// isCloudRefFailure is true for every error a cc: reference can end in: those never fall back to the list UI.
func isCloudRefFailure(err error) bool {
	var refErr *CloudRefError
	var refusal *refRefusal
	return errors.As(err, &refErr) || errors.As(err, &refusal)
}

func (cq *ChatHandler) verbName() string {
	switch cq.subCmd {
	case "continue", "c":
		return "continue"
	case "fork", "f":
		return "fork"
	case "delete", "d":
		return "delete"
	}
	return cq.subCmd
}

// findChatByRef resolves the chat argument of continue and fork; cc:<id> goes through the injected resolver.
func (cq *ChatHandler) findChatByRef(ctx context.Context, token string, foreign foreignSelection) (pub_models.Chat, error) {
	first, rest, _ := strings.Cut(token, " ")
	ref := ParseChatRef(first)
	if !ref.Cloud {
		return cq.findChatByID(ctx, token, foreign)
	}
	verb := cq.verbName()
	if ref.Value == "" {
		return pub_models.Chat{}, &refRefusal{verb: verb, reason: `"cc:" needs a conversation ID, as in cc:<chatID>`}
	}
	if cq.cloudResolver == nil {
		return pub_models.Chat{}, &refRefusal{verb: verb, reason: ErrCloudRefsUnavailable.Error(), cause: ErrCloudRefsUnavailable}
	}
	resolver, err := cq.cloudResolver(ctx, cq.confDir)
	if err != nil {
		return pub_models.Chat{}, &CloudRefError{ChatID: ref.Value, Cause: err}
	}
	res, err := resolver.ResolveCloud(ctx, ref.Value)
	if err != nil {
		return pub_models.Chat{}, &CloudRefError{ChatID: ref.Value, Cause: err}
	}
	if res.Notice != "" {
		runenv.Noticef(ctx, runenv.SubsystemChat, "%s", res.Notice)
	}
	cq.prompt = rest
	return res.Chat, nil
}

func refuseCloudDelete(token string) error {
	first, _, _ := strings.Cut(token, " ")
	ref := ParseChatRef(first)
	if !ref.Cloud {
		return nil
	}
	return fmt.Errorf("chat delete: cc: references are for continue and fork; delete the local copy with 'clai chat delete %s'", ref.Value)
}
