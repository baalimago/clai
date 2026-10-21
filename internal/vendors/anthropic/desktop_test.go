package anthropic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
)

func writeJSONL(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func discoverRow(t *testing.T, rows []vendors.SourceRow, id string) vendors.SourceRow {
	t.Helper()
	for _, r := range rows {
		if r.SourceID == id {
			return r
		}
	}
	t.Fatalf("row %q not found in %+v", id, rows)
	return vendors.SourceRow{}
}

func TestDesktopMetadataEnrichesDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projects := filepath.Join(home, ".claude", "projects", "p1")
	writeJSONL(t, filepath.Join(projects, "s1.jsonl"),
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","cwd":"/jsonl-cwd","message":{"content":"first user msg"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s1","message":{"model":"jsonl-model","content":[{"type":"text","text":"hi"}]}}`+"\n")
	writeJSONL(t, filepath.Join(projects, "s2.jsonl"),
		`{"type":"user","sessionId":"s2","message":{"content":"second user msg"}}`+"\n")

	desktop := t.TempDir()
	if err := os.MkdirAll(filepath.Join(desktop, "acct", "org"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "acct", "org", "local_a.json"), []byte(`{
		"sessionId":"desktop-1",
		"cliSessionId":"s1",
		"title":"Refactor auth\nplease",
		"model":"desktop-model",
		"cwd":"/desktop-cwd",
		"createdAt":1750000000000
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "local_b.json"), []byte(`{
		"sessionId":"desktop-2",
		"cliSessionId":"s2",
		"title":"",
		"model":"desktop-model-2",
		"cwd":"/desktop-cwd-2",
		"createdAt":"2026-02-03T04:05:06Z"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := SourceReader{FS: os.DirFS("/"), DesktopRoot: desktop}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}

	s1 := discoverRow(t, rows, "s1")
	if s1.FirstUserMessage != "Refactor auth please" {
		t.Fatalf("title preview = %q", s1.FirstUserMessage)
	}
	if s1.FullFirstUserMessage != "first user msg" {
		t.Fatalf("full preview must keep the user message, got %q", s1.FullFirstUserMessage)
	}
	if s1.Model != "jsonl-model" {
		t.Fatalf("transcript model must win, got %q", s1.Model)
	}
	if s1.Cwd != "/jsonl-cwd" {
		t.Fatalf("transcript cwd must win, got %q", s1.Cwd)
	}

	s2 := discoverRow(t, rows, "s2")
	if s2.FirstUserMessage != "second user msg" {
		t.Fatalf("empty title must keep the JSONL preview, got %q", s2.FirstUserMessage)
	}
	if s2.Model != "desktop-model-2" {
		t.Fatalf("model fill = %q", s2.Model)
	}
	if s2.Cwd != "/desktop-cwd-2" {
		t.Fatalf("cwd fill = %q", s2.Cwd)
	}

	chat, err := r.Read(context.Background(), nil, "s2")
	if err != nil {
		t.Fatalf("Read(s2): %v", err)
	}
	if want := "Continued from Claude Code session s2 (originally at /desktop-cwd-2)."; chat.Messages[0].Content != want {
		t.Fatalf("Read preamble = %q, want %q", chat.Messages[0].Content, want)
	}
	if want := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC); !chat.Created.Equal(want) {
		t.Fatalf("Read created = %v, want %v", chat.Created, want)
	}
}

func TestEnrichDesktopMetadataFillsCreated(t *testing.T) {
	desktop := t.TempDir()
	if err := os.WriteFile(filepath.Join(desktop, "local_a.json"), []byte(`{"cliSessionId":"s1","title":"t","createdAt":1750000000000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := []vendors.SourceRow{{SourceID: "s1", FirstUserMessage: "preview"}}
	(SourceReader{DesktopRoot: desktop}).enrichDesktopMetadata(rows)
	if want := time.UnixMilli(1750000000000); !rows[0].Created.Equal(want) {
		t.Fatalf("created = %v, want %v", rows[0].Created, want)
	}
}

func TestDesktopMetadataDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	projects := filepath.Join(home, ".claude", "projects", "p1")
	writeJSONL(t, filepath.Join(projects, "s1.jsonl"),
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","message":{"content":"hi"}}`+"\n")

	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	desktop := filepath.Join(cfg, "Claude", "claude-code-sessions")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "local_a.json"), []byte(`{"sessionId":"d","cliSessionId":"s1","title":"From the default root"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := (SourceReader{FS: os.DirFS("/")}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := discoverRow(t, rows, "s1").FirstUserMessage; got != "From the default root" {
		t.Fatalf("preview = %q", got)
	}
}

func TestDesktopMetadataToleratesBadFiles(t *testing.T) {
	desktop := t.TempDir()
	if err := os.WriteFile(filepath.Join(desktop, "local_bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "local_empty.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "other.json"), []byte(`{"cliSessionId":"s1","title":"no"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := desktopSessions(desktop)
	if len(got) != 0 {
		t.Fatalf("want no sessions, got %+v", got)
	}
	if got := desktopSessions(""); len(got) != 0 {
		t.Fatalf("empty root: %+v", got)
	}
}

func TestDesktopTimeParsing(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{`1750000000000`, time.UnixMilli(1750000000000)},
		{`1750000000`, time.Unix(1750000000, 0)},
		{`"2026-02-03T04:05:06Z"`, time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)},
		{`"not a time"`, time.Time{}},
		{`0`, time.Time{}},
	}
	for _, tc := range cases {
		var got desktopMetadata
		if err := json.Unmarshal([]byte(`{"createdAt":`+tc.in+`}`), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.in, err)
		}
		if !got.CreatedAt.Equal(tc.want) {
			t.Fatalf("createdAt %s = %v, want %v", tc.in, got.CreatedAt.Time, tc.want)
		}
	}
}
