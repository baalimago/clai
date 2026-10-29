//go:build linux

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAliasConfigs gives "fast" a different ladder in every mode config, the
// shape that needs all three levels of the publish wizard.
func writeAliasConfigs(t *testing.T, confDir string) {
	t.Helper()
	for file, ladder := range map[string]string{
		"textConfig.json":  `["test","gpt-4.1-mini"]`,
		"photoConfig.json": `["gpt-image-1","dall-e-3"]`,
		"audioConfig.json": `["whisper-1"]`,
	} {
		body := `{"model-aliases":{"fast":` + ladder + `}}`
		if err := os.WriteFile(filepath.Join(confDir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
}

// Test_CLOUD_publishWizardOnATerminal drives `clai cloud publish` as a person
// would: on a terminal, answering kind, alias, then mode, and the alias of the
// chosen mode reaches the Hub.
func Test_CLOUD_publishWizardOnATerminal(t *testing.T) {
	env := setupRemoteE2E(t)
	writeAliasConfigs(t, env.confDir)
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)

	// 0 = alias, 0 = fast, 2 = text (modes sort audio, photo, text).
	screen, stdout, status := runRemoteOnPTY(t, env, []string{"cloud", "publish", "-handle", "ada"}, "0\n0\n2")
	if status != 0 {
		t.Fatalf("status %d stdout %q, screen:\n%s", status, stdout, screen)
	}
	for _, want := range []string{
		"0. alias", "1. profile", "2. skill",
		"fast", "audio, photo, text",
		"0. audio", "1. photo", "2. text",
		"Published cchub:ada/fast@1",
		"clai -chat-model cchub:ada/fast@1#sha256=" + digestOf(`["test","gpt-4.1-mini"]`),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout lacks %q:\n%s\nscreen:\n%s", want, stdout, screen)
		}
	}
	env.hub.mu.Lock()
	defer env.hub.mu.Unlock()
	if got := env.hub.agents["ada/fast"]; len(got) != 1 || got[0] != `["test","gpt-4.1-mini"]` {
		t.Fatalf("hub holds %q, want the text ladder", got)
	}
}

// Test_CLOUD_publishWithoutATerminalNamesTheMissingArgument pins that a
// script never waits on a selector: each level it leaves out is an error
// naming the argument to pass, and nothing is sent.
func Test_CLOUD_publishWithoutATerminalNamesTheMissingArgument(t *testing.T) {
	env := setupRemoteE2E(t)
	writeAliasConfigs(t, env.confDir)
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)
	for args, want := range map[string]string{
		"cloud publish":            "clai cloud publish <alias|profile|skill>",
		"cloud publish alias":      "clai cloud publish alias <name>",
		"cloud publish alias fast": "clai cloud publish alias fast <audio|photo|text>",
	} {
		status, _, stderr := runRemote(t, args)
		if status != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("%s: status %d stderr %q, want %q", args, status, stderr, want)
		}
	}
	if n := env.hub.hitsOn("/api/v1/hub"); n != 0 {
		t.Fatalf("hub got %d publishes, want none", n)
	}
}
