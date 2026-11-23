package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cloudsync "github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/diag"
)

// CloudPullError is a pull that failed after it changed local files; Notice names what it did.
type CloudPullError struct {
	Notice string
	Cause  error
}

func (e *CloudPullError) Error() string { return e.Notice + ": " + e.Cause.Error() }

func (e *CloudPullError) Unwrap() error { return e.Cause }

// CloudConversationMissingError is a conversation the cloud store lacks.
type CloudConversationMissingError struct {
	ID    string
	Cause error
}

func (e *CloudConversationMissingError) Error() string {
	return fmt.Sprintf("clai.chat has no conversation %s; 'clai chat continue %s' continues the local one", e.ID, e.ID)
}

func (e *CloudConversationMissingError) Unwrap() error { return e.Cause }

type cloudResolver struct {
	puller  cloudsync.ConversationPuller
	confDir string
}

// NewCloudResolver is the CloudResolver that pulls; a pull error returns no chat; when the pull wrote files, the error carries its notice.
func NewCloudResolver(p cloudsync.ConversationPuller, confDir string) CloudResolver {
	return &cloudResolver{puller: p, confDir: confDir}
}

func (r *cloudResolver) ResolveCloud(ctx context.Context, id string) (CloudResolution, error) {
	pull, err := r.puller.PullConversation(ctx, id)
	if err != nil {
		if _, ok := errors.AsType[*cloudsync.ConversationNotFoundError](err); ok {
			return CloudResolution{}, &CloudConversationMissingError{ID: id, Cause: err}
		}
		if notice := cloudNotice(id, pull); notice != "" {
			return CloudResolution{}, &CloudPullError{Notice: notice, Cause: err}
		}
		return CloudResolution{}, err
	}
	chat, err := loadConversation(ctx, r.confDir, conversationPathFromDir(conversationsDir(r.confDir), id))
	if err != nil {
		return CloudResolution{}, diag.Errorf("load pulled conversation %s: %w", id, err)
	}
	return CloudResolution{Chat: chat, Notice: cloudNotice(id, pull)}, nil
}

func cloudNotice(id string, p cloudsync.ConversationPull) string {
	switch p.Outcome {
	case cloudsync.ConversationAdopted:
		return fmt.Sprintf("cc:%s: taken from clai.chat", id)
	case cloudsync.ConversationReplaced:
		return fmt.Sprintf("cc:%s: replaced a shorter local copy (kept in cloud/trash/%s)", id, trashRelative(p.TrashPath))
	case cloudsync.ConversationLocalForked:
		return fmt.Sprintf("cc:%s: taken from clai.chat; the local copy is now %s", id, p.ForkID)
	}
	return ""
}

func trashRelative(path string) string {
	const marker = "cloud/trash/"
	slashed := strings.ReplaceAll(path, "\\", "/")
	if i := strings.LastIndex(slashed, marker); i >= 0 {
		return slashed[i+len(marker):]
	}
	return slashed
}
