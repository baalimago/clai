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
	mustContain(t, out, "BCDF-GHJK", "/cli/login#", "▸ logged in · @lorentz", "nothing syncs until you ask")
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
	mustContain(t, out, "▸ logged out · your local files are unchanged")
	if _, err := os.Stat(filepath.Join(cloud.Dir(h.confDir), "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sync state kept after logout")
	}
	if h.read("profiles/x.json") != "1" {
		t.Fatal("logout touched local files")
	}
	_, out = h.run("logout")
	mustContain(t, out, "▸ not logged in")
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
	unlock := h.srv.Lock()
	h.srv.CardOnFile = false
	unlock()
	code, out := h.run("whoami")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	mustContain(t, out, "▸ @lorentz", "device a", "write access", "■ config", "■ conversations",
		"billing: config sync is free (0.0 kB of 1.0 GB)", "conversations need a card on file")
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
	mustContain(t, out, "▸ config · 1 to commit", "+ profiles/coder.json", "holds a secret: profiles/leak.json  api_key", "never pulled here", "1 imported file")

	code, out = h.run("commit", "-m", "first", "config")
	if code != 0 {
		t.Fatalf("commit %d: %s", code, out)
	}
	mustContain(t, out, "▸ config · commit #1 · 1 file", "not synced, holds a secret")
	if got, _ := h.srv.Content("config", "profiles/coder.json"); got == "" {
		t.Fatal("not on the server")
	}
	_, out = h.run("commit")
	mustContain(t, out, "▸ config · nothing to commit")

	h.srv.Put("config", "profiles/coder.json", `{"prompt":"y"}`)
	_, out = h.run("status", "config")
	mustContain(t, out, "server 1 commit ahead")
	code, out = h.run("pull", "config")
	if code != 0 || h.read("profiles/coder.json") != `{"prompt":"y"}` {
		t.Fatalf("pull %d: %s", code, out)
	}
	mustContain(t, out, "▸ config · pulled #2 · 1 updated")

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
	mustContain(t, out, "▸ config · not committed · your account has 1 newer file", "~ profiles/x.json   #2 \"edit profiles/x.json\" · other", "→ clai cloud pull config")
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
	mustContain(t, out, "1 conflict", "C profiles/x.json   yours is untouched", "cloud/theirs/profiles/x.json", "→ clai cloud resolve profiles/x.json -theirs")
	_, out = h.run("status", "config")
	mustContain(t, out, "C profiles/x.json")
	_, out = h.run("diff")
	mustContain(t, out, "▸ profiles/x.json · conflict", "+mine", "+theirs")
	code, out = h.run("commit")
	if code != ExitDecisionNeeded {
		t.Fatalf("commit with conflict: %d %s", code, out)
	}
	mustContain(t, out, "▸ config · not committed · 1 conflict to resolve first")
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
	mustContain(t, out, "took your account's copy")
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
		mustContain(t, out, "→ clai cloud commit")
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
		mustContain(t, out, "▸ config · first pull on this machine", "≠ 1 differs", "profiles/x.json", "not pulled", "→ clai cloud pull config -theirs")
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
		for answer, want := range map[string]string{"h\n": "mine", "a\n": "theirs", "d\n": "mine"} {
			h := setup(t)
			h.tty, h.in = true, answer
			code, out := h.run("pull", "config")
			if h.read("profiles/x.json") != want {
				t.Fatalf("%q: file %q, %d %s", answer, h.read("profiles/x.json"), code, out)
			}
			mustContain(t, out, "[a] your account's", "Keep [a/h/d/q]")
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
	mustContain(t, out, "1 merged", "⑂ conversations/c1.json", `"hi (other)"`)
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
	mustContain(t, out, "▸ diff · no local changes")
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
		}, "billing: config sync is free (2.0 MB of 1.0 GB); conversations need a card on file, $1 per started GB a month"},
		{"card, nothing stored", cloud.Me{
			Usage: cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true},
		}, "billing: config sync is free (0.0 kB of 1.0 GB); conversations $0 a month at 0.0 kB"},
		{"card, 1.2 GB", cloud.Me{
			Usage:  cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true},
			Stores: map[string]cloud.StoreInfo{"conversations": {Bytes: 1_200_000_000}},
		}, "billing: config sync is free (0.0 kB of 1.0 GB); conversations $2 a month at 1.2 GB"},
		{"overdue", cloud.Me{
			Usage:  cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true, Overdue: true},
			Stores: map[string]cloud.StoreInfo{"conversations": {Bytes: 1}},
		}, "billing: config sync is free (0.0 kB of 1.0 GB); conversations $1 a month at 0.0 kB; a payment is overdue, so commits are refused"},
	}
	for _, c := range cases {
		if got := billingLine(c.me); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestCommit_withoutACardCommitsConfigAndSaysWhyNotConversations(t *testing.T) {
	h := newHarness(t, "tok-a")
	unlock := h.srv.Lock()
	h.srv.CardOnFile = false
	unlock()
	h.write("profiles/a.json", `{"prompt":"a"}`)
	h.write("conversations/abc.json", `{"messages":[]}`)
	code, out := h.run("commit")
	if code != 0 {
		t.Fatalf("code %d, out %s", code, out)
	}
	mustContain(t, out, "▸ config · commit #1", "▸ conversations · not committed · needs a card on file", "  → "+h.srv.URL()+"/account/billing/")

	r := &runner{deps: h.deps().withDefaults()}
	err := r.commit(context.Background(), []string{"conversations"}, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "/account/billing/") {
		t.Fatalf("explicit conversations commit err = %v, want the billing link", err)
	}
}

func TestProgressText(t *testing.T) {
	cases := []struct {
		p       sync.Progress
		elapsed time.Duration
		want    string
	}{
		{sync.Progress{Phase: sync.PhaseScanning, Done: 1204}, 5 * time.Second, "found 1.2k files"},
		{sync.Progress{Phase: sync.PhaseHashing, Done: 120, Total: 300, Bytes: 1_200_000, TotalBytes: 3_000_000, Reused: 3800}, 0, "reading 120/300 changed files · 1.2 MB of 3.0 MB · 3.8k unchanged"},
		{sync.Progress{Phase: sync.PhaseHashing, Done: 120, Total: 300, Bytes: 1_200_000, TotalBytes: 3_000_000, Reused: 3800}, 4 * time.Second, "reading 120/300 changed files · 1.2 MB of 3.0 MB · 3.8k unchanged · 300.0 kB/s · 6s left"},
		{sync.Progress{Phase: sync.PhaseHashing, Reused: 4100}, 0, "4.1k files unchanged since the last run"},
		{sync.Progress{Phase: sync.PhaseDiscovering}, 0, "looking for conversations in other tools"},
		{sync.Progress{Phase: sync.PhaseExporting}, 0, "exporting conversations from other tools"},
		{sync.Progress{Phase: sync.PhaseExporting, Done: 300, Total: 5000}, 10 * time.Second, "exporting from other tools 300/5.0k · 30 files/s · 2m36s left"},
		{sync.Progress{Phase: sync.PhaseUploading, Done: 2000, Total: 4511, Bytes: 210_000_000, TotalBytes: 1_200_000_000}, 0, "uploaded 2000/4511 files · 210.0 MB of 1.2 GB"},
		{sync.Progress{Phase: sync.PhaseUploading, Done: 2000, Total: 4511, Bytes: 210_000_000, TotalBytes: 1_200_000_000}, 100 * time.Second, "uploaded 2000/4511 files · 210.0 MB of 1.2 GB · 2.1 MB/s · 7m51s left"},
		{sync.Progress{Phase: sync.PhaseUploading, Done: 2000, Total: 4511, Bytes: 210_000_000, TotalBytes: 1_200_000_000}, 3 * time.Hour, "uploaded 2000/4511 files · 210.0 MB of 1.2 GB · 19.4 kB/s · 14h08m left"},
		{sync.Progress{Phase: sync.PhaseCommitting, Done: 1000, Total: 4511}, time.Minute, "recording the commit"},
		{sync.Progress{Phase: sync.PhaseDownloading, Done: 300, Total: 30000, Bytes: 6_000_000, TotalBytes: 553_000_000}, 3 * time.Second, "downloaded 300/30.0k files · 6.0 MB of 553.0 MB · 2.0 MB/s · 4m57s left"},
		{sync.Progress{Phase: sync.PhaseDownloading, Done: 448, Total: 10000, Bytes: 121_600_000, TotalBytes: 1_100_000_000, Have: 9600, Copies: 418}, 0, "downloaded 448/10.0k files · 121.6 MB of 1.1 GB · 9.6k already here · 418 copies"},
		{sync.Progress{Phase: sync.PhaseDownloading, Total: 0, Have: 20018}, 0, "all 20.0k files already here"},
		{sync.Progress{Phase: sync.PhaseDownloading, Done: 2800, Total: 10000, Bytes: 1_000_000_000, TotalBytes: 1_100_000_000}, 231 * time.Second, "downloaded 2.8k/10.0k files · 1.0 GB of 1.1 GB · 4.3 MB/s · 9m54s left"},
		{sync.Progress{Phase: sync.PhaseIndexing, Done: 256, Total: 10000}, 0, "indexing 256/10.0k pulled chats"},
		{sync.Progress{Phase: "other", Done: 3, Total: 9}, 0, "3/9 files"},
	}
	for _, c := range cases {
		if got := progressText(c.p, c.elapsed); got != c.want {
			t.Errorf("progressText(%+v, %s) = %q, want %q", c.p, c.elapsed, got, c.want)
		}
	}
}

func TestCommit_drawsProgressOnATerminal(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/a.json", `{"prompt":"a"}`)
	errOut := &bytes.Buffer{}
	d := h.deps()
	d.Err = errOut
	d.ErrIsTerminal = func() bool { return true }
	code := cmd.Run(context.Background(), []string{"clai", "cloud", "commit"}, map[string]cmd.Command{"cloud": Command(d)}, "usage")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errOut.String(), "committing config") || strings.Contains(h.out.String(), "committing config") {
		t.Fatalf("progress went to stderr %q / stdout %q; want it on stderr only", errOut.String(), h.out.String())
	}
}

func TestStatus_drawsProgressOnATerminal(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/a.json", `{"prompt":"a"}`)
	errOut := &bytes.Buffer{}
	d := h.deps()
	d.Err = errOut
	d.ErrIsTerminal = func() bool { return true }
	code := cmd.Run(context.Background(), []string{"clai", "cloud", "status"}, map[string]cmd.Command{"cloud": Command(d)}, "usage")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errOut.String(), "checking config") {
		t.Fatalf("no progress on stderr: %q", errOut.String())
	}
}
