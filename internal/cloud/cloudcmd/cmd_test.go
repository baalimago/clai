package cloudcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	"github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

func TestMain(m *testing.M) { testenv.MainInHome(m) }

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type harness struct {
	t       *testing.T
	srv     *cloudtest.Server
	confDir string
	out     *bytes.Buffer
	in      string
	tty     bool
	exit    int
}

func newHarness(t *testing.T, token string) *harness {
	t.Helper()
	h := &harness{t: t, srv: cloudtest.New(t), confDir: t.TempDir(), out: &bytes.Buffer{}}
	if token != "" {
		if err := cloud.SaveCredentials(h.confDir, cloud.Credentials{URL: h.srv.URL(), Token: token, Device: "laptop"}); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *harness) deps() Deps {
	return Deps{
		ConfDir:     func() (string, error) { return h.confDir, nil },
		Out:         h.out,
		In:          strings.NewReader(h.in),
		Interactive: func() bool { return h.tty },
		Now:         func() time.Time { return testNow },
		Sleep:       func(context.Context, time.Duration) error { return nil },
		Hostname:    func() (string, error) { return "laptop", nil },
		SetExit:     func(c int) { h.exit = c },
	}
}

// run dispatches like cli.Run and answers the exit status and the output.
func (h *harness) run(args ...string) (int, string) {
	h.t.Helper()
	h.out.Reset()
	h.exit = 0
	code := cmd.Run(context.Background(), append([]string{"clai", "cloud"}, args...), map[string]cmd.Command{"cloud": Command(h.deps())}, "usage")
	if code == 0 && h.exit != 0 {
		code = h.exit
	}
	return code, h.out.String()
}

func (h *harness) write(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.confDir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(h.confDir, filepath.FromSlash(rel)))
	return string(b)
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestHelp(t *testing.T) {
	h := newHarness(t, "")
	code, out := h.run()
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	mustContain(t, out, "Nothing syncs on its own", "commit [-m msg]", "Exit 4")
}

func TestLogin_savesTheKeyAndSyncsNothing(t *testing.T) {
	h := newHarness(t, "")
	h.srv.Put("config", "profiles/x.json", "remote")
	code, out := h.run("login", "-url", h.srv.URL())
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	mustContain(t, out, "BCDF-GHJK", "/cli/login#", "Logged in as @lorentz", "Nothing is synced")
	creds, err := cloud.LoadCredentials(h.confDir)
	if err != nil || creds.Token != "tok-new" || creds.Device != "laptop" || creds.Access != "write" {
		t.Fatalf("creds = %+v, %v", creds, err)
	}
	if _, err := os.Stat(filepath.Join(h.confDir, "profiles", "x.json")); err == nil {
		t.Fatal("login pulled a file")
	}
	h.srv.Deny = true
	if code, _ := h.run("login", "-name", "box"); code != 1 {
		t.Fatalf("denied login code %d", code)
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/x.json", "1")
	if code, out := h.run("commit"); code != 0 {
		t.Fatalf("commit: %d %s", code, out)
	}
	code, out := h.run("logout")
	if code != 0 || !h.srv.Revoked {
		t.Fatalf("logout: %d %s", code, out)
	}
	mustContain(t, out, "Logged out")
	if _, err := os.Stat(filepath.Join(cloud.Dir(h.confDir), "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sync state kept after logout")
	}
	if h.read("profiles/x.json") != "1" {
		t.Fatal("logout touched local files")
	}
	_, out = h.run("logout")
	mustContain(t, out, "Not logged in")
}

func TestLogout_revokedTokenStillLogsOut(t *testing.T) {
	h := newHarness(t, "gone")
	if code, out := h.run("logout"); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	h2 := newHarness(t, "tok-a")
	h2.srv.Close()
	if code, _ := h2.run("logout"); code != 1 {
		t.Fatal("logout offline should fail and keep the login")
	}
	if _, err := cloud.LoadCredentials(h2.confDir); err != nil {
		t.Fatal("login forgotten after a failed revoke")
	}
}

func TestWhoami(t *testing.T) {
	h := newHarness(t, "tok-a")
	code, out := h.run("whoami")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	mustContain(t, out, "@lorentz", "Device:  a", "Access:  write", "config:", "conversations:",
		"Billing: config sync is free (0.0 kB of 1.0 GB)", "conversations need a card on file")
	_, out = h.run("whoami", "-r")
	var me cloud.Me
	if err := json.Unmarshal([]byte(out), &me); err != nil || me.Handle != "lorentz" {
		t.Fatalf("raw = %s, %v", out, err)
	}
	if code, _ := newHarness(t, "revoked").run("whoami"); code != 1 {
		t.Fatal("a revoked token should fail")
	}
	if code, _ := newHarness(t, "").run("whoami"); code != 1 {
		t.Fatal("no login should fail")
	}
}

func TestStatusCommitPullLog(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/coder.json", `{"prompt":"x"}`)
	h.write("profiles/leak.json", `{"api_key":"abcdefgh1234"}`)
	h.write("conversations/imported/claude-code/s1.json", `{}`)
	code, out := h.run("status")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, out, "+ profiles/coder.json", "holds a secret", "profiles/leak.json  api_key", "never pulled", "1 imported file")

	code, out = h.run("commit", "-m", "first", "config")
	if code != 0 {
		t.Fatalf("commit %d: %s", code, out)
	}
	mustContain(t, out, "config commit #1 · 1 file", "not committed, holds a secret")
	if got, _ := h.srv.Content("config", "profiles/coder.json"); got == "" {
		t.Fatal("not on the server")
	}
	_, out = h.run("commit")
	mustContain(t, out, "nothing to commit")

	h.srv.Put("config", "profiles/coder.json", `{"prompt":"y"}`)
	_, out = h.run("status", "config")
	mustContain(t, out, "server is 1 commit ahead")
	code, out = h.run("pull", "config")
	if code != 0 || h.read("profiles/coder.json") != `{"prompt":"y"}` {
		t.Fatalf("pull %d: %s", code, out)
	}
	mustContain(t, out, "Pulled config #2 · 1 updated")

	code, out = h.run("log", "config", "-limit", "5")
	if code != 0 {
		t.Fatalf("log %d", code)
	}
	mustContain(t, out, `#2`, `"edit profiles/coder.json"`, `#1`, `"first"`, "a ·")
	_, out = h.run("log", "conversations")
	mustContain(t, out, "no commits yet")
	_, out = h.run("log", "-r")
	var all map[string][]sync.LogEntry
	if err := json.Unmarshal([]byte(out), &all); err != nil || len(all["config"]) != 2 {
		t.Fatalf("raw log = %s", out)
	}
	_, out = h.run("status", "-r")
	var st []sync.Status
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st) != 2 {
		t.Fatalf("raw status = %s", out)
	}
	if code, _ := h.run("status", "notes"); code != 1 {
		t.Fatal("unknown store accepted")
	}
	if code, _ := h.run("status", "config", "conversations"); code != 1 {
		t.Fatal("two stores accepted")
	}
}

func TestCommit_behindExits4(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/x.json", "1")
	h.run("commit")
	h.srv.Put("config", "profiles/x.json", "theirs")
	h.write("profiles/x.json", "mine")
	code, out := h.run("commit", "-m", "x")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	mustContain(t, out, "Not committed. The server changed 1 of your config files", "profiles/x.json   #2 \"edit profiles/x.json\", other", "Run clai cloud pull")
}

func TestPull_conflictThenResolve(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/x.json", "base\n")
	h.run("commit")
	h.srv.Put("config", "profiles/x.json", "theirs\n")
	h.write("profiles/x.json", "mine\n")
	code, out := h.run("pull")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	mustContain(t, out, "1 conflict, your file is untouched", "C profiles/x.json", "cloud/theirs/profiles/x.json", "clai cloud resolve")
	_, out = h.run("status", "config")
	mustContain(t, out, "C profiles/x.json")
	_, out = h.run("diff")
	mustContain(t, out, "profiles/x.json  conflict", "+mine", "+theirs")
	code, out = h.run("commit")
	if code != ExitDecisionNeeded {
		t.Fatalf("commit with conflict: %d %s", code, out)
	}
	mustContain(t, out, "1 conflict needs resolving")
	if code, _ := h.run("resolve"); code != 1 {
		t.Fatal("resolve without a path")
	}
	if code, _ := h.run("resolve", "profiles/x.json", "-mine", "-theirs"); code != 1 {
		t.Fatal("both flags accepted")
	}
	code, out = h.run("resolve", "profiles/x.json", "-theirs")
	if code != 0 || h.read("profiles/x.json") != "theirs\n" {
		t.Fatalf("resolve %d: %s", code, out)
	}
	mustContain(t, out, "with the server's file")
	if code, _ := h.run("resolve", "profiles/x.json"); code != 1 {
		t.Fatal("resolving twice should fail")
	}
}

func TestResolve_mineMessages(t *testing.T) {
	for _, flag := range []string{"-mine", ""} {
		h := newHarness(t, "tok-a")
		h.write("profiles/x.json", "base")
		h.run("commit")
		h.srv.Put("config", "profiles/x.json", "theirs")
		h.write("profiles/x.json", "mine")
		h.run("pull")
		args := []string{"resolve", "profiles/x.json"}
		if flag != "" {
			args = append(args, flag)
		}
		code, out := h.run(args...)
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		mustContain(t, out, "Commit to share it")
	}
}

func TestPull_firstPull(t *testing.T) {
	setup := func(t *testing.T) *harness {
		h := newHarness(t, "tok-a")
		h.srv.Put("config", "profiles/x.json", "theirs")
		h.write("profiles/x.json", "mine")
		return h
	}
	t.Run("no terminal exits 4", func(t *testing.T) {
		h := setup(t)
		code, out := h.run("pull", "config")
		if code != ExitDecisionNeeded || h.read("profiles/x.json") != "mine" {
			t.Fatalf("code %d: %s", code, out)
		}
		mustContain(t, out, "first config pull", "1 differ: profiles/x.json", "No answer: pull again with -theirs or -mine")
	})
	t.Run("flag", func(t *testing.T) {
		h := setup(t)
		code, out := h.run("pull", "config", "-theirs")
		if code != 0 || h.read("profiles/x.json") != "theirs" {
			t.Fatalf("code %d: %s", code, out)
		}
		mustContain(t, out, "1 replaced", "cloud/trash/")
		if code, _ := h.run("pull", "-theirs", "-mine"); code != 1 {
			t.Fatal("both flags accepted")
		}
	})
	t.Run("terminal answers", func(t *testing.T) {
		for answer, want := range map[string]string{"m\n": "mine", "t\n": "theirs", "c\n": "mine"} {
			h := setup(t)
			h.tty, h.in = true, answer
			code, out := h.run("pull", "config")
			if h.read("profiles/x.json") != want {
				t.Fatalf("%q: file %q, %d %s", answer, h.read("profiles/x.json"), code, out)
			}
			mustContain(t, out, "[t] my account's copy")
		}
	})
	t.Run("terminal quits", func(t *testing.T) {
		for answer, want := range map[string]int{"q\n": 1, "": ExitDecisionNeeded} {
			h := setup(t)
			h.tty, h.in = true, answer
			if code, _ := h.run("pull", "config"); code != want || h.read("profiles/x.json") != "mine" {
				t.Fatalf("%q: code %d", answer, code)
			}
		}
	})
	t.Run("raw", func(t *testing.T) {
		h := setup(t)
		code, out := h.run("pull", "config", "-r")
		var res []sync.PullResult
		if code != ExitDecisionNeeded || json.Unmarshal([]byte(out), &res) != nil || res[0].First == nil {
			t.Fatalf("code %d: %s", code, out)
		}
	})
}

func TestPull_forksAndMerges(t *testing.T) {
	h := newHarness(t, "tok-a")
	chat := func(msgs ...string) string { return `{"messages":[` + strings.Join(msgs, ",") + `]}` }
	h.write("conversations/c1.json", chat(`{"role":"user","content":"hi"}`))
	h.write("conversations/c2.json", chat(`{"role":"user","content":"yo"}`))
	h.run("commit")
	h.run("pull")
	h.srv.Put("conversations", "conversations/c1.json", chat(`{"role":"user","content":"hi"}`, `{"a":1}`))
	h.srv.Put("conversations", "conversations/c2.json", chat(`{"role":"user","content":"yo"}`, `{"a":2}`))
	h.write("conversations/c1.json", chat(`{"role":"user","content":"hi"}`, `{"b":1}`))
	h.write("conversations/c2.json", chat(`{"role":"user","content":"yo"}`)+" ")
	code, out := h.run("pull", "conversations")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	mustContain(t, out, "1 merged", "1 fork: conversations/c1.json", `"hi (other)"`)
}

func TestCommit_pathsAndErrors(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/a.json", "a")
	h.write("mcpServers/b.json", "b")
	code, out := h.run("commit", "profiles/")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if _, ok := h.srv.Content("config", "mcpServers/b.json"); ok {
		t.Fatal("commit profiles/ sent mcpServers")
	}
	if code, _ := h.run("commit", "notes.txt"); code != 1 {
		t.Fatal("an unsynced path accepted")
	}
	h.write("conversations/c.json", "{}")
	if code, _ := h.run("commit", "conversations/", "-r"); code != 0 {
		t.Fatal("commit conversations/")
	}
	h.srv.Refuse = &cloudtest.Problem{Status: 402, Code: "card_required", Message: "Syncing conversations needs a card", URL: "https://clai.chat/account/billing"}
	h.write("mcpServers/b.json", "b2")
	code, _ = h.run("commit")
	if code != 1 {
		t.Fatalf("refused commit code %d", code)
	}
	r := &runner{deps: h.deps().withDefaults()}
	err := r.commit(context.Background(), nil, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "https://clai.chat/account/billing") {
		t.Fatalf("card error = %v", err)
	}
	h.srv.Refuse = &cloudtest.Problem{Status: 401, Code: "unauthorized", Message: "revoked"}
	if err := r.commit(context.Background(), nil, "", false, false); err == nil || !strings.Contains(err.Error(), "clai cloud login") {
		t.Fatalf("revoked error = %v", err)
	}
}

func TestDiff(t *testing.T) {
	h := newHarness(t, "tok-a")
	_, out := h.run("diff")
	mustContain(t, out, "No local changes")
	h.write("profiles/x.json", "one\n")
	h.run("commit")
	h.write("profiles/x.json", "two\n")
	_, out = h.run("diff", "profiles/x.json")
	mustContain(t, out, "-one", "+two")
	_, out = h.run("diff", "profiles/x.json", "-remote")
	mustContain(t, out, "theirs (the server, same as base)")
	if code, _ := h.run("diff", "notes.txt"); code != 1 {
		t.Fatal("diff of an unsynced path")
	}
}

func TestStatus_offlineAndRevoked(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/x.json", "1")
	h.srv.Close()
	code, out := h.run("status", "config")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	mustContain(t, out, "+ profiles/x.json", "server not reachable")
	if code, _ := newHarness(t, "revoked").run("status"); code != 1 {
		t.Fatal("revoked status should fail")
	}
	if code, _ := h.run("pull"); code != 1 {
		t.Fatal("offline pull should fail")
	}
	if code, _ := h.run("log"); code != 1 {
		t.Fatal("offline log should fail")
	}
}

func TestPositionals_flagsAfterArguments(t *testing.T) {
	h := newHarness(t, "tok-a")
	if code, _ := h.run("pull", "-nope"); code != 1 {
		t.Fatal("an unknown flag accepted")
	}
}

func TestHelpers(t *testing.T) {
	if human(2_500_000_000) != "2.5 GB" || human(1500) != "1.5 kB" || human(3_000_000) != "3.0 MB" {
		t.Fatal("human")
	}
	for d, want := range map[time.Duration]string{time.Second: "just now", 5 * time.Minute: "5 min ago", 3 * time.Hour: "3 h ago", 72 * time.Hour: "2026-10-23"} {
		if got := ago(testNow, testNow.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q", d, got)
		}
	}
	if quoteOr("") != "-" || hostOf("https://clai.chat/") != "clai.chat" {
		t.Fatal("quoteOr/hostOf")
	}
	d := Deps{}.withDefaults()
	if d.Out == nil || d.Now == nil || d.Interactive == nil {
		t.Fatal("defaults")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Sleep(ctx, time.Hour); err == nil {
		t.Fatal("sleep ignores cancel")
	}
	if err := d.Sleep(context.Background(), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	d.SetExit(4)
	if (&QuitError{}).Error() == "" {
		t.Fatal("QuitError")
	}
}

func TestBillingLine(t *testing.T) {
	cases := []struct {
		name string
		me   cloud.Me
		want string
	}{
		{"no card", cloud.Me{
			Usage:  cloud.Usage{ConfigLimitBytes: 1_000_000_000},
			Stores: map[string]cloud.StoreInfo{"config": {Bytes: 2_000_000}},
		}, "Billing: config sync is free (2.0 MB of 1.0 GB); conversations need a card on file, $1 per started GB a month"},
		{"card, nothing stored", cloud.Me{
			Usage: cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true},
		}, "Billing: config sync is free (0.0 kB of 1.0 GB); conversations $0 a month at 0.0 kB"},
		{"card, 1.2 GB", cloud.Me{
			Usage:  cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true},
			Stores: map[string]cloud.StoreInfo{"conversations": {Bytes: 1_200_000_000}},
		}, "Billing: config sync is free (0.0 kB of 1.0 GB); conversations $2 a month at 1.2 GB"},
		{"overdue", cloud.Me{
			Usage:  cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true, Overdue: true},
			Stores: map[string]cloud.StoreInfo{"conversations": {Bytes: 1}},
		}, "Billing: config sync is free (0.0 kB of 1.0 GB); conversations $1 a month at 0.0 kB; a payment is overdue, so commits are refused"},
	}
	for _, c := range cases {
		if got := billingLine(c.me); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
