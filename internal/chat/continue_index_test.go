package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// readingSourceReader serves one foreign session, readable by its id.
type readingSourceReader struct {
	stubSourceReader
}

func (r readingSourceReader) Read(_ context.Context, _ vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	return pub_models.Chat{
		Source: r.name, SourceID: sourceID,
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "foreign prompt"}, {Role: "assistant", Content: "answer"}},
	}, nil
}

// continueIndexFixture lists, newest first: 0 native "newest", 1 the
// foreign session, 2 native "oldest".
func continueIndexFixture(t *testing.T) *ChatHandler {
	t.Helper()
	cq, confDir := newTestHandler(t)
	cq.out = io.Discard
	convDir := conversationsDir(confDir)
	chdirToTemp(t)
	saveAll(t, convDir,
		pub_models.Chat{ID: "newest", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "newest prompt"}}},
		pub_models.Chat{ID: "oldest", Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "oldest prompt"}}},
	)
	reader := readingSourceReader{stubSourceReader{name: "test-source", rows: []vendors.SourceRow{{
		Source: "test-source", SourceID: "ext-1", FirstUserMessage: "foreign prompt", FullFirstUserMessage: "foreign prompt",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}}}
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{reader}))
	return cq
}

// TestFindChatByID_indexMatchesTheListedRow pins that a number `chat list`
// shows selects the same row under `chat continue`, foreign rows included.
func TestFindChatByID_indexMatchesTheListedRow(t *testing.T) {
	t.Run("native row after a foreign one", func(t *testing.T) {
		cq := continueIndexFixture(t)
		got, err := cq.findChatByID(context.Background(), "2 go on", foreignClone)
		if err != nil {
			t.Fatalf("findChatByID: %v", err)
		}
		if got.ID != "oldest" || cq.prompt != "go on" {
			t.Fatalf("got %q with prompt %q, want oldest with %q", got.ID, cq.prompt, "go on")
		}
	})
	t.Run("foreign row is cloned into a clai chat", func(t *testing.T) {
		cq := continueIndexFixture(t)
		got, err := cq.findChatByID(context.Background(), "1", foreignClone)
		if err != nil {
			t.Fatalf("findChatByID: %v", err)
		}
		if got.Source != "test-source" || got.SourceID != "ext-1" || got.ID == "" {
			t.Fatalf("got %+v, want a clone of the foreign session", got)
		}
		if _, err := cq.getByID(got.ID); err != nil {
			t.Fatalf("clone not saved: %v", err)
		}
	})
	t.Run("out of range names the listed count", func(t *testing.T) {
		cq := continueIndexFixture(t)
		if _, err := cq.findChatByID(context.Background(), "3", foreignClone); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Fatalf("err = %v, want out of range", err)
		}
	})
}

func TestDeleteFromPrompt_refusesAForeignRow(t *testing.T) {
	cq := continueIndexFixture(t)
	cq.prompt = "1"
	if err := cq.deleteFromPrompt(context.Background()); !errors.Is(err, ErrForeignRowSelected) {
		t.Fatalf("err = %v, want ErrForeignRowSelected", err)
	}
}

func TestCont_sendsTheTrailingPrompt(t *testing.T) {
	t.Run("the request carries prompt, profile and raw", func(t *testing.T) {
		cq := continueIndexFixture(t)
		cq.prompt, cq.profile, cq.raw = "2 go on", "work", true
		var got DirReplyRequest
		cq.sendPrompt = func(_ context.Context, req DirReplyRequest) error { got = req; return nil }
		if err := cq.cont(context.Background()); err != nil {
			t.Fatalf("cont: %v", err)
		}
		if got != (DirReplyRequest{Prompt: "go on", Profile: "work", Raw: true}) {
			t.Fatalf("sent %+v", got)
		}
	})
	t.Run("a failed send is returned", func(t *testing.T) {
		cq := continueIndexFixture(t)
		cq.prompt = "0 go on"
		boom := errors.New("boom")
		cq.sendPrompt = func(context.Context, DirReplyRequest) error { return boom }
		if err := cq.cont(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})
	t.Run("no prompt sends nothing", func(t *testing.T) {
		cq := continueIndexFixture(t)
		cq.prompt = "0"
		cq.sendPrompt = func(context.Context, DirReplyRequest) error { t.Fatal("sent without a prompt"); return nil }
		if err := cq.cont(context.Background()); err != nil {
			t.Fatalf("cont: %v", err)
		}
	})
}
