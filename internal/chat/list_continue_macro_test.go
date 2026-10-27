package chat

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// macroListFixture lists ten distinct chats on page one and, at index 10 on
// page two, a group of two chats that share their first message.
func macroListFixture(t *testing.T) *ChatHandler {
	t.Helper()
	cq, confDir := newTestHandler(t)
	cq.out = io.Discard
	chdirToTemp(t)
	t.Cleanup(useTestSourceReaders(nil))
	base := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	for i := range 12 {
		msg := fmt.Sprintf("native %d", i)
		if i >= 10 {
			msg = "repeated question"
		}
		saveAll(t, conversationsDir(confDir), pub_models.Chat{
			ID:       fmt.Sprintf("n%02d", i),
			Created:  base.Add(time.Duration(100-i) * time.Minute),
			Messages: []pub_models.Message{{Role: "user", Content: msg}},
		})
	}
	return cq
}

func boundChatID(t *testing.T, cq *ChatHandler) string {
	t.Helper()
	ds, err := cq.LoadDirScopeFromCWD()
	if err != nil {
		t.Fatalf("LoadDirScopeFromCWD: %v", err)
	}
	return ds.ChatID
}

// TestListChats_cContinuesTheSelection pins `chat list <i> c`: c continues
// the selected chat, and on a group row its newest member.
func TestListChats_cContinuesTheSelection(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{name: "native row", input: "3\nc\n", want: "n03"},
		{name: "group row on page two", input: "10\nc\n", want: "n10"},
		{name: "group row after paging", input: "n\n10\nc\n", want: "n10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq := macroListFixture(t)
			cq.input = strings.NewReader(tc.input)
			if err := cq.handleListCmd(context.Background()); err != nil {
				t.Fatalf("handleListCmd: %v", err)
			}
			if got := boundChatID(t, cq); got != tc.want {
				t.Fatalf("bound %q, want %q", got, tc.want)
			}
		})
	}
}
