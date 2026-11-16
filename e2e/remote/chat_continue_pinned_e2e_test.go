package remote

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/remote"
)

var chatRecord = regexp.MustCompile(`\{"type":"chat","id":"([^"]+)"\}`)

// Minutes in clai-in-wasmer-sh-poc answers in a meeting's conversation with
// `clai -json chat continue -reply-only -n -prp <target>#sha256=<hex> <id> <prompt>`,
// the hash being the one its Start checked. A version the hub publishes in
// between reaches neither the reply nor the conversation. Without the -prp,
// the chat's stamp, which keeps no hash, is fetched again and refused.
func Test_REMOTE_chatContinueRunsTheGivenPinAfterANewVersion(t *testing.T) {
	env := setupRemoteE2E(t)
	if status, _, stderr := runRemote(t, "config trust profile cchub:ada/bot"); status != 0 {
		t.Fatalf("trust: status %d stderr %q", status, stderr)
	}
	pin := "cchub:ada/bot#sha256=" + digestOf(remoteBotV1)
	status, stdout, stderr := runRemote(t, "-json -n -prp "+pin+" q first meeting")
	m := chatRecord.FindStringSubmatch(stdout)
	if status != 0 || m == nil {
		t.Fatalf("pinned run: status %d stdout %q stderr %q; want a chat record", status, stdout, stderr)
	}
	id := m[1]
	conversation := func() string {
		b, err := os.ReadFile(filepath.Join(env.confDir, "conversations", id+".json"))
		if err != nil {
			t.Fatalf("read conversation %s: %v", id, err)
		}
		return string(b)
	}

	// A version that could not even run: a reply that ran it would fail.
	env.hub.publish("ada/bot", `{"model":"no-such-model","prompt":"You are bot two."}`)
	before := env.hub.fetches()
	status, stdout, stderr = runRemote(t, "-json chat continue -reply-only -n -prp "+pin+" "+id+" the answer")
	if m := chatRecord.FindStringSubmatch(stderr); status != 0 || m == nil || m[1] != id || env.hub.fetches() != before {
		t.Fatalf("pinned reply: status %d stdout %q stderr %q fetches %d -> %d; want %s continued from the cache", status, stdout, stderr, before, env.hub.fetches(), id)
	}
	if c := conversation(); !strings.Contains(c, `"content": "the answer"`) || !strings.Contains(c, "You are bot one.") || strings.Contains(c, "You are bot two.") {
		t.Fatalf("the reply did not continue version one's conversation: %s", c)
	}

	status, _, stderr = runRemote(t, "chat continue -n "+id+" unpinned")
	if status != remote.ExitRefused || !strings.Contains(stderr, "changed to a version") {
		t.Fatalf("reply on the stamp: status %d stderr %q, want the new version refused", status, stderr)
	}
	if strings.Contains(conversation(), "unpinned") {
		t.Fatalf("a refused reply reached the conversation: %s", conversation())
	}
}
