package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestProbeInstallNeedsInstallForUnmodelledRunner pins the reported failure:
// a uvx server names no content clai has seen, so connecting it may first
// download, and the package it names is what a status line should say.
func TestProbeInstallNeedsInstallForUnmodelledRunner(t *testing.T) {
	testenv.FakeBins(t, "uvx")
	got := ProbeInstall(pub_models.McpServer{
		Command: "uvx",
		Args:    []string{"duckduckgo-mcp-server==0.7.0"},
	})
	if !got.Need.NeedsInstall() {
		t.Fatalf("Need = %v, want NeedsInstall", got.Need)
	}
	if got.Target != "duckduckgo-mcp-server" {
		t.Errorf("Target = %q, want the installed package name", got.Target)
	}
}

// TestProbeInstallNeedsInstallForPinnedNpxWithoutInstallDir pins that a pin is
// evidence about the config, never about the disk: a pinned spec npm has not
// installed yet downloads on this run exactly like a floating one.
func TestProbeInstallNeedsInstallForPinnedNpxWithoutInstallDir(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", t.TempDir())
	got := ProbeInstall(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.2"}})
	if !got.Need.NeedsInstall() {
		t.Fatalf("Need = %v, want NeedsInstall for a spec npm holds no install for", got.Need)
	}
	if got.Target != "slivingdoc" {
		t.Errorf("Target = %q, want slivingdoc", got.Target)
	}
}

// TestProbeInstallEvidencedForInstalledNpx pins the other direction: an install
// directory that names the spec is local evidence, so nothing downloads and no
// install wait is owed.
func TestProbeInstallEvidencedForInstalledNpx(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")
	got := ProbeInstall(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Need.NeedsInstall() {
		t.Fatalf("Need = %v, want NoInstallNeeded for an installed spec", got.Need)
	}
	if got.Target != "slivingdoc" {
		t.Errorf("Target = %q, want slivingdoc", got.Target)
	}
}

// TestProbeInstallEvidencedForDirectPrograms pins that the shapes whose content
// clai can see owe no install wait: an endpoint, a direct binary, and an
// interpreter given a local script path.
func TestProbeInstallEvidencedForDirectPrograms(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.js")
	if err := os.WriteFile(script, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		server pub_models.McpServer
	}{
		{name: "endpoint", server: pub_models.McpServer{Url: "https://example.invalid/mcp"}},
		{name: "interpreter with script", server: pub_models.McpServer{Command: "node", Args: []string{script}}},
		{name: "spawned binary", server: pub_models.McpServer{Command: "sh", Args: []string{"-c", "true"}}},
	}
	for _, tt := range tests {
		if got := ProbeInstall(tt.server); got.Need.NeedsInstall() {
			t.Errorf("%s: Need = %v, want NoInstallNeeded", tt.name, got.Need)
		}
	}
}

// TestProbeInstallNamesNoTargetForOpaqueRunner pins that a runner whose
// arguments are not a package spec still reports the install, with no target:
// a status line must never name the wrong package.
func TestProbeInstallNamesNoTargetForOpaqueRunner(t *testing.T) {
	testenv.FakeBins(t, "docker")
	got := ProbeInstall(pub_models.McpServer{Command: "docker", Args: []string{"run", "-i", "img/server"}})
	if !got.Need.NeedsInstall() {
		t.Fatalf("Need = %v, want NeedsInstall for a container that may need pulling", got.Need)
	}
	if got.Target != "" {
		t.Errorf("Target = %q, want empty for an unreadable command line", got.Target)
	}
}
