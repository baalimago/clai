package vendors

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestMock_StreamCompletions_NoUserMessage(t *testing.T) {
	m := &Mock{}
	ch, err := m.StreamCompletions(context.Background(), pub_models.Chat{})
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	var got []models.CompletionEvent
	for ev := range ch {
		got = append(got, ev)
	}
	if len(got) != 2 {
		t.Fatalf("expected an empty reply plus a stop event, got %#v", got)
	}
	if _, ok := got[0].(string); !ok {
		t.Fatalf("first event is %T, want a plain string", got[0])
	}
	if _, ok := got[1].(models.StopEvent); !ok {
		t.Fatalf("second event is %T, want StopEvent", got[1])
	}
	if usage := m.TokenUsage(); usage == nil || usage.PromptTokens != 1 {
		t.Fatalf("expected the empty-prompt usage floor, got %#v", usage)
	}
}

func TestMockUsageForPrompt_EmptyPromptFloorsAtOne(t *testing.T) {
	usage := mockUsageForPrompt("")
	if usage.PromptTokens != 1 || usage.CompletionTokens != 2 || usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestNextToolCall_NoUserMessage(t *testing.T) {
	if _, _, ok := nextToolCall(pub_models.Chat{}, nil); ok {
		t.Fatal("expected no tool call without a user message")
	}
}

func TestNextToolCall_IgnoresEmptyCallNames(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "tool_search tool_search"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{Name: ""}}},
		{Role: "assistant", ToolCalls: []pub_models.Call{{Name: "search"}}},
	}}
	name, ordinal, ok := nextToolCall(chat, nil)
	if !ok || name != "search" || ordinal != 1 {
		t.Fatalf("got (%q, %d, %v), want (search, 1, true)", name, ordinal, ok)
	}
}

func TestInputsForTool_EnvDriven(t *testing.T) {
	t.Setenv("CLAI_MOCK_SEARCH_DIRECTORY", "/tmp/search-root")
	t.Setenv("CLAI_MOCK_SEARCH_SUBTREE", "true")
	got := inputsForTool("search_conversations", 0)
	if got["directory"] != "/tmp/search-root" || got["subtree"] != true {
		t.Fatalf("search_conversations inputs = %#v", got)
	}

	t.Setenv("CLAI_MOCK_INSPECT_ROLE", "user")
	t.Setenv("CLAI_MOCK_INSPECT_MATCH", "needle")
	got = inputsForTool("inspect_conversation", 0)
	if got["role"] != "user" || got["match"] != "needle" {
		t.Fatalf("inspect_conversation inputs = %#v", got)
	}

	if got := inputsForTool("freetext_command", 0); got["command"] != `printf mocked-cmd` {
		t.Fatalf("freetext_command inputs = %#v", got)
	}

	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_CWD", "/tmp/run-root")
	if got := inputsForTool("async_cmd", 0); got["cwd"] != "/tmp/run-root" {
		t.Fatalf("async_cmd inputs = %#v", got)
	}
}

func TestReadAsyncCmdIDFromEnv(t *testing.T) {
	idFile := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(idFile, []byte("from-file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", "")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_STATUS_IDS_FILE", idFile)
	if got := readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_STATUS"); got != "from-file" {
		t.Fatalf("got %q, want the trimmed file content", got)
	}

	t.Setenv("CLAI_MOCK_ASYNC_CMD_STATUS_IDS_FILE", "")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_STATUS_ASYNC_CMD_ID", "")
	if got := readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_STATUS"); got != "async_cmd_missing" {
		t.Fatalf("got %q, want the missing-id sentinel", got)
	}
}

func TestSplitMockArgs(t *testing.T) {
	if got := splitMockArgs(""); got != nil {
		t.Fatalf("splitMockArgs(\"\") = %#v, want nil", got)
	}
	got := splitMockArgs(`-c "echo hi" plain`)
	if len(got) != 3 || got[0] != "-c" || got[1] != "echo hi" || got[2] != "plain" {
		t.Fatalf("splitMockArgs = %#v", got)
	}
}
