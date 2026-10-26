package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// A pull that stops for a decision exits 4 through cli.Run, although
// cmd.Run itself only returns 0 or 1.
func Test_e2e_cloud_pull_needing_a_decision_exits_4(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	srv := cloudtest.New(t)
	srv.Put("config", "profiles/x.json", "theirs")
	t.Setenv("CLAI_CLOUD_URL", srv.URL())
	t.Setenv("CLAI_CLOUD_TOKEN", "tok-a")
	p := filepath.Join(confDir, "profiles", "x.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run(strings.Split("cloud pull config", " "))
	})
	testboil.FailTestIfDiff(t, status, 4)
	if !strings.Contains(stdout, "≠ 1 differs") || !strings.Contains(stdout, "profiles/x.json") {
		t.Fatalf("stdout = %q", stdout)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Fatal("the local file changed")
	}

	stdout = testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run(strings.Split("cloud pull config -theirs", " "))
	})
	testboil.FailTestIfDiff(t, status, 0)
	if b, _ := os.ReadFile(p); string(b) != "theirs" {
		t.Fatalf("pull -theirs left %q: %s", b, stdout)
	}
}

// status -imports previews a conversation another tool holds without
// exporting it; commit -imports then exports and commits it.
func Test_e2e_cloud_status_previews_other_tools_and_commit_takes_them(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
	srv := cloudtest.New(t)
	t.Setenv("CLAI_CLOUD_URL", srv.URL())
	t.Setenv("CLAI_CLOUD_TOKEN", "tok-a")
	jsonl := "" +
		`{"type":"session_meta","payload":{"session_id":"cdx-1","cwd":"/work","timestamp":"2026-01-01T00:00:00Z","thread_source":"user"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello codex"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello human"}]}}` + "\n"
	p := filepath.Join(home, ".codex", "sessions", "2026", "01", "01", "rollout-cdx-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(jsonl), 0o600); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(confDir, "conversations", "imported", "codex", "cdx-1.json")

	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run(strings.Split("cloud status -imports conversations", " "))
	})
	testboil.FailTestIfDiff(t, status, 0)
	if !strings.Contains(stdout, "▸ from other tools · 1 conversation to export") || !strings.Contains(stdout, "■ codex") {
		t.Fatalf("status -imports:\n%s", stdout)
	}
	if _, err := os.Stat(exported); !os.IsNotExist(err) {
		t.Fatalf("status exported the conversation: %v", err)
	}

	stdout = testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run(strings.Split("cloud commit -imports conversations", " "))
	})
	testboil.FailTestIfDiff(t, status, 0)
	if _, err := os.Stat(exported); err != nil {
		t.Fatalf("commit -imports did not export: %v\n%s", err, stdout)
	}
	stdout = testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run(strings.Split("cloud status -imports conversations", " "))
	})
	if !strings.Contains(stdout, "▸ from other tools · nothing new to export") {
		t.Fatalf("status after the commit:\n%s", stdout)
	}
}

// A cloud subcommand's bad flag names that subcommand's usage only, and an
// unknown subcommand fails instead of printing help.
func Test_e2e_cloud_usage_errors_stay_with_the_subcommand(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	for _, line := range []string{"cloud status -zz", "cloud log -limit abc", "cloud bogus"} {
		var status int
		var stdout string
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			stdout = testboil.CaptureStdout(t, func(t *testing.T) {
				status = harness.Run(strings.Split(line, " "))
			})
		})
		if status != 1 {
			t.Errorf("%s: status %d", line, status)
		}
		all := stdout + stderr
		if strings.Contains(all, "unknown error") || strings.Contains(all, "(c)ommand (l)ine") {
			t.Errorf("%s printed the top-level usage:\n%s", line, all)
		}
		want := map[string]string{"cloud status -zz": "cloud status [store]", "cloud log -limit abc": "cloud log [store]", "cloud bogus": `unknown cloud subcommand "bogus"`}[line]
		if !strings.Contains(stderr, want) {
			t.Errorf("%s: stderr lacks %q:\n%s", line, want, stderr)
		}
	}
}
