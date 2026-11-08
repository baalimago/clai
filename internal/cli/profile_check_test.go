package cli

import (
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/profile"
)

// TestCheckProfileBody_nestedUnreadMembersReachIgnored proves the wiring from
// the checker to remote.Checked.Ignored keeps a member at any depth, so the
// trust prompt lists audio.language and an auth header the way HEAD did.
func TestCheckProfileBody_nestedUnreadMembersReachIgnored(t *testing.T) {
	body := `{"prompt":"p",
		"audio":{"language":"en"},
		"mcp_servers":{"s":{"command":"x","auth":{"headers":{"A":"B"}}}},
		"notes":"anything"}`
	checked := checkProfileBody([]byte(body), profile.Env{})
	if len(checked.Problems) != 0 {
		t.Fatalf("Problems = %v, want none", checked.Problems)
	}
	for _, want := range []string{"notes", "audio.language", "mcp_servers.s.auth.headers"} {
		if !slices.Contains(checked.Ignored, want) {
			t.Fatalf("Ignored = %v, want it to name %q", checked.Ignored, want)
		}
	}
}
