package text

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl pins the config's own
// XOR: both command and url set is a parse error naming the file and both
// fields (phase 4, README shared interfaces). The error also names "both"
// specifically (R2-18): the two XOR branches must be distinguishable, not
// one shared, branch-insensitive string that a regression collapsing them
// could still satisfy.
func TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl(t *testing.T) {
	dir := t.TempDir()
	// Named to avoid "both" or "neither" appearing in the path itself
	// (R2-18): the discriminating assertion below must fail for the right
	// reason, not because the file name happens to contain the word.
	path := filepath.Join(dir, "config-a.json")
	if err := os.WriteFile(path, []byte(`{"command":"node","url":"https://mcp.example.com/mcp"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := findConfiguredMcpServers([]string{path})
	if err == nil {
		t.Fatal("expected an error when both command and url are set")
	}
	assertErrNamesFileAndFields(t, err, path)
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("err = %v, want it to say \"both\" are set", err)
	}
}

// TestMcpServerConfigRejectsMissingTransport pins the other half of the
// XOR: neither command nor url set is a parse error naming the file and
// both fields, and naming "neither" specifically (R2-18: see the sibling
// assertion above).
func TestMcpServerConfigRejectsMissingTransport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config-b.json")
	if err := os.WriteFile(path, []byte(`{"env":{"K":"V"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := findConfiguredMcpServers([]string{path})
	if err == nil {
		t.Fatal("expected an error when neither command nor url is set")
	}
	assertErrNamesFileAndFields(t, err, path)
	if !strings.Contains(err.Error(), "neither") {
		t.Errorf("err = %v, want it to say \"neither\" is set", err)
	}
}

// Rejecting a url that is not an absolute http or https URL is pinned
// directly against the relocated parser: serverconfig's own
// TestFindConfiguredServers_RejectsNonAbsoluteEndpoint (phase 7, relocated
// from this file to avoid a verbatim duplicate per the dupl gate).

func assertErrNamesFileAndFields(t *testing.T, err error, path string) {
	t.Helper()
	msg := err.Error()
	if !strings.Contains(msg, path) {
		t.Errorf("err = %v, want it to name the file %q", err, path)
	}
	if !strings.Contains(msg, "command") || !strings.Contains(msg, "url") {
		t.Errorf("err = %v, want it to name both \"command\" and \"url\"", err)
	}
}
