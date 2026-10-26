package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

const remoteBot = `{"model":"test","prompt":"You are a test bot."}`

// setupRemoteE2E serves one hub agent over TLS and points clai's hub at it.
func setupRemoteE2E(t *testing.T) (confDir string, hits *int) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir = e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("CLAI_CLOUD", "")
	n := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.URL.Path != "/hub/ada/bot/v/1/raw" && r.URL.Path != "/hub/ada/bot/raw" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(remoteBot))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CLAI_CLOUD_URL", srv.URL)
	old := harness.Deps.RemoteClient
	harness.Deps.RemoteClient = srv.Client()
	t.Cleanup(func() { harness.Deps.RemoteClient = old })
	return confDir, &n
}

func runRemote(t *testing.T, args string) (int, string, string) {
	t.Helper()
	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = harness.Run(strings.Split(args, " "))
		})
	})
	return status, stdout, stderr
}

func Test_REMOTE_untrustedTargetExitsThreeWithoutATerminal(t *testing.T) {
	_, _ = setupRemoteE2E(t)
	status, stdout, stderr := runRemote(t, "-r -prp hub:ada/bot@1 q hello")
	if status != remote.ExitRefused {
		t.Fatalf("status = %d stdout = %q stderr = %q, want 3", status, stdout, stderr)
	}
	for _, want := range []string{"no terminal to ask in", "clai profiles trust hub:ada/bot@1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q lacks %q", stderr, want)
		}
	}
}

func Test_REMOTE_hashIsConsentThenTheCacheServesIt(t *testing.T) {
	_, hits := setupRemoteE2E(t)
	pinned := "hub:ada/bot@1#sha256=" + remote.Digest([]byte(remoteBot))
	status, stdout, stderr := runRemote(t, "-r -prp "+pinned+" q hello")
	if status != 0 || stdout != "hello\n" {
		t.Fatalf("pinned run: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	status, stdout, _ = runRemote(t, "-r -prp hub:ada/bot@1 q again")
	if status != 0 || stdout != "again\n" || *hits != 1 {
		t.Fatalf("cached run: status %d stdout %q hits %d, want one fetch in all", status, stdout, *hits)
	}
	t.Setenv("CLAI_CLOUD", "off")
	status, _, stderr = runRemote(t, "-r -prp hub:ada/other@1 q hi")
	if status != remote.ExitRefused || !strings.Contains(stderr, "CLAI_CLOUD=off") {
		t.Fatalf("offline uncached: status %d stderr %q", status, stderr)
	}
}

func Test_REMOTE_installThenRunByName(t *testing.T) {
	confDir, _ := setupRemoteE2E(t)
	status, stdout, stderr := runRemote(t, "profiles install hub:ada/bot@1")
	if status != 0 || !strings.Contains(stdout, "Installed hub:ada/bot@1#sha256=") {
		t.Fatalf("install: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	b, err := os.ReadFile(filepath.Join(confDir, "profiles", "bot.json"))
	if err != nil || !strings.Contains(string(b), `"source"`) {
		t.Fatalf("installed profile = %q, %v", b, err)
	}
	t.Setenv("CLAI_CLOUD", "off")
	status, stdout, _ = runRemote(t, "-r -p bot q offline")
	if status != 0 || stdout != "offline\n" {
		t.Fatalf("run installed: status %d stdout %q", status, stdout)
	}
	status, stdout, _ = runRemote(t, "profiles")
	if status != 0 || !strings.Contains(stdout, "Source: hub:ada/bot@1#sha256=") {
		t.Fatalf("list: status %d stdout %q", status, stdout)
	}
}
