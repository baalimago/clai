package records

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// TestMcpServerStderrIsTheSameTextInBothFormats: a server's stderr line with
// a "\r", an SGR sequence, a bell and a secret-shaped value reaches the run's
// stderr as the same lines with and without -json. The text form shows each
// control character as a visible escape; the JSON string escapes it.
func TestMcpServerStderrIsTheSameTextInBothFormats(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	written := "ERROR: something happened\rspin error \x1b[31mred\x1b[0m bell\a token=supersecretvalue123"
	for _, name := range []string{"paritytext", "parityjson"} {
		profile, err := json.Marshal(map[string]any{
			"model": "test", "use_tools": true,
			"mcp_servers": map[string]any{name: map[string]any{
				"command": e2etest.TestServerBinary(t),
				"env":     map[string]string{"TEST_SERVER_STDERR_TEXT": written},
			}},
		})
		if err != nil {
			t.Fatalf("encode profile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(confDir, "profiles", name+".json"), profile, 0o644); err != nil {
			t.Fatalf("write profile: %v", err)
		}
	}
	args := func(name string) []string {
		return strings.Fields(fmt.Sprintf("-r -cm test -p %s q tool_mcp_%s_echo", name, name))
	}
	text := runOnStreams(args("paritytext"), streamOpts{})
	asJSON := runOnStreams(append([]string{"-json"}, args("parityjson")...), streamOpts{})
	if text.status != 0 || asJSON.status != 0 {
		t.Fatalf("status text %d json %d, want 0; text stderr %q json stderr %q", text.status, asJSON.status, text.stderr, asJSON.stderr)
	}

	wantText := []string{"ERROR: something happened", `spin error \x1b[31mred\x1b[0m bell\x07 token=[REDACTED]`}
	wantJSON := []string{"ERROR: something happened", "spin error \x1b[31mred\x1b[0m bell\a token=[REDACTED]"}

	var gotText []string
	for line := range strings.SplitSeq(text.stderr, "\n") {
		if rest, ok := strings.CutPrefix(line, "mcp_paritytext: "); ok {
			gotText = append(gotText, rest)
		}
	}
	if !slices.Equal(gotText, wantText) {
		t.Errorf("text stderr lines = %q, want %q; stderr %q", gotText, wantText, text.stderr)
	}
	if strings.ContainsAny(text.stderr, "\x1b\a\r") || strings.Contains(text.stderr, "supersecret") {
		t.Errorf("text stderr holds a raw control character or the secret: %q", text.stderr)
	}

	var gotJSON []string
	for _, r := range ofType(parseRecords(t, "stderr", asJSON.stderr), "child_output") {
		if r.str("source") != "mcp_parityjson" || r.str("stream") != "stderr" {
			t.Fatalf("child_output record %v, want source mcp_parityjson on stderr", r)
		}
		gotJSON = append(gotJSON, r.str("text"))
	}
	if !slices.Equal(gotJSON, wantJSON) {
		t.Errorf("json child_output texts = %q, want %q; stderr %q", gotJSON, wantJSON, asJSON.stderr)
	}
}
