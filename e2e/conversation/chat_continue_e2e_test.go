package conversation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// Two conversations started in one directory: the binding names the newer.
// `chat continue <id> <prompt>` sends the prompt to the one the id names, in
// place, with the query flags given after `continue`, and leaves the other
// untouched. The id is no part of the prompt.
func Test_e2e_chat_continue_sends_to_the_named_chat_not_the_bound_one(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	e2etest.ChdirTemp(t)

	if status := harness.Run(strings.Split("-r -cm mock_test q first meeting", " ")); status != 0 {
		t.Fatalf("first query status %d", status)
	}
	if status := harness.Run(strings.Split("-r -cm mock_test q second meeting", " ")); status != 0 {
		t.Fatalf("second query status %d", status)
	}
	bound := readOnlyBinding(t, confDir)
	if len(bound.History) != 2 {
		t.Fatalf("expected two conversations bound in turn, got %+v", bound.History)
	}
	second, first := bound.History[0].ChatID, bound.History[1].ChatID
	convs := conversationFiles(t, confDir)
	secondBefore := e2etest.ReadStringFile(t, filepath.Join(confDir, "conversations", second+".json"))

	args := []string{"chat", "continue", "-r", "-n", "-cm", "mock_test", first, "answer for the first"}
	if status := harness.Run(args); status != 0 {
		t.Fatalf("chat continue status %d", status)
	}

	firstAfter := e2etest.ReadStringFile(t, filepath.Join(confDir, "conversations", first+".json"))
	if !strings.Contains(firstAfter, `"content": "first meeting"`) || !strings.Contains(firstAfter, `"content": "answer for the first"`) {
		t.Fatalf("the first conversation did not get the answer:\n%s", firstAfter)
	}
	if got := e2etest.ReadStringFile(t, filepath.Join(confDir, "conversations", second+".json")); got != secondBefore {
		t.Fatalf("the bound conversation changed:\n%s", got)
	}
	if got := conversationFiles(t, confDir); len(got) != len(convs) {
		t.Fatalf("expected chat continue to continue in place, conversations %d -> %d", len(convs), len(got))
	}
	if after := readOnlyBinding(t, confDir); after.ChatID != first {
		t.Fatalf("binding head = %q, want the continued %q", after.ChatID, first)
	}
}

// `chat continue <id>` with no prompt binds the directory and sends nothing:
// the id is not a prompt.
func Test_e2e_chat_continue_by_id_alone_sends_nothing(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	e2etest.ChdirTemp(t)

	if status := harness.Run(strings.Split("-r -cm mock_test q first meeting", " ")); status != 0 {
		t.Fatalf("seed status %d", status)
	}
	if status := harness.Run(strings.Split("-r -cm mock_test q second meeting", " ")); status != 0 {
		t.Fatalf("second query status %d", status)
	}
	first := readOnlyBinding(t, confDir).History[1].ChatID
	path := filepath.Join(confDir, "conversations", first+".json")
	before := e2etest.ReadStringFile(t, path)
	if status := harness.Run([]string{"-r", "chat", "continue", first}); status != 0 {
		t.Fatalf("chat continue status %d", status)
	}
	if got := e2etest.ReadStringFile(t, path); got != before {
		t.Fatalf("continuing by id alone changed the conversation:\n%s", got)
	}
	if head := readOnlyBinding(t, confDir).ChatID; head != first {
		t.Fatalf("binding head = %q, want %q", head, first)
	}
}
