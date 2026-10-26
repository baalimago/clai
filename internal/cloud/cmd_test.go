package cloud

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type harness struct {
	f        *fakeServer
	conf     string
	cache    string
	out      bytes.Buffer
	deps     CommandDeps
	sleeps   []time.Duration
	spawned  []string
	clockNow time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("CLAI_CLOUD", "")
	t.Setenv("CLAI_CLOUD_TOKEN", "")
	t.Setenv("CLAI_CLOUD_URL", "")
	h := &harness{f: newFakeServer(t), conf: t.TempDir(), cache: t.TempDir(), clockNow: testNow}
	h.deps = CommandDeps{
		ConfDir:  func() (string, error) { return h.conf, nil },
		CacheDir: func() (string, error) { return h.cache, nil },
		Out:      &h.out,
		Now:      func() time.Time { return h.clockNow },
		Sleep: func(_ context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			h.clockNow = h.clockNow.Add(d)
			return nil
		},
		Hostname: func() (string, error) { return "laptop", nil },
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) int {
	t.Helper()
	return cmd.Run(context.Background(), append([]string{"clai", "cloud"}, args...), map[string]cmd.Command{"cloud": Command(h.deps)}, "usage")
}

func (h *harness) login(t *testing.T) {
	t.Helper()
	if err := saveCredentials(h.conf, Credentials{URL: h.f.srv.URL, Token: "tok-a", Device: "a"}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginApprovesThenSyncsConfig(t *testing.T) {
	h := newHarness(t)
	h.f.pollsLeft = 2
	h.f.put(StoreConfig, "theme.json", `{"dark":true}`)
	writeFile(t, h.conf, "cloud/state/config.json", `{"cursor":99}`)
	if code := h.run(t, "login", "-url", h.f.srv.URL); code != 0 {
		t.Fatalf("exit %d: %s", code, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{"ABCD-EFGH", `"laptop"`, "Logged in as u-12345678", "config: 1 pulled"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(h.sleeps) != 3 || h.sleeps[0] != 5*time.Second {
		t.Fatalf("sleeps %v", h.sleeps)
	}
	info, err := os.Stat(credentialsPath(h.conf))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials %v %v", info, err)
	}
	creds, err := LoadCredentials(h.conf)
	if err != nil || creds.Token != "tok-new" || creds.Device != "laptop" || creds.Account != "u-12345678" {
		t.Fatalf("creds %+v %v", creds, err)
	}
	if got, _ := readFile(t, h.conf, "theme.json"); got != `{"dark":true}` {
		t.Fatalf("theme %q", got)
	}
}

func TestLoginRefusedOrExpired(t *testing.T) {
	h := newHarness(t)
	h.f.denyLogin = true
	err := runLogin(context.Background(), h.deps, h.f.srv.URL, "")
	var le *LoginError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err %v", err)
	}
	h = newHarness(t)
	h.f.pollsLeft = 1_000
	err = runLogin(context.Background(), h.deps, h.f.srv.URL, "box")
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err %v", err)
	}
	if h.f.deviceName != "box" {
		t.Fatalf("name %q", h.f.deviceName)
	}
}

func TestLogoutRevokesAndForgets(t *testing.T) {
	h := newHarness(t)
	if err := runLogout(context.Background(), h.deps); err != nil || !strings.Contains(h.out.String(), "Not logged in") {
		t.Fatalf("%v %s", err, h.out.String())
	}
	h.login(t)
	writeFile(t, h.conf, "cloud/state/config.json", `{}`)
	if err := runLogout(context.Background(), h.deps); err != nil {
		t.Fatal(err)
	}
	if !h.f.revoked {
		t.Fatal("token not revoked")
	}
	if _, err := os.Stat(filepath.Join(h.conf, "cloud", "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state kept")
	}
	var notIn *NotLoggedInError
	if _, err := LoadCredentials(h.conf); !errors.As(err, &notIn) {
		t.Fatalf("still logged in: %v", err)
	}
	// A token the server already forgot still logs out.
	h.login(t)
	h.f.mu.Lock()
	delete(h.f.tokens, "tok-a")
	h.f.mu.Unlock()
	if err := runLogout(context.Background(), h.deps); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAndToggles(t *testing.T) {
	h := newHarness(t)
	var notIn *NotLoggedInError
	if err := runStatus(context.Background(), h.deps); !errors.As(err, &notIn) {
		t.Fatalf("err %v", err)
	}
	if code := h.run(t, "enable", "conversations"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.out.String(), "clai cloud login") {
		t.Fatalf("out %s", h.out.String())
	}
	h.login(t)
	h.f.subscribed = false
	h.out.Reset()
	if err := runToggle(context.Background(), h.deps, []string{"conversations"}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "/account/#conversations") {
		t.Fatalf("out %s", h.out.String())
	}
	h.out.Reset()
	if err := runStatus(context.Background(), h.deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"u-12345678", "config:        sync on", "conversations: sync on", "subscribe on your clai.chat account page", "Last sync: never"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, h.out.String())
		}
	}
	if err := runToggle(context.Background(), h.deps, []string{"config"}, false); err != nil {
		t.Fatal(err)
	}
	s, _ := LoadSettings(h.conf)
	if s.Enabled(StoreConfig) || !s.Enabled(StoreConversations) {
		t.Fatalf("settings %+v", s)
	}
	var usage *UsageError
	if err := runToggle(context.Background(), h.deps, nil, true); !errors.As(err, &usage) {
		t.Fatalf("err %v", err)
	}
	if err := runToggle(context.Background(), h.deps, []string{"notes"}, true); !errors.As(err, &usage) {
		t.Fatalf("err %v", err)
	}
}

func TestSubscriptionLine(t *testing.T) {
	var me Me
	me.Billing = true
	for kind, want := range map[string]string{"active": "active", "grace": "payment failed", "lapsed": "read-only until", "none": "subscribe"} {
		me.Conversations.Kind = kind
		if got := subscriptionLine(me, testNow); !strings.Contains(got, want) {
			t.Errorf("%s: %q", kind, got)
		}
	}
	me.Conversations.Kind, me.Conversations.RenewsAt = "active", testNow.UnixMilli()
	if got := subscriptionLine(me, testNow); !strings.Contains(got, "renews") {
		t.Errorf("renews: %q", got)
	}
	me.Conversations.Kind, me.Billing = "none", false
	if got := subscriptionLine(me, testNow); !strings.Contains(got, "not open") {
		t.Errorf("closed: %q", got)
	}
}

func TestSyncCommand(t *testing.T) {
	h := newHarness(t)
	if err := runSync(context.Background(), h.deps, nil, true); err != nil {
		t.Fatalf("a background sync with no login must stay quiet: %v", err)
	}
	h.login(t)
	writeFile(t, h.conf, "profiles/a.json", "{}")
	if code := h.run(t, "sync"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.out.String())
	}
	if !strings.Contains(h.out.String(), "config: 1 uploaded") {
		t.Fatalf("out %s", h.out.String())
	}
	h.out.Reset()
	h.f.subscribed = false
	err := runSync(context.Background(), h.deps, []string{"config", "conversations"}, false)
	if err == nil || !strings.Contains(h.out.String(), "config: up to date") || !strings.Contains(h.out.String(), "needs a subscription") {
		t.Fatalf("err %v, out %s", err, h.out.String())
	}
	var usage *UsageError
	if err := runSync(context.Background(), h.deps, []string{"notes"}, false); !errors.As(err, &usage) {
		t.Fatalf("err %v", err)
	}
	if LastSync(h.cache).IsZero() {
		t.Fatal("last sync not recorded")
	}
}

func TestSyncLock(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	release, err := acquire(h.cache, testNow)
	if err != nil {
		t.Fatal(err)
	}
	var locked *LockedError
	if err := runSync(context.Background(), h.deps, nil, false); !errors.As(err, &locked) {
		t.Fatalf("err %v", err)
	}
	if err := runSync(context.Background(), h.deps, nil, true); err != nil {
		t.Fatalf("background: %v", err)
	}
	release()
	// The blocked runs left the dirty marker; a background sync takes it
	// and goes round once more.
	writeFile(t, h.conf, "theme.json", "{}")
	if err := runSync(context.Background(), h.deps, nil, true); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(h.out.String(), "config: "); n != 2 {
		t.Fatalf("rounds %d:\n%s", n, h.out.String())
	}
	if takeDirty(h.cache) {
		t.Fatal("dirty marker left")
	}
	// A lock older than staleLock belongs to a dead sync.
	if _, err := acquire(h.cache, testNow); err != nil {
		t.Fatal(err)
	}
	old := testNow.Add(-time.Hour)
	_ = os.Chtimes(lockPath(h.cache), old, old)
	if _, err := acquire(h.cache, testNow); err != nil {
		t.Fatalf("stale lock: %v", err)
	}
}

func TestAutoSync(t *testing.T) {
	h := newHarness(t)
	spawn := func(log string) error { h.spawned = append(h.spawned, log); return nil }
	auto := func(args ...string) bool { return AutoSync(h.conf, h.cache, args, h.clockNow, spawn) }
	if auto("q", "hi") {
		t.Fatal("spawned without a login")
	}
	h.login(t)
	if auto("cloud", "status") || auto() {
		t.Fatal("spawned after the cloud command")
	}
	if !auto("q", "hi") || len(h.spawned) != 1 || h.spawned[0] != LogPath(h.cache) {
		t.Fatalf("spawned %v", h.spawned)
	}
	if auto("q", "again") {
		t.Fatal("spawned inside the interval")
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	if !auto("q", "later") {
		t.Fatal("not spawned after the interval")
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	t.Setenv("CLAI_CLOUD", "off")
	if auto("q", "off") {
		t.Fatal("spawned with CLAI_CLOUD=off")
	}
	t.Setenv("CLAI_CLOUD", "")
	if err := saveSettings(h.conf, Settings{Stores: map[Store]bool{}}); err != nil {
		t.Fatal(err)
	}
	if auto("q", "none enabled") {
		t.Fatal("spawned with no store enabled")
	}
}

func TestCredentialsFromEnv(t *testing.T) {
	h := newHarness(t)
	t.Setenv("CLAI_CLOUD_TOKEN", "tok-env")
	t.Setenv("CLAI_CLOUD_URL", "https://example.test")
	c, err := LoadCredentials(h.conf)
	if err != nil || c.Token != "tok-env" || c.URL != "https://example.test" {
		t.Fatalf("%+v %v", c, err)
	}
	writeFile(t, h.conf, "cloud/credentials.json", "{")
	if _, err := LoadCredentials(h.conf); err == nil {
		t.Fatal("a torn credentials file read fine")
	}
	writeFile(t, h.conf, "cloud/settings.json", "{")
	if _, err := LoadSettings(h.conf); err == nil {
		t.Fatal("a torn settings file read fine")
	}
	writeFile(t, h.conf, "cloud/settings.json", `{"min-interval-seconds":-1}`)
	if s, err := LoadSettings(h.conf); err != nil || s.MinIntervalSeconds != DefaultMinInterval || s.Stores == nil {
		t.Fatalf("%+v %v", s, err)
	}
	writeFile(t, h.conf, "cloud/state/config.json", "{")
	if st, err := loadState(h.conf, StoreConfig); err != nil || st.Cursor != 0 {
		t.Fatalf("torn state: %v", err)
	}
}

func TestHelpAndBareCommand(t *testing.T) {
	h := newHarness(t)
	if code := h.run(t); code != 0 || !strings.Contains(h.out.String(), "Subcommands:") {
		t.Fatalf("exit %d: %s", code, h.out.String())
	}
	if humanBytes(2_500_000_000) != "2.5 GB" || humanBytes(1_500_000) != "1.5 MB" || orDash("") != "-" {
		t.Fatal("formatting")
	}
}
