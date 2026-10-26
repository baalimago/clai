package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

const fakeHubToken = "tok-ada"

// publishFromCLI is clai.chat's POST /api/v1/hub (cloud/site/api.ts in
// clai-cloud): a write key publishes a profile as its account, the first
// time under a new name and as the next version after that.
func (h *fakeHub) publishFromCLI(w http.ResponseWriter, r *http.Request) {
	answer := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+fakeHubToken {
		answer(http.StatusUnauthorized, map[string]string{"error": "unauthorized", "message": "Log in again: clai cloud login"})
		return
	}
	var req struct{ Name, Body, Handle, Note string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		answer(http.StatusBadRequest, map[string]string{"error": "invalid_request", "message": "Send a JSON object."})
		return
	}
	if h.handle == "" {
		if req.Handle == "" {
			answer(http.StatusConflict, map[string]string{"error": "handle_required", "message": "Pick a handle.", "suggested": "ada"})
			return
		}
		h.handle = req.Handle
	}
	key := h.handle + "/" + req.Name
	h.agents[key] = append(h.agents[key], req.Body)
	n := len(h.agents[key])
	status := http.StatusOK
	if n == 1 {
		status = http.StatusCreated
	}
	answer(status, map[string]any{
		"address": "cchub:" + key, "version": n, "created": n == 1,
		"url": fmt.Sprintf("https://%s/hub/%s/", r.Host, key), "sha256": digestOf(req.Body),
	})
}

// The whole round trip: publish a local profile, pick a handle the first
// time, publish a second version, then run each version by its address.
func Test_HUB_publishThenRunByAddress(t *testing.T) {
	env := setupRemoteE2E(t)
	// The cloud client and the remote fetch read the same CLAI_CLOUD_URL;
	// the publish half runs against the same hub over plain http.
	api := httptest.NewServer(env.hub)
	t.Cleanup(api.Close)
	hubURL := env.hub.srv.URL
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)
	writeBot := func(prompt string) {
		t.Helper()
		body := fmt.Sprintf(`{"model":"test","prompt":%q}`, prompt)
		if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "trade-bot.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	readBot := func(t *testing.T) string {
		t.Helper()
		return e2etest.ReadStringFile(t, filepath.Join(env.confDir, "profiles", "trade-bot.json"))
	}
	writeBot("You trade, version one.")

	t.Setenv("CLAI_CLOUD_URL", api.URL)
	status, stdout, stderr := runRemote(t, "profiles publish trade-bot")
	if status != 1 || !strings.Contains(stderr, "clai profiles publish trade-bot -handle ada") {
		t.Fatalf("no handle yet: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	status, stdout, stderr = runRemote(t, "profiles publish trade-bot -handle ada -summary Trades.")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/trade-bot@1\n") {
		t.Fatalf("first publish: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	// The run line pins the bytes sent: the profile as it is on disk.
	v1 := digestOf(readBot(t))
	if !strings.Contains(stdout, "clai -prp cchub:ada/trade-bot@1#sha256="+v1) {
		t.Fatalf("first publish shows no pinned run line: %q", stdout)
	}

	writeBot("You trade, version two.")
	status, stdout, stderr = runRemote(t, "profiles publish trade-bot -note tighter")
	v2 := digestOf(readBot(t))
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/trade-bot@2 (a new version)") {
		t.Fatalf("second publish: status %d stdout %q stderr %q", status, stdout, stderr)
	}

	writeBot("Use sk-abcdefghijklmnopqrstuvwx to trade.")
	before := env.hub.hitsOn("/api/v1/hub")
	status, _, stderr = runRemote(t, "profiles publish trade-bot")
	if status != 1 || !strings.Contains(stderr, "holds what looks like a credential") || env.hub.hitsOn("/api/v1/hub") != before {
		t.Fatalf("secret: status %d stderr %q, want a refusal before any request", status, stderr)
	}

	t.Setenv("CLAI_CLOUD_URL", hubURL)
	status, stdout, stderr = runRemote(t, "-r -prp cchub:ada/trade-bot@1#sha256="+v1+" q hi")
	if status != 0 || stdout != "hi\n" || !strings.Contains(env.savedConversation(t), "You trade, version one.") {
		t.Fatalf("run @1: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	status, _, stderr = runRemote(t, "-r -prp cchub:ada/trade-bot#sha256="+v2+" q hi")
	if status != 0 {
		t.Fatalf("run newest: status %d stderr %q", status, stderr)
	}
}

func Test_HUB_publishNeedsALogin(t *testing.T) {
	env := setupRemoteE2E(t)
	if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "bot.json"), []byte(remoteBotV1), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAI_CLOUD_TOKEN", "")
	status, _, stderr := runRemote(t, "profiles publish bot")
	if status != 1 || !strings.Contains(stderr, "clai cloud login") {
		t.Fatalf("status %d stderr %q", status, stderr)
	}
}
