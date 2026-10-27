package remote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tradeBot = `{"model":"test","tools":["date"],"mcp_servers":{"poly":{"command":"pnpx","args":["@iqai/mcp-polymarket"],"envfile":"~/.secrets.env"}},"prompt":"Trade."}`

type hubServer struct {
	srv      *httptest.Server
	hits     atomic.Int32
	versions map[string]string
	ctype    string
}

func newHub(t *testing.T, versions map[string]string) *hubServer {
	t.Helper()
	h := &hubServer{versions: versions, ctype: "application/json; charset=utf-8"}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		body, ok := h.versions[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", h.ctype)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func testResolver(t *testing.T, h *hubServer, prompt Prompter) Resolver {
	t.Helper()
	r, err := NewResolver(Env{CloudURL: h.srv.URL}, t.TempDir(), t.TempDir(), h.srv.Client(), prompt, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	r.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return r
}

func yes(context.Context, TrustPrompt) (bool, error) { return true, nil }

func TestResolve_asksOnceThenRunsFromCache(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/v/1/raw": tradeBot})
	var asked []TrustPrompt
	r := testResolver(t, h, func(_ context.Context, p TrustPrompt) (bool, error) {
		asked = append(asked, p)
		return true, nil
	})
	got, err := r.Resolve(t.Context(), "cchub:ada/bot@1", ConsentAsk)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Fetched || got.Digest != Digest([]byte(tradeBot)) {
		t.Fatalf("Resolved = %+v", got)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil || string(b) != tradeBot {
		t.Fatalf("cached file = %q, %v", b, err)
	}
	if len(asked) != 1 || asked[0].Target != "cchub:ada/bot@1" || !strings.HasSuffix(asked[0].Page, "/hub/ada/bot/") {
		t.Fatalf("asked = %+v", asked)
	}
	if s := asked[0].Summary.String(); !strings.Contains(s, "  poly   pnpx @iqai/mcp-polymarket   ! not pinned\n") || !strings.Contains(s, "▸ reads · ~/.secrets.env\n") {
		t.Fatalf("summary = %q", s)
	}

	r.Prompt = nil
	again, err := r.Resolve(t.Context(), "cchub:ada/bot@1", ConsentAsk)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if again.Fetched || h.hits.Load() != 1 || len(asked) != 1 {
		t.Fatalf("second resolve fetched=%v hits=%d asked=%d, want cache only", again.Fetched, h.hits.Load(), len(asked))
	}
}

func TestResolve_refusesUntrustedWithoutATerminal(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/raw": tradeBot})
	r := testResolver(t, h, nil)
	_, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk)
	var untrusted *UntrustedError
	if !errors.As(err, &untrusted) || untrusted.ExitCode() != ExitRefused {
		t.Fatalf("err = %v, want *UntrustedError", err)
	}
	for _, want := range []string{"cchub:ada/bot is a profile", "clai profiles trust cchub:ada/bot", "cchub:ada/bot#sha256=" + Digest([]byte(tradeBot))} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if _, err := os.Stat(r.Cache.Path(Digest([]byte(tradeBot)))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untrusted bytes were cached: %v", err)
	}
}

func TestResolve_unpinnedTargetThatChangedAsksAgain(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/raw": tradeBot})
	r := testResolver(t, h, yes)
	if _, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk); err != nil {
		t.Fatalf("first: %v", err)
	}
	h.versions["/hub/ada/bot/raw"] = `{"model":"test","prompt":"v2"}`
	r.Prompt = nil
	_, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk)
	if err == nil || !strings.Contains(err.Error(), "changed to a version") {
		t.Fatalf("err = %v, want a changed-version refusal", err)
	}
	r.Prompt = func(context.Context, TrustPrompt) (bool, error) { return false, nil }
	_, err = r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk)
	var untrusted *UntrustedError
	if !errors.As(err, &untrusted) || !untrusted.Asked || !strings.Contains(err.Error(), "was not trusted") {
		t.Fatalf("declined: err = %v", err)
	}
	r.Prompt = func(context.Context, TrustPrompt) (bool, error) { return false, errors.New("tty gone") }
	if _, err = r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk); err == nil || !strings.Contains(err.Error(), "tty gone") {
		t.Fatalf("prompt failure: err = %v", err)
	}
}

func TestResolve_hashIsConsent(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/v/1/raw": tradeBot})
	r := testResolver(t, h, nil)
	digest := Digest([]byte(tradeBot))
	if _, err := r.Resolve(t.Context(), "cchub:ada/bot@1#sha256="+digest, ConsentAsk); err != nil {
		t.Fatalf("hash consent: %v", err)
	}
	if ok, err := r.Trust.Trusted("cchub:ada/bot", digest); err != nil || !ok {
		t.Fatalf("trusted = %v, %v", ok, err)
	}
	wrong := strings.Repeat("0", 64)
	_, err := r.Resolve(t.Context(), "cchub:ada/bot@1#sha256="+wrong, ConsentAsk)
	var mismatch *HashMismatchError
	if !errors.As(err, &mismatch) || mismatch.ExitCode() != ExitRefused || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want *HashMismatchError", err)
	}
}

func TestResolve_consentGivenTrustsWithoutAsking(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/raw": tradeBot})
	r := testResolver(t, h, nil)
	if _, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentGiven); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk); err != nil {
		t.Fatalf("trusted resolve: %v", err)
	}
}

func TestResolve_offlineIsCacheOnly(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bot/raw": tradeBot})
	r := testResolver(t, h, yes)
	if _, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk); err != nil {
		t.Fatalf("online: %v", err)
	}
	r.Offline = true
	got, err := r.Resolve(t.Context(), "cchub:ada/bot", ConsentAsk)
	if err != nil || got.Fetched || h.hits.Load() != 1 {
		t.Fatalf("offline cached: %+v %v hits=%d", got, err, h.hits.Load())
	}
	_, err = r.Resolve(t.Context(), "cchub:ada/other", ConsentAsk)
	var offline *OfflineError
	if !errors.As(err, &offline) || offline.ExitCode() != ExitRefused || !strings.Contains(err.Error(), "CLAI_CLOUD=off") {
		t.Fatalf("err = %v, want *OfflineError", err)
	}
}

func TestResolve_refusals(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/bad/raw": `{"promt":"x"}`, "/page": "<html></html>"})
	r := testResolver(t, h, yes)
	var invalid *InvalidProfileError
	if _, err := r.Resolve(t.Context(), "cchub:ada/bad", ConsentAsk); !errors.As(err, &invalid) {
		t.Fatalf("invalid: err = %v", err)
	}
	var fetchErr *FetchError
	if _, err := r.Resolve(t.Context(), "cchub:ada/missing", ConsentAsk); !errors.As(err, &fetchErr) || fetchErr.Status != http.StatusNotFound || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing: err = %v", err)
	}
	h.ctype = "text/html"
	var notJSON *NotJSONError
	if _, err := r.Resolve(t.Context(), h.srv.URL+"/page", ConsentAsk); !errors.As(err, &notJSON) || notJSON.ExitCode() != ExitRefused || !strings.Contains(err.Error(), "answered text/html") {
		t.Fatalf("html: err = %v", err)
	}
	if _, err := r.Resolve(t.Context(), "cchub:Bad", ConsentAsk); err == nil {
		t.Fatal("bad target: want error")
	}
}

func TestResolve_ghFollowingADefaultBranchIsNoted(t *testing.T) {
	var notes []string
	r := Resolver{
		Client: doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://raw.githubusercontent.com/ada/agents/HEAD/agent.json" {
				t.Fatalf("fetched %s", req.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}}, Body: bodyOf(tradeBot)}, nil
		}),
		HubBase: DefaultHubBase,
		Trust:   NewTrustStore(t.TempDir()),
		Cache:   NewCache(t.TempDir()),
		Prompt:  yes,
		Note:    func(s string) { notes = append(notes, s) },
	}
	if _, err := r.Resolve(t.Context(), "gh:ada/agents", ConsentAsk); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "follows the default branch") {
		t.Fatalf("notes = %q", notes)
	}
}

func TestCheckRedirect(t *testing.T) {
	plain, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := CheckRedirect(plain, nil); !errors.Is(err, ErrInsecureRedirect) {
		t.Fatalf("http redirect: %v", err)
	}
	secure, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := CheckRedirect(secure, make([]*http.Request, 4)); err != nil {
		t.Fatalf("https redirect: %v", err)
	}
	if err := CheckRedirect(secure, make([]*http.Request, 5)); err == nil {
		t.Fatal("sixth redirect: want error")
	}
	if NewClient().CheckRedirect == nil {
		t.Fatal("NewClient has no redirect policy")
	}
}

func TestStores_reportCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TrustFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTrustStore(dir).Trusted("x", "y"); err == nil {
		t.Fatal("corrupt trust store: want error")
	}
	if err := NewTrustStore(dir).Trust("x", "y", time.Now()); err == nil {
		t.Fatal("corrupt trust store write: want error")
	}
	c := NewCache(dir)
	if err := os.MkdirAll(filepath.Join(dir, CacheSubdir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.indexPath(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Lookup("x"); err == nil || errors.Is(err, ErrNotCached) {
		t.Fatalf("corrupt index: err = %v", err)
	}
	if _, err := c.Store("x", []byte("{}")); err == nil {
		t.Fatal("corrupt index store: want error")
	}
}

func TestCache_dropsBytesThatNoLongerMatchTheirHash(t *testing.T) {
	c := NewCache(t.TempDir())
	digest, err := c.Store("cchub:a1/b@1", []byte(tradeBot))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := os.WriteFile(c.Path(digest), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Lookup("cchub:a1/b@1"); !errors.Is(err, ErrNotCached) {
		t.Fatalf("Lookup tampered: err = %v, want ErrNotCached", err)
	}
}

func TestEnv(t *testing.T) {
	env := EnvFrom(func(k string) string {
		return map[string]string{"CLAI_CLOUD_URL": "https://dev.clai.chat/some/path", "CLAI_CLOUD": "OFF"}[k]
	})
	base, err := env.HubBase()
	if err != nil || base != "https://dev.clai.chat" || !env.Offline() {
		t.Fatalf("env = %q %v offline=%v", base, err, env.Offline())
	}
	if base, _ := (Env{}).HubBase(); base != DefaultHubBase {
		t.Fatalf("default base = %q", base)
	}
	for _, bad := range []string{"http://dev.clai.chat", "dev.clai.chat", "https://"} {
		if _, err := (Env{CloudURL: bad}).HubBase(); err == nil {
			t.Fatalf("HubBase(%q): want error", bad)
		}
		if _, err := NewResolver(Env{CloudURL: bad}, "", "", nil, nil, nil); err == nil {
			t.Fatalf("NewResolver(%q): want error", bad)
		}
	}
}

func TestSummary_withoutServers(t *testing.T) {
	s, err := Summarize([]byte(`{"name":"x"}`), Checked{Ignored: []string{"description"}})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	got := s.String()
	for _, want := range []string{"no MCP servers", "▸ model · your default", "▸ not read by clai · description"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q lacks %q", got, want)
		}
	}
	if !unpinned("npx", []string{"-y", "pkg"}) || unpinned("npx", []string{"-y", "@scope/pkg@1"}) || unpinned("node", []string{"x"}) || unpinned("npx", []string{"-y"}) ||
		unpinned("uvx", []string{"duckduckgo-mcp-server==0.7.0"}) || !unpinned("uvx", []string{"duckduckgo-mcp-server"}) {
		t.Fatal("unpinned")
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func bodyOf(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
