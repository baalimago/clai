package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
)

func writeCodexFile(t *testing.T, home, rel string, lines ...string) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
	return p
}

const (
	codexSessionMeta = `{"timestamp":"2026-08-19T06:12:01Z","type":"session_meta","payload":{"session_id":"s1","cwd":"/work","timestamp":"2026-08-19T06:08:16Z","originator":"Codex Desktop","source":"vscode","thread_source":"user"}}`
	codexTurnContext = `{"type":"turn_context","payload":{"cwd":"/work","model":"gpt-5.2-codex"}}`
	codexAssistant   = `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}}`
)

func codexUserLine(text string) string {
	return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}`
}

func TestCodexSource_Discover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "2026", "08", "19", "rollout-a.jsonl"),
		codexSessionMeta,
		codexTurnContext,
		codexUserLine("<environment_context>injected</environment_context>"),
		codexUserLine("hello there"),
		codexAssistant,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"/work"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"hello there"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"hi there"}}`,
	)
	writeCodexFile(t, home, filepath.Join(".codex", "archived_sessions", "rollout-b.jsonl"),
		`{"type":"session_meta","payload":{"session_id":"s2","cwd":"/old","timestamp":"2026-08-11T05:27:31Z","thread_source":"user","source":"cli"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5.1"}}`,
		codexUserLine("# AGENTS.md instructions for /old"),
		codexUserLine(":q"),
		codexAssistant,
	)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "2026", "08", "19", "rollout-sub.jsonl"),
		`{"type":"session_meta","payload":{"session_id":"s1","id":"sub-id","cwd":"/work","timestamp":"2026-08-19T06:08:16Z","thread_source":"subagent","parent_thread_id":"s1"}}`,
		codexUserLine("The following is the Codex agent history"),
		`{"type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/work","timestamp":"2026-08-19T06:08:16Z","thread_source":"user"}}`,
		codexUserLine("leaked parent prompt"),
		codexAssistant,
	)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "2026", "08", "19", "rollout-broken.jsonl"),
		`{not json`,
	)

	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want the live and archived user sessions: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row.SourceID == "s1" && row.RawPath == filepath.Join(home, ".codex", "sessions", "2026", "08", "19", "rollout-sub.jsonl") {
			t.Fatalf("subagent file %q claimed the parent session", row.RawPath)
		}
	}
	live := rows[0]
	if live.Source != "codex" {
		t.Fatalf("Source = %q, want codex", live.Source)
	}
	if live.SourceID != "s1" {
		t.Fatalf("SourceID = %q, want s1", live.SourceID)
	}
	if live.FirstUserMessage != "hello there" || live.FullFirstUserMessage != "hello there" {
		t.Fatalf("preview = %q / %q, want hello there", live.FirstUserMessage, live.FullFirstUserMessage)
	}
	if live.MessageCount != 4 {
		t.Fatalf("MessageCount = %d, want 4 (user, assistant, call, output)", live.MessageCount)
	}
	if live.Model != "gpt-5.2-codex" {
		t.Fatalf("Model = %q, want gpt-5.2-codex", live.Model)
	}
	if live.Cwd != "/work" {
		t.Fatalf("Cwd = %q, want /work", live.Cwd)
	}
	if want := time.Date(2026, 8, 19, 6, 8, 16, 0, time.UTC); !live.Created.Equal(want) {
		t.Fatalf("Created = %v, want %v", live.Created, want)
	}
	archived := rows[1]
	if archived.SourceID != "s2" || archived.FirstUserMessage != ":q" {
		t.Fatalf("archived row = %+v, want s2 with preview :q", archived)
	}
}

func TestCodexSource_Read(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "2026", "08", "19", "rollout-a.jsonl"),
		codexSessionMeta,
		codexTurnContext,
		codexUserLine("<recommended_plugins>injected</recommended_plugins>"),
		codexUserLine("first prompt"),
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"plan A"}]}}`,
		codexAssistant,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"/work"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"const x = 1","call_id":"c2"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2","output":[{"type":"input_text","text":"done"}]}}`,
		codexUserLine("second"),
	)

	chat, err := (SourceReader{}).Read(context.Background(), nil, "s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "codex" || chat.SourceID != "s1" || chat.ID != "" {
		t.Fatalf("chat identity = %+v, want source codex id s1 with empty chat ID", chat)
	}
	if want := time.Date(2026, 8, 19, 6, 8, 16, 0, time.UTC); !chat.Created.Equal(want) {
		t.Fatalf("Created = %v, want %v", chat.Created, want)
	}
	if len(chat.Messages) == 0 || chat.Messages[0].Role != "system" {
		t.Fatalf("first message = %+v, want a system preamble", chat.Messages)
	}
	if !strings.Contains(chat.Messages[0].Content, "s1") || !strings.Contains(chat.Messages[0].Content, "/work") {
		t.Fatalf("system preamble = %q, want session id and cwd", chat.Messages[0].Content)
	}

	roles := make([]string, 0, len(chat.Messages)-1)
	for _, m := range chat.Messages[1:] {
		roles = append(roles, m.Role)
	}
	wantRoles := []string{"user", "assistant", "assistant", "tool", "assistant", "tool", "user"}
	if strings.Join(roles, ",") != strings.Join(wantRoles, ",") {
		t.Fatalf("roles = %v, want %v", roles, wantRoles)
	}
	answer := chat.Messages[2]
	if answer.Content != "hi there" || answer.ReasoningContent != "plan A" {
		t.Fatalf("assistant = %+v, want content and reasoning", answer)
	}
	call := chat.Messages[3]
	if len(call.ToolCalls) != 1 || call.ToolCalls[0].ID != "c1" || call.ToolCalls[0].Function.Name != "exec_command" {
		t.Fatalf("function call = %+v, want c1 exec_command", call.ToolCalls)
	}
	if call.ToolCalls[0].Function.Arguments != `{"cmd":"pwd"}` {
		t.Fatalf("arguments = %q, want the JSON object as stored", call.ToolCalls[0].Function.Arguments)
	}
	if chat.Messages[4].ToolCallID != "c1" || chat.Messages[4].Content != "/work" {
		t.Fatalf("function output = %+v, want c1 /work", chat.Messages[4])
	}
	custom := chat.Messages[5]
	if len(custom.ToolCalls) != 1 || custom.ToolCalls[0].ID != "c2" || custom.ToolCalls[0].Function.Name != "exec" {
		t.Fatalf("custom call = %+v, want c2 exec", custom.ToolCalls)
	}
	if custom.ToolCalls[0].Function.Arguments != `"const x = 1"` {
		t.Fatalf("custom arguments = %q, want the input JSON-encoded", custom.ToolCalls[0].Function.Arguments)
	}
	if chat.Messages[6].ToolCallID != "c2" || chat.Messages[6].Content != "done" {
		t.Fatalf("custom output = %+v, want c2 done", chat.Messages[6])
	}
}

func TestCodexSource_ReadNeverReportsAnInjectedPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "a.jsonl"),
		codexSessionMeta,
		codexUserLine("<user_instructions>ignore me</user_instructions>"),
		codexUserLine("<turn_aborted>ignore me too</turn_aborted>"),
		codexUserLine("real prompt"),
	)

	chat, err := (SourceReader{}).Read(context.Background(), nil, "s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, m := range chat.Messages {
		if m.Role == "user" && m.Content != "real prompt" {
			t.Fatalf("injected wrapper leaked into the transcript: %q", m.Content)
		}
	}
}

func TestCodexSource_ReadSubagentIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "sub.jsonl"),
		`{"type":"session_meta","payload":{"session_id":"sub","thread_source":"subagent","parent_thread_id":"parent"}}`,
		codexUserLine("child task"),
	)
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("discovered %d rows for a subagent rollout, want none", len(rows))
	}
	if _, err := (SourceReader{}).Read(context.Background(), nil, "sub"); err == nil {
		t.Fatal("Read accepted a subagent rollout")
	}
}

func TestCodexSource_ReadEnvelopeTimestampAndPlainContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "a.jsonl"),
		`{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"fallback-id"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":"plain answer"}}`,
		`{"type":"response_item","payload":{"type":"function_call","call_id":"c1","arguments":""}}`,
		`{"type":"query_start","payload":{"ignored":true}}`,
	)
	chat, err := (SourceReader{}).Read(context.Background(), nil, "fallback-id")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !chat.Created.Equal(want) {
		t.Fatalf("Created = %v, want the envelope timestamp %v", chat.Created, want)
	}
	if got := chat.Messages[1].Content; got != "plain answer" {
		t.Fatalf("assistant content = %q, want the plain string", got)
	}
	call := chat.Messages[2].ToolCalls
	if len(call) != 1 || call[0].Function.Arguments != "{}" {
		t.Fatalf("empty arguments = %+v, want {}", call)
	}
}

func TestCodexTextShapes(t *testing.T) {
	var parts codexTextParts
	if err := parts.UnmarshalJSON(nil); err != nil || len(parts) != 0 {
		t.Fatalf("empty summary = %v / %v, want nil parts", parts, err)
	}
	if err := parts.UnmarshalJSON([]byte(`"summary"`)); err != nil {
		t.Fatalf("string summary: %v", err)
	}
	if len(parts) != 1 || parts[0].Text != "summary" {
		t.Fatalf("string summary = %+v, want one text part", parts)
	}
	if err := parts.UnmarshalJSON([]byte(`[{"type":"summary_text","text":"a"}]`)); err != nil {
		t.Fatalf("array summary: %v", err)
	}
	if len(parts) != 1 || parts[0].Text != "a" {
		t.Fatalf("array summary = %+v, want one part", parts)
	}
	if err := parts.UnmarshalJSON([]byte(`{"unexpected":true}`)); err == nil {
		t.Fatal("object summary decoded without an error")
	}
	if err := parts.UnmarshalJSON([]byte(`"unterminated`)); err == nil {
		t.Fatal("invalid string summary decoded without an error")
	}

	if got := (codexPayload{ID: "fallback"}).sessionID(); got != "fallback" {
		t.Fatalf("sessionID = %q, want the id fallback", got)
	}
	if got := (codexPayload{SessionID: "primary", ID: "fallback"}).sessionID(); got != "primary" {
		t.Fatalf("sessionID = %q, want the session_id", got)
	}
	if got := codexContentText([]byte(`"plain"`)); got != "plain" {
		t.Fatalf("plain content = %q", got)
	}
	if got := codexContentText([]byte(`[{"type":"input_text","text":"a"},{"type":"output_text","text":"b"}]`)); got != "a\nb" {
		t.Fatalf("block content = %q, want a newline-joined text", got)
	}
	if got := codexContentText([]byte(`{"type":"text"}`)); got != "" {
		t.Fatalf("object content = %q, want empty", got)
	}
	if got := codexTime("not a time"); !got.IsZero() {
		t.Fatalf("codexTime(invalid) = %v, want zero", got)
	}
}

func TestCodexSource_ReasoningDoesNotCrossAUserTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "a.jsonl"),
		codexSessionMeta,
		codexUserLine("first"),
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"old plan"}]}}`,
		codexUserLine("second"),
		codexAssistant,
	)
	chat, err := (SourceReader{}).Read(context.Background(), nil, "s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	last := chat.Messages[len(chat.Messages)-1]
	if last.Content != "hi there" || last.ReasoningContent != "" {
		t.Fatalf("last assistant = %+v, want no reasoning carried across the user turn", last)
	}
}

func TestCodexSource_ReadErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "a.jsonl"), codexSessionMeta, codexUserLine("hi"))

	if _, err := (SourceReader{}).Read(context.Background(), nil, ""); err == nil {
		t.Fatal("Read accepted an empty session id")
	}
	if _, err := (SourceReader{}).Read(context.Background(), nil, "absent"); err == nil {
		t.Fatal("Read found a session no file holds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (SourceReader{}).Read(ctx, nil, "s1"); err == nil {
		t.Fatal("Read ignored a cancelled context")
	}
}

func TestCodexSource_DiscoverMissingRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows without a Codex tree, want none", len(rows))
	}
}

func TestCodexSource_MessageCountMatchesRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCodexFile(t, home, filepath.Join(".codex", "sessions", "a.jsonl"),
		codexSessionMeta,
		codexUserLine("hi"),
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}]}}`,
		codexAssistant,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":""}}`,
	)
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	chat, err := (SourceReader{}).Read(context.Background(), nil, rows[0].SourceID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := len(chat.Messages) - 1; got != rows[0].MessageCount {
		t.Fatalf("Read produced %d non-system messages, discovery counted %d", got, rows[0].MessageCount)
	}
}

func TestCodexSchema_PrefilterConformance(t *testing.T) {
	s := (SourceReader{Root: t.TempDir()}).schema()
	lines := [][]byte{
		[]byte(codexSessionMeta),
		[]byte(codexTurnContext),
		[]byte(codexUserLine("hi")),
		[]byte(codexAssistant),
		[]byte(`{"type":"response_item","payload":{"type":"function_call","name":"exec","arguments":"{}","call_id":"c1"}}`),
		[]byte(`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"x"}}`),
	}
	for i, line := range lines {
		got := s.Fields(line)
		if got == (vendors.LineFields{}) {
			t.Fatalf("line %d contributed nothing; the marker set is stale: %s", i+1, line)
		}
		if !s.MayContribute(line) {
			t.Fatalf("line %d: MayContribute rejected a contributing line: %s", i+1, line)
		}
	}
	for _, nonContributing := range [][]byte{
		[]byte(`{"type":"event_msg","payload":{"type":"token_count"}}`),
		[]byte(`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}]}}`),
	} {
		if s.Fields(nonContributing) != (vendors.LineFields{}) {
			t.Fatalf("non-contributing line changed a row: %s", nonContributing)
		}
	}
	if !s.AcceptFirstLine([]byte(codexSessionMeta)) {
		t.Fatal("AcceptFirstLine rejected a user session")
	}
	if s.AcceptFirstLine([]byte(`{"type":"session_meta","payload":{"session_id":"s1","thread_source":"subagent","parent_thread_id":"p"}}`)) {
		t.Fatal("AcceptFirstLine accepted a subagent session")
	}
	if !s.AcceptFirstLine([]byte(`not json`)) {
		t.Fatal("AcceptFirstLine rejected an unparsable first line instead of leaving it to Fields")
	}
	if !s.AcceptFirstLine([]byte(`{"type":"turn_context","payload":{}}`)) {
		t.Fatal("AcceptFirstLine rejected a file whose first line is not session_meta")
	}
}

func TestCodexSchema_Roots(t *testing.T) {
	root := t.TempDir()
	s := (SourceReader{Root: root}).schema()
	got := s.Roots()
	want := []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Roots = %v, want %v", got, want)
	}
	if liveRoot := s.Root(); liveRoot != filepath.Join(root, "sessions") {
		t.Fatalf("Root() = %q, want the live sessions directory", liveRoot)
	}
	t.Setenv("HOME", "")
	if empty := (SourceReader{}).schema(); empty.Root() != "" {
		t.Fatalf("Root() = %q with no HOME, want empty", empty.Root())
	}
}
