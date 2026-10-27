package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

const (
	remoteBotV1 = `{"model":"test","prompt":"You are bot one."}`
	remoteBotV2 = `{"model":"test","prompt":"You are bot two."}`
)

// fakeHub serves what clai.chat's site Worker serves for agents
// (cloud/site/app.ts in clai-cloud): /hub/<handle>/<name>/raw for the
// newest version and /hub/<handle>/<name>/v/<n>/raw for one version, as
// application/json, and 404 for anything else. It also stands in for
// raw.githubusercontent.com under /gh/ and for any https host under
// /files/, so every target kind runs through the real fetch path.
type fakeHub struct {
	srv *httptest.Server

	mu     sync.Mutex
	agents map[string][]string
	files  map[string]fakeFile
	hits   map[string]int
	// skills names the hub entries that are skills, served as Markdown.
	skills map[string]bool
	// handle is the account behind fakeHubToken, "" until it picks one.
	handle string
}

type fakeFile struct {
	contentType string
	body        string
}

var hubRaw = regexp.MustCompile(`^/hub/([a-z0-9-]+/[a-z0-9-]+)(?:/v/([1-9][0-9]*))?/raw$`)

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hits[r.URL.Path]++
	if r.URL.Path == "/api/v1/hub" {
		h.publishFromCLI(w, r)
		return
	}
	if m := hubRaw.FindStringSubmatch(r.URL.Path); m != nil {
		versions := h.agents[m[1]]
		n := len(versions)
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		if n < 1 || n > len(versions) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if h.skills[m[1]] {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		}
		_, _ = w.Write([]byte(versions[n-1]))
		return
	}
	if f, ok := h.files[r.URL.Path]; ok {
		w.Header().Set("Content-Type", f.contentType)
		_, _ = w.Write([]byte(f.body))
		return
	}
	http.NotFound(w, r)
}

func (h *fakeHub) publish(agent, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[agent] = append(h.agents[agent], body)
}

func (h *fakeHub) publishSkill(skill, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.skills[skill] = true
	h.agents[skill] = append(h.agents[skill], body)
}

func (h *fakeHub) serveFile(path, contentType, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = fakeFile{contentType: contentType, body: body}
}

func (h *fakeHub) hitsOn(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits[path]
}

func (h *fakeHub) fetches() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.hits {
		n += c
	}
	return n
}

// githubToHub sends raw.githubusercontent.com requests to the fake hub's
// /gh/ tree and every other request through unchanged.
type githubToHub struct {
	hub    *url.URL
	client *http.Client
}

func (d githubToHub) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "raw.githubusercontent.com" {
		u := *r.URL
		u.Host = d.hub.Host
		u.Path = "/gh" + r.URL.Path
		r = r.Clone(context.WithoutCancel(r.Context()))
		r.URL = &u
		r.Host = u.Host
	}
	return d.client.Do(r)
}

type remoteE2E struct {
	confDir  string
	cacheDir string
	hub      *fakeHub
}

// setupRemoteE2E starts a fake hub with ada/bot at version 1 and points
// clai's hub at it, with a fresh config and cache directory.
func setupRemoteE2E(t *testing.T) remoteE2E {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	env := remoteE2E{confDir: e2etest.SetupMainTestConfigDir(t), cacheDir: filepath.Join(t.TempDir(), "cache")}
	t.Setenv("CLAI_CACHE_DIR", env.cacheDir)
	t.Setenv("CLAI_CLOUD", "")
	env.hub = &fakeHub{agents: map[string][]string{}, files: map[string]fakeFile{}, hits: map[string]int{}, skills: map[string]bool{}}
	env.hub.publish("ada/bot", remoteBotV1)
	env.hub.srv = httptest.NewTLSServer(env.hub)
	t.Cleanup(env.hub.srv.Close)
	t.Setenv("CLAI_CLOUD_URL", env.hub.srv.URL)
	hubURL, err := url.Parse(env.hub.srv.URL)
	if err != nil {
		t.Fatalf("parse hub url: %v", err)
	}
	old := harness.Deps.RemoteClient
	harness.Deps.RemoteClient = githubToHub{hub: hubURL, client: env.hub.srv.Client()}
	t.Cleanup(func() { harness.Deps.RemoteClient = old })
	return env
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

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// assertCacheHolds fails unless the remote cache holds exactly the index
// and one file per digest, each still hashing to its own name.
func (e remoteE2E) assertCacheHolds(t *testing.T, digests ...string) {
	t.Helper()
	dir := filepath.Join(e.cacheDir, remote.CacheSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read remote cache: %v", err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	want := []string{"index.json"}
	for _, d := range digests {
		want = append(want, d+".json")
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("remote cache holds %v, want %v", got, want)
	}
	for _, d := range digests {
		b, err := os.ReadFile(filepath.Join(dir, d+".json"))
		if err != nil || digestOf(string(b)) != d {
			t.Fatalf("cached %s no longer matches its hash: %q, %v", d, b, err)
		}
	}
}

func (e remoteE2E) trusted(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.confDir, remote.TrustFile))
	if err != nil {
		return ""
	}
	return string(b)
}

func (e remoteE2E) savedConversation(t *testing.T) string {
	t.Helper()
	return e2etest.ReadStringFile(t, e2etest.FindSavedConversationFile(t, e.confDir))
}

func Test_REMOTE_untrustedTargetExitsThreeWithoutATerminal(t *testing.T) {
	env := setupRemoteE2E(t)
	status, stdout, stderr := runRemote(t, "-prp cchub:ada/bot q hello")
	if status != remote.ExitRefused || stdout != "" {
		t.Fatalf("status = %d stdout = %q stderr = %q, want 3 and no output", status, stdout, stderr)
	}
	pin := "cchub:ada/bot#sha256=" + digestOf(remoteBotV1)
	for _, want := range []string{"no terminal to ask in", "clai profiles trust cchub:ada/bot", pin, env.hub.srv.URL + "/hub/ada/bot/"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q lacks %q", stderr, want)
		}
	}
	if env.trusted(t) != "" {
		t.Fatalf("a refused run recorded trust: %s", env.trusted(t))
	}
}

// The journey a cron job takes: pin by hash once, then run from the trust
// store and the cache, then offline. Runs are not raw, as a user's are, so
// the config loader's write paths are exercised too.
func Test_REMOTE_hashIsConsentThenTrustAndCacheServeIt(t *testing.T) {
	env := setupRemoteE2E(t)
	v1 := digestOf(remoteBotV1)
	status, stdout, stderr := runRemote(t, "-r -prp cchub:ada/bot#sha256="+v1+" q hello")
	if status != 0 || stdout != "hello\n" {
		t.Fatalf("pinned run: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if !strings.Contains(env.savedConversation(t), "You are bot one.") {
		t.Fatalf("the remote profile's prompt did not reach the conversation: %s", env.savedConversation(t))
	}
	if !strings.Contains(env.trusted(t), v1) {
		t.Fatalf("trust store lacks %s: %s", v1, env.trusted(t))
	}

	status, _, stderr = runRemote(t, "-prp cchub:ada/bot q again")
	if status != 0 {
		t.Fatalf("trusted run: status %d stderr %q", status, stderr)
	}
	env.assertCacheHolds(t, v1)

	t.Setenv("CLAI_CLOUD", "off")
	before := env.hub.fetches()
	status, _, stderr = runRemote(t, "-prp cchub:ada/bot q offline")
	if status != 0 || env.hub.fetches() != before {
		t.Fatalf("offline run: status %d stderr %q fetches %d -> %d", status, stderr, before, env.hub.fetches())
	}
	env.assertCacheHolds(t, v1)
	status, _, stderr = runRemote(t, "-prp cchub:ada/other q hi")
	if status != remote.ExitRefused || !strings.Contains(stderr, "CLAI_CLOUD=off") {
		t.Fatalf("offline uncached: status %d stderr %q", status, stderr)
	}
}

func Test_REMOTE_pinnedVersionIsFetchedOnce(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.publish("ada/bot", remoteBotV2)
	v1 := digestOf(remoteBotV1)
	status, _, stderr := runRemote(t, "-r -prp cchub:ada/bot@1#sha256="+v1+" q one")
	if status != 0 {
		t.Fatalf("pinned @1: status %d stderr %q", status, stderr)
	}
	status, _, stderr = runRemote(t, "-r -prp cchub:ada/bot@1 q two")
	if status != 0 || env.hub.hitsOn("/hub/ada/bot/v/1/raw") != 1 {
		t.Fatalf("cached @1: status %d stderr %q fetches of v1 %d, want 1", status, stderr, env.hub.hitsOn("/hub/ada/bot/v/1/raw"))
	}
	if !strings.Contains(env.savedConversation(t), "You are bot one.") {
		t.Fatalf("@1 did not run version 1")
	}
}

// The summary shows what a fetched profile runs on this machine, escaped so
// its bytes cannot drive the terminal.
func Test_REMOTE_trustShowsWhatTheProfileRunsHere(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.publish("ada/ops", `{"use_tools":true,"prompt":"Ops bot.\u001b[2J\nCheck the deploys.","mcp_servers":{
		"vault":{"url":"https://mcp.example.com","env":{"VAULT_NAMESPACE":"ops"},"auth":{"token_command":["vault","print","token"]}}}}`)
	env.hub.publish("ada/all", `{"use_tools":true}`)
	status, stdout, stderr := runRemote(t, "profiles trust cchub:ada/ops")
	if status != 0 {
		t.Fatalf("trust: status %d stderr %q", status, stderr)
	}
	for _, want := range []string{
		"  vault  https://mcp.example.com\n",
		"         runs here for a token: vault print token\n",
		"         env: VAULT_NAMESPACE=ops\n",
		"Prompt:\n  Ops bot.\\x1b[2J\n  Check the deploys.\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("summary:\n%s\nlacks %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("summary holds a raw escape: %q", stdout)
	}
	status, stdout, stderr = runRemote(t, "profiles trust cchub:ada/all")
	if status != 0 || !strings.Contains(stdout, "  no tools, no MCP servers\n") {
		t.Fatalf("use_tools alone: status %d stdout %q stderr %q", status, stdout, stderr)
	}
}

func Test_REMOTE_newVersionNeedsTrustAgain(t *testing.T) {
	env := setupRemoteE2E(t)
	status, _, stderr := runRemote(t, "profiles trust cchub:ada/bot")
	if status != 0 {
		t.Fatalf("trust: status %d stderr %q", status, stderr)
	}
	env.hub.publish("ada/bot", remoteBotV2)
	status, stdout, stderr := runRemote(t, "-r -prp cchub:ada/bot q hi")
	if status != remote.ExitRefused || stdout != "" || !strings.Contains(stderr, "changed to a version") {
		t.Fatalf("changed agent: status %d stdout %q stderr %q, want a refusal naming the change", status, stdout, stderr)
	}
	status, _, stderr = runRemote(t, "-r -prp cchub:ada/bot#sha256="+digestOf(remoteBotV2)+" q hi")
	if status != 0 {
		t.Fatalf("v2 by hash: status %d stderr %q", status, stderr)
	}
}

func Test_REMOTE_wrongHashRunsNothingAndKeepsNothing(t *testing.T) {
	env := setupRemoteE2E(t)
	wrong := digestOf("something else")
	status, stdout, stderr := runRemote(t, "-r -prp cchub:ada/bot#sha256="+wrong+" q hi")
	if status != remote.ExitRefused || stdout != "" || !strings.Contains(stderr, "does not match its #sha256=") {
		t.Fatalf("status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if env.trusted(t) != "" {
		t.Fatalf("a mismatch recorded trust: %s", env.trusted(t))
	}
	if _, err := os.Stat(filepath.Join(env.cacheDir, remote.CacheSubdir)); !os.IsNotExist(err) {
		t.Fatalf("a mismatch wrote the cache: %v", err)
	}
}

func Test_REMOTE_installUpdateAndRunByName(t *testing.T) {
	env := setupRemoteE2E(t)
	status, stdout, stderr := runRemote(t, "profiles install cchub:ada/bot")
	if status != 0 || !strings.Contains(stdout, "Installed cchub:ada/bot#sha256="+digestOf(remoteBotV1)) {
		t.Fatalf("install: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	installed := filepath.Join(env.confDir, "profiles", "bot.json")
	if b, err := os.ReadFile(installed); err != nil || !strings.Contains(string(b), `"source"`) {
		t.Fatalf("installed profile = %q, %v", b, err)
	}
	status, stdout, _ = runRemote(t, "profiles update bot")
	if status != 0 || !strings.Contains(stdout, "bot is up to date") {
		t.Fatalf("update with nothing new: status %d stdout %q", status, stdout)
	}

	env.hub.publish("ada/bot", remoteBotV2)
	// Updating them all asks before a new version; with no terminal it is left.
	status, _, stderr = runRemote(t, "profiles update")
	if status == 0 || !strings.Contains(stderr, "you have not trusted") {
		t.Fatalf("update all without a terminal: status %d stderr %q", status, stderr)
	}
	status, stdout, stderr = runRemote(t, "profiles update bot")
	if status != 0 || !strings.Contains(stdout, "Updated bot to cchub:ada/bot") {
		t.Fatalf("update: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	t.Setenv("CLAI_CLOUD", "off")
	status, stdout, stderr = runRemote(t, "-r -p bot q offline")
	if status != 0 || stdout != "offline\n" {
		t.Fatalf("run installed: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if !strings.Contains(env.savedConversation(t), "You are bot two.") {
		t.Fatalf("the updated profile did not run version 2")
	}
	status, stdout, _ = runRemote(t, "profiles")
	if status != 0 || !strings.Contains(stdout, "Source: cchub:ada/bot#sha256="+digestOf(remoteBotV2)) {
		t.Fatalf("list: status %d stdout %q", status, stdout)
	}
}

func Test_REMOTE_githubAndHTTPSTargets(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.serveFile("/gh/ada/agents/v1/agent.json", "text/plain; charset=utf-8", remoteBotV1)
	env.hub.serveFile("/gh/ada/agents/HEAD/review.json", "text/plain; charset=utf-8", remoteBotV2)
	env.hub.serveFile("/files/ops.json", "application/json", remoteBotV2)

	for _, tc := range []struct{ target, body, note string }{
		{"gh:ada/agents@v1", remoteBotV1, ""},
		{"gh:ada/agents/review.json", remoteBotV2, "follows the default branch"},
		{env.hub.srv.URL + "/files/ops.json", remoteBotV2, ""},
	} {
		t.Run(tc.target, func(t *testing.T) {
			status, stdout, stderr := runRemote(t, "-r -prp "+tc.target+"#sha256="+digestOf(tc.body)+" q hi")
			if status != 0 || stdout != "hi\n" {
				t.Fatalf("status %d stdout %q stderr %q", status, stdout, stderr)
			}
			if tc.note != "" && !strings.Contains(stderr, tc.note) {
				t.Fatalf("stderr %q lacks %q", stderr, tc.note)
			}
		})
	}
}

func Test_REMOTE_refusals(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.serveFile("/files/page.html", "text/html", "<html>")
	env.hub.serveFile("/files/typo.json", "application/json", `{"modle":"test"}`)
	env.hub.serveFile("/files/envfile.json", "application/json",
		`{"model":"test","mcp_servers":{"crm":{"command":"crm","envfile":"crm.env"}}}`)
	base := env.hub.srv.URL

	for _, tc := range []struct {
		target string
		status int
		want   string
	}{
		{"cchub:ada/missing", 1, "cchub:ada/missing: not found"},
		{"cchub:ada/bot@9", 1, "cchub:ada/bot@9: not found"},
		{base + "/files/page.html", remote.ExitRefused, "not application/json"},
		{base + "/files/typo.json", remote.ExitRefused, `did you mean "model"`},
		{base + "/files/envfile.json", remote.ExitRefused, "a relative path has no meaning"},
		{"http://example.com/a.json", 1, "only https:// is fetched"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			status, stdout, stderr := runRemote(t, "-r -prp "+tc.target+" q hi")
			if status != tc.status || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("status %d stdout %q stderr %q, want %d and %q", status, stdout, stderr, tc.status, tc.want)
			}
		})
	}
	if env.trusted(t) != "" {
		t.Fatalf("a refusal recorded trust: %s", env.trusted(t))
	}
}
