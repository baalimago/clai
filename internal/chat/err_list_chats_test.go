package chat

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// Test_ChatHandler_ListChats_TypedMatch pins clai's own consumer conversion
// (phase 8): the chat handler distinguishes a real chat-listing failure from
// "not found" via errors.Is on the package-internal errListChats sentinel,
// never by matching the message text (worklog
// 2026-09-05-error-propagation, phase 8).
func Test_ChatHandler_ListChats_TypedMatch(t *testing.T) {
	// A missing conversations dir makes the index read yield no rows, which
	// routes findChatByID into the list fallback; the fallback then fails on
	// the same missing dir, producing the errListChats-wrapped error.
	missingConvDir := filepath.Join(t.TempDir(), "missing-conversations")
	cq := &ChatHandler{
		subCmd:  "continue",
		prompt:  "1",
		convDir: missingConvDir,
		out:     io.Discard,
	}

	err := cq.Query(context.Background())
	if err == nil {
		t.Fatal("expected the listing failure to propagate, got nil")
	}
	if !errors.Is(err, errListChats) {
		t.Errorf("err = %v, want errors.Is(err, errListChats)", err)
	}
	// The human-readable wording is preserved; only the matching mechanism
	// changed.
	if !strings.Contains(err.Error(), "failed to list chats") {
		t.Errorf("err = %v, want the message to keep naming the listing failure", err)
	}
}

// Test_findChatByID_ListFailureCarriesSentinel pins the producing side: the
// sentinel is wrapped into the error the handler returns, so the consumer
// can match it without message parsing.
func Test_findChatByID_ListFailureCarriesSentinel(t *testing.T) {
	missingConvDir := filepath.Join(t.TempDir(), "missing-conversations")
	cq := &ChatHandler{convDir: missingConvDir, out: io.Discard}

	_, err := cq.findChatByID("1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, errListChats) {
		t.Errorf("err = %v, want errors.Is(err, errListChats)", err)
	}
}
