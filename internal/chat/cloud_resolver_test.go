package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cloudsync "github.com/baalimago/clai/internal/cloud/sync"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type fakePuller struct {
	pull  cloudsync.ConversationPull
	err   error
	calls []string
}

func (f *fakePuller) PullConversation(_ context.Context, id string) (cloudsync.ConversationPull, error) {
	f.calls = append(f.calls, id)
	return f.pull, f.err
}

func TestCloudNotice(t *testing.T) {
	trash := filepath.Join("/home/u/.config/clai", "cloud", "trash", "conversations", "X.json.20261026T120000Z")
	for _, tc := range []struct {
		name string
		pull cloudsync.ConversationPull
		want string
	}{
		{"adopted", cloudsync.ConversationPull{Outcome: cloudsync.ConversationAdopted, ID: "X"}, "cc:X: taken from clai.chat"},
		{"replaced", cloudsync.ConversationPull{Outcome: cloudsync.ConversationReplaced, ID: "X", TrashPath: trash}, "cc:X: replaced a shorter local copy (kept in cloud/trash/conversations/X.json.20261026T120000Z)"},
		{"local forked", cloudsync.ConversationPull{Outcome: cloudsync.ConversationLocalForked, ID: "X", ForkID: "L"}, "cc:X: taken from clai.chat; the local copy is now L"},
		{"unchanged", cloudsync.ConversationPull{Outcome: cloudsync.ConversationUnchanged, ID: "X", TrashPath: trash, ForkID: "L"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cloudNotice("X", tc.pull); got != tc.want {
				t.Fatalf("cloudNotice = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCloudResolver_pulls(t *testing.T) {
	for _, outcome := range []cloudsync.ConversationOutcome{
		cloudsync.ConversationUnchanged, cloudsync.ConversationAdopted, cloudsync.ConversationReplaced, cloudsync.ConversationLocalForked,
	} {
		t.Run("", func(t *testing.T) {
			cq, confDir := newTestHandler(t)
			x := pub_models.Chat{ID: "X", Messages: []pub_models.Message{{Role: "user", Content: "cloud"}}}
			l := pub_models.Chat{ID: "L", Messages: []pub_models.Message{{Role: "user", Content: "local"}}}
			saveAll(t, cq.convDir, x, l)
			pull := cloudsync.ConversationPull{Outcome: outcome, ID: "X", ForkID: "L"}
			p := &fakePuller{pull: pull}
			got, err := NewCloudResolver(p, confDir).ResolveCloud(context.Background(), "X")
			if err != nil {
				t.Fatalf("ResolveCloud: %v", err)
			}
			if got.Chat.ID != "X" || len(got.Chat.Messages) != 1 || got.Chat.Messages[0].Content != "cloud" {
				t.Fatalf("chat = %+v", got.Chat)
			}
			if got.Notice != cloudNotice("X", pull) {
				t.Fatalf("notice = %q", got.Notice)
			}
			if len(p.calls) != 1 || p.calls[0] != "X" {
				t.Fatalf("calls = %v", p.calls)
			}
		})
	}
}

func TestCloudResolver_missing(t *testing.T) {
	_, confDir := newTestHandler(t)
	notFound := &cloudsync.ConversationNotFoundError{ID: "X"}
	_, err := NewCloudResolver(&fakePuller{err: notFound}, confDir).ResolveCloud(context.Background(), "X")
	var missing *CloudConversationMissingError
	if !errors.As(err, &missing) || missing.ID != "X" {
		t.Fatalf("err = %v", err)
	}
	want := "clai.chat has no conversation X; 'clai chat continue X' continues the local one"
	if err.Error() != want {
		t.Fatalf("text = %q, want %q", err.Error(), want)
	}
	var cause *cloudsync.ConversationNotFoundError
	if !errors.As(err, &cause) || cause != notFound {
		t.Fatalf("cause not reachable: %v", err)
	}

	transport := errors.New("offline")
	_, err = NewCloudResolver(&fakePuller{err: transport}, confDir).ResolveCloud(context.Background(), "X")
	if err != transport {
		t.Fatalf("err = %v, want the transport error unchanged", err)
	}
}

func TestCloudResolver_pulledWithError(t *testing.T) {
	trash := filepath.Join("/home/u/.config/clai", "cloud", "trash", "conversations", "X.json.20261026T120000Z")
	for _, tc := range []struct {
		name       string
		pull       cloudsync.ConversationPull
		wantNotice string
	}{
		{"adopted", cloudsync.ConversationPull{Outcome: cloudsync.ConversationAdopted, ID: "X"}, "cc:X: taken from clai.chat"},
		{"local forked", cloudsync.ConversationPull{Outcome: cloudsync.ConversationLocalForked, ID: "X", ForkID: "L"}, "cc:X: taken from clai.chat; the local copy is now L"},
		{"replaced", cloudsync.ConversationPull{Outcome: cloudsync.ConversationReplaced, ID: "X", TrashPath: trash}, "cc:X: replaced a shorter local copy (kept in cloud/trash/conversations/X.json.20261026T120000Z)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, confDir := newTestHandler(t)
			saveAll(t, cq.convDir, pub_models.Chat{ID: "X"})
			hookErr := &os.PathError{Op: "hook", Path: "index", Err: errors.New("index hook failed")}
			p := &fakePuller{pull: tc.pull, err: hookErr}
			got, err := NewCloudResolver(p, confDir).ResolveCloud(context.Background(), "X")
			if err == nil || !strings.Contains(err.Error(), tc.wantNotice) || !strings.Contains(err.Error(), "index hook failed") {
				t.Fatalf("err = %v, want the notice %q and the cause", err, tc.wantNotice)
			}
			var pe *os.PathError
			if !errors.Is(err, hookErr) || !errors.As(err, &pe) {
				t.Fatalf("err chain lost the cause: %v", err)
			}
			if got.Chat.ID != "" || got.Notice != "" {
				t.Fatalf("resolution = %+v, want zero", got)
			}
		})
	}
}

func TestCloudResolver_pulledWithErrorUnchanged(t *testing.T) {
	_, confDir := newTestHandler(t)
	cause := errors.New("boom")
	p := &fakePuller{pull: cloudsync.ConversationPull{Outcome: cloudsync.ConversationUnchanged, ID: "X"}, err: cause}
	_, err := NewCloudResolver(p, confDir).ResolveCloud(context.Background(), "X")
	if err != cause {
		t.Fatalf("err = %v, want the pull error unchanged", err)
	}
}

func TestCloudResolver_loadFailure(t *testing.T) {
	_, confDir := newTestHandler(t)
	p := &fakePuller{pull: cloudsync.ConversationPull{Outcome: cloudsync.ConversationAdopted, ID: "X"}}
	if _, err := NewCloudResolver(p, confDir).ResolveCloud(context.Background(), "X"); err == nil {
		t.Fatal("want an error when the pulled chat cannot be loaded")
	}
}
