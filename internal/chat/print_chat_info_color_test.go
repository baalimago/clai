package chat

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestPrintChatInfoColorized(t *testing.T) {
	// Ensure NO_COLOR is unset so Colorize emits sequences in test.
	_ = os.Unsetenv("NO_COLOR")

	var b bytes.Buffer
	ch := pub_models.Chat{
		ID:      "testid",
		Created: time.Now(),
		Messages: []pub_models.Message{
			{Role: "user", Content: "u"},
			{Role: "tool", Content: "t"},
			{Role: "system", Content: "s"},
			{Role: "assistant", Content: "a"},
		},
	}
	cq := &ChatHandler{out: &b}

	if err := cq.printChatInfo(&b, ch); err != nil {
		t.Fatalf("printChatInfo: %v", err)
	}

	out := b.String()
	// Header should be colorized with primary color prefix
	if !strings.Contains(out, utils.ThemePrimaryColor()) {
		t.Fatalf("expected primary color code in output: got %q", out)
	}

	// Role colors should appear
	if !strings.Contains(out, utils.RoleColor("user")) {
		t.Fatalf("expected user role color in output: got %q", out)
	}
	if !strings.Contains(out, utils.RoleColor("tool")) {
		t.Fatalf("expected tool role color in output: got %q", out)
	}
	if !strings.Contains(out, utils.RoleColor("system")) {
		t.Fatalf("expected system role color in output: got %q", out)
	}
}
