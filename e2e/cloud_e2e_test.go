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
	if !strings.Contains(stdout, "1 differ: profiles/x.json") {
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
