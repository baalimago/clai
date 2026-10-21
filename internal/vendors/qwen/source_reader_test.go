package qwen

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
)

func writeQwenSession(t *testing.T, root, id string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", "proj", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func qwenParent(parent string) string {
	if parent == "" {
		return "null"
	}
	b, _ := json.Marshal(parent)
	return string(b)
}

func qwenUserLine(uuid, parent, text string) string {
	return `{"uuid":` + quote(uuid) + `,"parentUuid":` + qwenParent(parent) + `,"sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","type":"user","cwd":"/work/proj","message":{"role":"user","parts":[{"text":` + quote(text) + `}]}}`
}

func qwenAssistantLine(uuid, parent, parts string) string {
	return `{"uuid":` + quote(uuid) + `,"parentUuid":` + qwenParent(parent) + `,"sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","type":"assistant","message":{"role":"assistant","parts":` + parts + `},"model":"qwen3"}`
}

func qwenToolResultLine(uuid, parent, callID, output string) string {
	return `{"uuid":` + quote(uuid) + `,"parentUuid":` + qwenParent(parent) + `,"sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","type":"tool_result","message":{"role":"user","parts":[{"functionResponse":{"id":` + quote(callID) + `,"name":"read","response":{"output":` + quote(output) + `}}}]}}`
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func readQwenTestConversation(t *testing.T, path string) qwenConversation {
	t.Helper()
	conv, ok, err := (SourceReader{}).readConversation(path)
	if err != nil || !ok {
		t.Fatalf("readConversation = %v, %v", ok, err)
	}
	return conv
}

func TestQwenSourceReader_Source(t *testing.T) {
	if got := (SourceReader{}).Source(); got != "qwen" {
		t.Fatalf("Source() = %q, want qwen", got)
	}
}

func TestQwenSourceReader_DiscoverAndRead(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "hello"),
		qwenAssistantLine("a1", "u1", `[{"text":"hi"}]`),
	)
	r := SourceReader{Root: root}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Source != "qwen" || row.SourceID != "s1" {
		t.Fatalf("identity = %q/%q", row.Source, row.SourceID)
	}
	if row.MessageCount != 2 {
		t.Fatalf("MessageCount = %d", row.MessageCount)
	}
	if row.FirstUserMessage != "hello" || row.FullFirstUserMessage != "hello" {
		t.Fatalf("preview = %q / %q", row.FirstUserMessage, row.FullFirstUserMessage)
	}
	if row.Cwd != "/work/proj" || row.Model != "qwen3" {
		t.Fatalf("cwd/model = %q/%q", row.Cwd, row.Model)
	}
	if !row.Created.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("Created = %v", row.Created)
	}

	chat, err := r.Read(context.Background(), nil, "s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != row.MessageCount+1 {
		t.Fatalf("messages = %d, want %d", len(chat.Messages), row.MessageCount+1)
	}
	if chat.Messages[0].Role != "system" || !strings.Contains(chat.Messages[0].Content, "s1") {
		t.Fatalf("preamble = %#v", chat.Messages[0])
	}
	if chat.Messages[1].Content != "hello" || chat.Messages[2].Content != "hi" {
		t.Fatalf("messages = %#v", chat.Messages)
	}
}

func TestQwenSourceReader_ChainNotFileOrder(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "root prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"root answer"}]`),
		qwenUserLine("u2", "a1", "abandoned"),
		qwenAssistantLine("a2", "u2", `[{"text":"abandoned answer"}]`),
		qwenUserLine("u3", "a1", "kept"),
		qwenAssistantLine("a3", "u3", `[{"text":"kept answer"}]`),
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %#v", len(msgs), msgs)
	}
	if msgs[2].Content != "kept" {
		t.Fatalf("leaf = %#v", msgs[2])
	}
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "abandoned") {
			t.Fatalf("abandoned branch leaked: %#v", msgs)
		}
	}
}

func TestQwenSourceReader_Fragments(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"part one"}]`),
		qwenAssistantLine("a1", "u1", `[{"text":"part two"}]`),
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(msgs), msgs)
	}
	if msgs[1].Content != "part one\npart two" {
		t.Fatalf("fragment = %q", msgs[1].Content)
	}
}

func TestQwenSourceReader_SidechainExcluded(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "main prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"main answer"}]`),
		`{"uuid":"s1","parentUuid":"a1","sessionId":"s1","timestamp":"2026-01-02T03:04:06Z","type":"user","isSidechain":true,"message":{"role":"user","parts":[{"text":"subagent prompt"}]}}`,
		`{"uuid":"s2","parentUuid":"s1","sessionId":"s1","timestamp":"2026-01-02T03:04:07Z","type":"assistant","isSidechain":true,"message":{"role":"assistant","parts":[{"text":"subagent answer"}]}}`,
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(msgs), msgs)
	}
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "subagent") {
			t.Fatalf("sidechain leaked: %#v", msgs)
		}
	}
}

func TestQwenSourceReader_OnlySidechain(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1",
		`{"uuid":"s1","parentUuid":null,"sessionId":"s1","type":"user","isSidechain":true,"message":{"role":"user","parts":[{"text":"child"}]}}`,
	)
	r := SourceReader{Root: root}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
	if _, err := r.Read(context.Background(), nil, "s1"); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("Read = %v", err)
	}
}

func TestQwenSourceReader_SystemEventsAndTitle(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "first prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"first answer"}]`),
		`{"uuid":"c1","parentUuid":"a1","sessionId":"s1","type":"system","subtype":"chat_compression","systemPayload":{"compressedHistory":[]}}`,
		qwenUserLine("u2", "c1", "second prompt"),
		qwenAssistantLine("a2", "u2", `[{"text":"second answer"}]`),
		`{"uuid":"t1","parentUuid":"a2","sessionId":"s1","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"My title"}}`,
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %#v", len(msgs), msgs)
	}
	if msgs[0].Content != "first prompt" || msgs[2].Content != "second prompt" {
		t.Fatalf("compression dropped history: %#v", msgs)
	}
	row := conv.sourceRow(path, nil)
	if row.FirstUserMessage != "My title" {
		t.Fatalf("preview = %q", row.FirstUserMessage)
	}
	if row.FullFirstUserMessage != "first prompt" {
		t.Fatalf("full = %q", row.FullFirstUserMessage)
	}
}

func TestQwenSourceReader_UserDisplayText(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		`{"uuid":"u1","parentUuid":null,"sessionId":"s1","type":"user","systemPayload":{"displayText":"visible","hookContext":"ctx"},"message":{"role":"user","parts":[{"text":"model-facing"},{"text":"<qwen:user-prompt-submit-context>ctx</qwen:user-prompt-submit-context>"}]}}`,
		`{"uuid":"u2","parentUuid":"u1","sessionId":"s1","type":"user","message":{"role":"user","parts":[{"text":"real"},{"text":"<qwen:user-prompt-submit-context>ctx</qwen:user-prompt-submit-context>"}]}}`,
		`{"uuid":"u3","parentUuid":"u2","sessionId":"s1","type":"user","subtype":"agent_mention","message":{"role":"user","parts":[{"text":"mention"}]}}`,
		`{"uuid":"u4","parentUuid":"u3","sessionId":"s1","type":"user","subtype":"agent_message","message":{"role":"user","parts":[{"text":"agent message"}]}}`,
		`{"uuid":"a1","parentUuid":"u4","sessionId":"s1","type":"assistant","message":{"role":"assistant","parts":[{"text":"answer"}]}}`,
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(msgs), msgs)
	}
	if msgs[0].Content != "visible" {
		t.Fatalf("displayText = %q", msgs[0].Content)
	}
	if msgs[1].Content != "real" {
		t.Fatalf("context stripped = %q", msgs[1].Content)
	}
}

func TestQwenSourceReader_ToolPairing(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "do it"),
		qwenAssistantLine("a1", "u1", `[{"text":"plan","thought":true},{"text":"working"},{"functionCall":{"id":"c1","name":"read","args":{"path":"a"}}}]`),
		qwenToolResultLine("r1", "a1", "c1", "done"),
		qwenAssistantLine("a2", "r1", `[{"functionCall":{"id":"c2","name":"write","args":{}}}]`),
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %#v", len(msgs), msgs)
	}
	if msgs[1].Content != "working" || msgs[1].ReasoningContent != "plan" {
		t.Fatalf("assistant = %#v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("call = %#v", msgs[1].ToolCalls)
	}
	if msgs[2].Role != "tool" || msgs[2].Content != "done" {
		t.Fatalf("tool = %#v", msgs[2])
	}
	if msgs[3].Content != `[tool call: write({})]` || len(msgs[3].ToolCalls) != 0 {
		t.Fatalf("unpaired = %#v", msgs[3])
	}
}

func TestQwenSourceReader_ToolResultFallback(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "do it"),
		qwenAssistantLine("a1", "u1", `[{"functionCall":{"id":"c1","name":"read","args":{}}}]`),
		`{"uuid":"r1","parentUuid":"a1","sessionId":"s1","type":"tool_result","toolCallResult":{"callId":"c1","status":"success","resultDisplay":"display only"},"message":{"role":"user","parts":[]}}`,
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 3 || msgs[2].Role != "tool" || msgs[2].Content != "display only" {
		t.Fatalf("messages = %#v", msgs)
	}
}

func TestQwenSourceReader_ReasoningOnlyAssistant(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"deep thought","thought":true}]`),
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(msgs), msgs)
	}
	if msgs[1].Content != "[thinking] deep thought" || msgs[1].ReasoningContent != "deep thought" {
		t.Fatalf("reasoning-only assistant = %#v", msgs[1])
	}
}

func TestQwenSourceReader_TrailingRootSystemRecord(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"answer"}]`),
		`{"uuid":"tail","parentUuid":null,"sessionId":"s1","type":"system","subtype":"turn_result","systemPayload":{}}`,
	)
	conv := readQwenTestConversation(t, path)
	msgs := conv.mapped()
	if len(msgs) != 2 || msgs[1].Content != "answer" {
		t.Fatalf("messages = %#v, want the chain before the trailing root", msgs)
	}
	if row := conv.sourceRow(path, nil); row.SourceID != "s1" || row.MessageCount != 2 {
		t.Fatalf("row = %#v", row)
	}
}

func TestQwenSourceReader_SubagentDirSkipped(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "active prompt"),
		qwenAssistantLine("a1", "u1", `[{"text":"active answer"}]`),
	)
	subagents := filepath.Join(root, "projects", "proj", "subagents", "s1")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	child := `{"uuid":"c1","parentUuid":null,"sessionId":"child","type":"user","message":{"role":"user","parts":[{"text":"child prompt"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(subagents, "agent-x.jsonl"), []byte(child), 0o644); err != nil {
		t.Fatalf("write subagent: %v", err)
	}
	rows, err := (SourceReader{Root: root}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "s1" {
		t.Fatalf("rows = %#v, want only the active session", rows)
	}
}

func TestQwenSourceReader_MissingParentAndCycle(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		`{"uuid":"u1","parentUuid":"missing","sessionId":"s1","type":"user","message":{"role":"user","parts":[{"text":"one"}]}}`,
		qwenAssistantLine("a1", "u1", `[{"text":"two"}]`),
	)
	conv := readQwenTestConversation(t, path)
	if len(conv.records) != 2 {
		t.Fatalf("missing parent chain = %d, want 2", len(conv.records))
	}

	cycleRoot := t.TempDir()
	cyclePath := writeQwenSession(t, cycleRoot, "s1",
		`{"uuid":"c1","parentUuid":"c2","sessionId":"s1","type":"user","message":{"role":"user","parts":[{"text":"one"}]}}`,
		`{"uuid":"c2","parentUuid":"c1","sessionId":"s1","type":"assistant","message":{"role":"assistant","parts":[{"text":"two"}]}}`,
	)
	cycle := readQwenTestConversation(t, cyclePath)
	if len(cycle.records) != 2 {
		t.Fatalf("cycle chain = %d, want 2", len(cycle.records))
	}
}

func TestQwenSourceReader_MixedSessionIDs(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "one"),
		`{"uuid":"u2","parentUuid":"u1","sessionId":"other","type":"user","message":{"role":"user","parts":[{"text":"two"}]}}`,
	)
	if _, _, err := (SourceReader{}).readConversation(path); err == nil || !strings.Contains(err.Error(), "mixed session ids") {
		t.Fatalf("error = %v", err)
	}
	rows, err := (SourceReader{Root: root}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
}

func TestQwenSourceReader_Junk(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1", `{not json`, `{"uuid":`)
	rows, err := (SourceReader{Root: root}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
}

func TestQwenSourceReader_Errors(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1", `{"uuid":"c1","parentUuid":null,"sessionId":"s1","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"title"}}`)
	r := SourceReader{Root: root}
	if _, err := r.Read(context.Background(), nil, ""); err == nil {
		t.Fatal("empty id returned nil")
	}
	if _, err := r.Read(context.Background(), nil, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing = %v", err)
	}
	if _, err := r.Read(context.Background(), nil, "s1"); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("no messages = %v", err)
	}
	missing := SourceReader{Root: filepath.Join(root, "nope")}
	rows, err := missing.Discover(context.Background(), nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing root = %#v, %v", rows, err)
	}
}

func TestQwenSourceReader_RootPrecedence(t *testing.T) {
	runtimeDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("QWEN_RUNTIME_DIR", runtimeDir)
	t.Setenv("QWEN_HOME", homeDir)
	writeQwenSession(t, runtimeDir, "runtime-session", qwenUserLine("u1", "", "runtime"), qwenAssistantLine("a1", "u1", `[{"text":"answer"}]`))
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 || filepath.Base(rows[0].RawPath) != "runtime-session.jsonl" {
		t.Fatalf("runtime rows = %#v, %v", rows, err)
	}

	t.Setenv("QWEN_RUNTIME_DIR", "")
	writeQwenSession(t, homeDir, "home-session", qwenUserLine("u1", "", "home"), qwenAssistantLine("a1", "u1", `[{"text":"answer"}]`))
	rows, err = (SourceReader{}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 || filepath.Base(rows[0].RawPath) != "home-session.jsonl" {
		t.Fatalf("home rows = %#v, %v", rows, err)
	}

	t.Setenv("QWEN_HOME", "")
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	writeQwenSession(t, filepath.Join(userHome, ".qwen"), "dot-session", qwenUserLine("u1", "", "dot"), qwenAssistantLine("a1", "u1", `[{"text":"answer"}]`))
	rows, err = (SourceReader{}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 || filepath.Base(rows[0].RawPath) != "dot-session.jsonl" {
		t.Fatalf("dot rows = %#v, %v", rows, err)
	}
}

func TestQwenSourceReader_Cache(t *testing.T) {
	root := t.TempDir()
	session := writeQwenSession(t, root, "s1",
		qwenUserLine("u1", "", "hello"),
		qwenAssistantLine("a1", "u1", `[{"text":"hi"}]`),
	)
	cache := newQwenTestCache()
	fs1, counts1 := jsonltest.CountingFS("/")
	if rows, err := (SourceReader{Root: root, FS: fs1}).Discover(context.Background(), cache); err != nil || len(rows) != 1 {
		t.Fatalf("first Discover = %#v, %v", rows, err)
	}
	if counts1.TotalOpens() == 0 {
		t.Fatal("first Discover opened nothing")
	}
	fs2, counts2 := jsonltest.CountingFS("/")
	if rows, err := (SourceReader{Root: root, FS: fs2}).Discover(context.Background(), cache); err != nil || len(rows) != 1 {
		t.Fatalf("second Discover = %#v, %v", rows, err)
	}
	if got := counts2.Opens(strings.TrimPrefix(session, "/")); got != 0 {
		t.Fatalf("cache hit opened the session %d times", got)
	}
	if counts2.TotalOpens() != 0 {
		t.Fatalf("cache hit opened %d files", counts2.TotalOpens())
	}
	if chat, err := (SourceReader{Root: root, FS: fs2}).Read(context.Background(), cache, "s1"); err != nil || len(chat.Messages) != 3 {
		t.Fatalf("Read through cache = %#v, %v", chat, err)
	}
}

func TestQwenSourceReader_ZeroRowCached(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1", `{"uuid":"c1","parentUuid":null,"sessionId":"s1","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"title"}}`)
	cache := newQwenTestCache()
	fs1, counts1 := jsonltest.CountingFS("/")
	if rows, err := (SourceReader{Root: root, FS: fs1}).Discover(context.Background(), cache); err != nil || len(rows) != 0 {
		t.Fatalf("first Discover = %#v, %v", rows, err)
	}
	if counts1.TotalOpens() == 0 {
		t.Fatal("first Discover opened nothing")
	}
	fs2, counts2 := jsonltest.CountingFS("/")
	if rows, err := (SourceReader{Root: root, FS: fs2}).Discover(context.Background(), cache); err != nil || len(rows) != 0 {
		t.Fatalf("second Discover = %#v, %v", rows, err)
	}
	if counts2.TotalOpens() != 0 {
		t.Fatalf("zero row was not cached: %d opens", counts2.TotalOpens())
	}
}

func TestQwenSourceReader_CreatedFallsBackToModTime(t *testing.T) {
	root := t.TempDir()
	path := writeQwenSession(t, root, "s1",
		`{"uuid":"u1","parentUuid":null,"sessionId":"s1","type":"user","message":{"role":"user","parts":[{"text":"hello"}]}}`,
		`{"uuid":"a1","parentUuid":"u1","sessionId":"s1","type":"assistant","message":{"role":"assistant","parts":[{"text":"hi"}]}}`,
	)
	mod := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	rows, err := (SourceReader{Root: root}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
	if !rows[0].Created.Equal(mod) {
		t.Fatalf("Created = %v, want %v", rows[0].Created, mod)
	}
}

func TestQwenSourceReader_ContextCanceled(t *testing.T) {
	root := t.TempDir()
	writeQwenSession(t, root, "s1", qwenUserLine("u1", "", "hello"), qwenAssistantLine("a1", "u1", `[{"text":"hi"}]`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (SourceReader{Root: root}).Discover(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover error = %v, want canceled", err)
	}
}

type qwenTestCache struct {
	mu      sync.Mutex
	rows    map[string]vendors.SourceRow
	infos   map[string]fs.FileInfo
	located map[string]string
}

func newQwenTestCache() *qwenTestCache {
	return &qwenTestCache{
		rows:    map[string]vendors.SourceRow{},
		infos:   map[string]fs.FileInfo{},
		located: map[string]string{},
	}
}

func (c *qwenTestCache) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[absPath]
	stored, okInfo := c.infos[absPath]
	if !ok || !okInfo || stored.Size() != info.Size() || !stored.ModTime().Equal(info.ModTime()) {
		return vendors.SourceRow{}, false
	}
	return row, true
}

func (c *qwenTestCache) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[absPath] = row
	c.infos[absPath] = info
	if row.SourceID != "" {
		c.located[row.Source+"\x00"+row.SourceID] = absPath
	}
}

func (c *qwenTestCache) Locate(source, sourceID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, ok := c.located[source+"\x00"+sourceID]
	return path, ok
}
