package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/runtally"
)

// Test_e2e_query_summary_counts_subagents pins the lead side of subagent
// tallies: a worker the lead starts reports into the directory named in
// CLAI_RUN_TALLY_DIR, and the lead's -qs summary shows it on its own line.
// The worker here is a shell command writing the report a child clai writes.
func Test_e2e_query_summary_counts_subagents(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	t.Setenv(runtally.EnvDir, "")
	report := `{"runs":2,"prompt_tokens":1200,"completion_tokens":30,"cost_usd":0.05}`
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf '%s' '`+report+`' > "$`+runtally.EnvDir+`/run-worker.json"`)

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split("-r -cm test -t=cmd -qs q tool_cmd", " "))
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr=%q", status, stderr)
	}
	if want := "  subagents 2 runs · 1.2k in · 30 out · ~$0.05"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr lacks %q:\n%s", want, stderr)
	}
	if got := os.Getenv(runtally.EnvDir); got != "" {
		t.Fatalf("%s = %q after the run, want it unset again", runtally.EnvDir, got)
	}
}

// Test_e2e_worker_reports_to_its_lead pins the worker side: a clai started
// under a lead's CLAI_RUN_TALLY_DIR writes its totals there, without -qs and
// without a word on stdout or stderr about it.
func Test_e2e_worker_reports_to_its_lead(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	lead := t.TempDir()
	t.Setenv(runtally.EnvDir, lead)

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split("-r -cm test q hello", " "))
	})
	if status != 0 {
		t.Fatalf("status = %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "subagents") {
		t.Fatalf("a worker printed a summary: stdout=%q stderr=%q", stdout, stderr)
	}
	paths, err := filepath.Glob(filepath.Join(lead, "run-*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("reports = %v (%v), want one", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got runtally.Totals
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Runs != 1 || got.PromptTokens == 0 || got.CompletionTokens == 0 {
		t.Fatalf("report = %+v, want one run with its tokens", got)
	}
	if os.Getenv(runtally.EnvDir) != lead {
		t.Fatalf("%s = %q after the run, want %q back", runtally.EnvDir, os.Getenv(runtally.EnvDir), lead)
	}
}
