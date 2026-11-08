package text

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/profile"
)

// TestCheckProfileBody_checksOnlyABodyWrittenHere proves the file route checks
// a -prp path once: a body fetched for this run was checked by the resolver
// that fetched it, and a profile named with -p is left to the lenient loader.
// A "source" member is no evidence of provenance: it is documented, so a
// hand-written body carrying one is checked like any other file.
func TestCheckProfileBody_checksOnlyABodyWrittenHere(t *testing.T) {
	const nearMiss = `{"model":"test","prompt":"p","promt":"x"}`
	writeBody := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "agent.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
		return path
	}
	envFor := func(string) (profile.Env, error) {
		return profile.Env{ToolNames: []string{"cat"}}, nil
	}
	t.Run("fetched for this run", func(t *testing.T) {
		conf := &Configurations{ProfilePath: writeBody(t, nearMiss), ProfileFetched: true}
		if err := checkProfileBody(t.TempDir(), conf, envFor); err != nil {
			t.Fatalf("checkProfileBody = %v, want no second check of a fetched body", err)
		}
	})
	t.Run("hand-written source stamp is refused", func(t *testing.T) {
		conf := &Configurations{ProfilePath: writeBody(t, `{"prompt":"hello","promt":"typo","source":{}}`)}
		err := checkProfileBody(t.TempDir(), conf, envFor)
		if err == nil || !strings.Contains(err.Error(), `did you mean "prompt"?`) {
			t.Fatalf("checkProfileBody = %v, want the refusal the checker gives a hand-written source", err)
		}
	})
	t.Run("named with -p", func(t *testing.T) {
		conf := &Configurations{UseProfile: "agent"}
		if err := checkProfileBody(t.TempDir(), conf, envFor); err != nil {
			t.Fatalf("checkProfileBody = %v, want the lenient loader", err)
		}
	})
	t.Run("written here and refused", func(t *testing.T) {
		conf := &Configurations{ProfilePath: writeBody(t, nearMiss)}
		err := checkProfileBody(t.TempDir(), conf, envFor)
		if err == nil || !strings.Contains(err.Error(), `did you mean "prompt"?`) {
			t.Fatalf("checkProfileBody = %v, want the refusal the checker gives", err)
		}
	})
	t.Run("written here and valid", func(t *testing.T) {
		conf := &Configurations{ProfilePath: writeBody(t, `{"model":"test","prompt":"p"}`)}
		if err := checkProfileBody(t.TempDir(), conf, envFor); err != nil {
			t.Fatalf("checkProfileBody = %v, want nil", err)
		}
	})
	t.Run("no checker in this build", func(t *testing.T) {
		conf := &Configurations{ProfilePath: writeBody(t, nearMiss)}
		if err := checkProfileBody(t.TempDir(), conf, nil); err != nil {
			t.Fatalf("checkProfileBody = %v, want the lenient path", err)
		}
	})
}
