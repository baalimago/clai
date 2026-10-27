package profiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/remote"
)

const botV1 = `{"model":"test","prompt":"v1"}`

type fixture struct {
	deps    CommandDeps
	confDir string
	out     *bytes.Buffer
	files   map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{confDir: t.TempDir(), out: &bytes.Buffer{}, files: map[string]string{
		"/hub/ada/bot/raw":     botV1,
		"/hub/ada/bot/v/1/raw": botV1,
	}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cacheDir := t.TempDir()
	f.deps = CommandDeps{
		NewResolver: func(confDir string, prompt remote.Prompter) (remote.Resolver, error) {
			return remote.NewResolver(remote.Env{CloudURL: srv.URL}, confDir, cacheDir, srv.Client(), prompt, nil)
		},
		ConfigDir: func() (string, error) { return f.confDir, nil },
		Out:       f.out,
	}
	return f
}

func (f *fixture) profile(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.confDir, "profiles", name+".json"))
	if err != nil {
		t.Fatalf("read profile %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse profile %s: %v", name, err)
	}
	return m
}

func TestTrust_recordsTheHash(t *testing.T) {
	f := newFixture(t)
	if err := f.deps.Trust(t.Context(), "cchub:ada/bot@1"); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	ok, err := remote.NewTrustStore(f.confDir).Trusted("cchub:ada/bot", remote.Digest([]byte(botV1)))
	if err != nil || !ok {
		t.Fatalf("trusted = %v %v", ok, err)
	}
	if !strings.Contains(f.out.String(), "Trusted cchub:ada/bot@1") {
		t.Fatalf("out = %q", f.out)
	}
	if err := f.deps.Trust(t.Context(), "cchub:ada/missing"); err == nil {
		t.Fatal("missing target: want error")
	}
}

func TestInstallAndUpdate(t *testing.T) {
	f := newFixture(t)
	if err := f.deps.Install(t.Context(), "cchub:ada/bot@1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	stamp := "cchub:ada/bot@1#sha256=" + remote.Digest([]byte(botV1))
	if p := f.profile(t, "bot"); p["prompt"] != "v1" || p["source"].(map[string]any)["target"] != stamp {
		t.Fatalf("installed profile = %v", p)
	}
	if err := f.deps.Install(t.Context(), "cchub:ada/bot@1", ""); err != nil {
		t.Fatalf("reinstall from the same target: %v", err)
	}

	if err := f.deps.Update(t.Context(), ""); err != nil {
		t.Fatalf("Update unchanged: %v", err)
	}
	if !strings.Contains(f.out.String(), "bot is up to date") {
		t.Fatalf("out = %q", f.out)
	}

	f.files["/hub/ada/bot/raw"] = `{"model":"test","prompt":"v2"}`
	if err := f.deps.Update(t.Context(), "bot"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	p := f.profile(t, "bot")
	if p["prompt"] != "v2" || !strings.HasPrefix(p["source"].(map[string]any)["target"].(string), "cchub:ada/bot#sha256=") {
		t.Fatalf("updated profile = %v", p)
	}
}

func TestInstall_refusals(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.confDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.json"), []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{"source":{"target":"cchub:bob/other"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, name, want string }{
		{"cchub:ada/bot", "mine", "was not installed from a remote target"},
		{"cchub:ada/bot", "other", "was installed from cchub:bob/other"},
		{"cchub:ada/bot", "../x", "is not a profile name"},
		{"./local.json", "", "not a remote profile target"},
		{"cchub:ada/missing", "", "HTTP 404"},
	} {
		if err := f.deps.Install(t.Context(), tc.target, tc.name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("Install(%q, %q) err = %v, want %q", tc.target, tc.name, err, tc.want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Install(t.Context(), "cchub:ada/bot", "broken"); err == nil {
		t.Fatal("broken existing profile: want error")
	}
}

func TestUpdate_refusalsAndEmptyDirs(t *testing.T) {
	f := newFixture(t)
	if err := f.deps.Update(t.Context(), ""); err != nil {
		t.Fatalf("Update without profiles dir: %v", err)
	}
	if !strings.Contains(f.out.String(), "No profile was installed") {
		t.Fatalf("out = %q", f.out)
	}
	dir := filepath.Join(f.confDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.json"), []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Update(t.Context(), "mine"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Update(mine) err = %v, want ErrNotInstalled", err)
	}
	if err := f.deps.Update(t.Context(), "../../outside"); err == nil || !strings.Contains(err.Error(), "is not a profile name") {
		t.Fatalf("Update(../../outside) err = %v, want a name refusal", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.json"), []byte(`{"source":{"target":"cchub:ada/gone"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"source":{"target":"cchub:BAD"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.deps.Update(t.Context(), "")
	if err == nil || !strings.Contains(err.Error(), "gone: ") || !strings.Contains(err.Error(), "bad: source stamp") {
		t.Fatalf("Update all err = %v", err)
	}
}

func TestRemoteCommands_needDeps(t *testing.T) {
	var empty CommandDeps
	if err := empty.Trust(t.Context(), "cchub:ada/bot"); err == nil {
		t.Fatal("no config dir: want error")
	}
	empty.ConfigDir = func() (string, error) { return t.TempDir(), nil }
	for _, run := range []func() error{
		func() error { return empty.Trust(t.Context(), "cchub:ada/bot") },
		func() error { return empty.Install(t.Context(), "cchub:ada/bot", "") },
		func() error { return empty.Update(t.Context(), "") },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "not available") {
			t.Fatalf("err = %v, want not available", err)
		}
	}
}

func TestRemoteSubcommands_checkTheirArgs(t *testing.T) {
	f := newFixture(t)
	c := Command(f.deps)
	install := c.Subcommands()["install"]
	fs := install.(interface{ Flagset() *flag.FlagSet }).Flagset()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := install.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := install.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "usage: clai profiles install <target> [name]") {
		t.Fatalf("Run without args err = %v", err)
	}
	for _, step := range []struct {
		name string
		args []string
	}{{"trust", []string{"cchub:ada/bot@1"}}, {"install", []string{"cchub:ada/bot@1", "b"}}, {"update", []string{"b"}}} {
		name, args := step.name, step.args
		sub := c.Subcommands()[name]
		if err := sub.(interface{ Flagset() *flag.FlagSet }).Flagset().Parse(args); err != nil {
			t.Fatal(err)
		}
		if err := sub.Setup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := sub.Run(context.Background()); err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
	}
}

func TestInstall_fetchesAndTrustsTheProfilesSkills(t *testing.T) {
	f := newFixture(t)
	f.files["/hub/ada/agent/v/1/raw"] = `{"model":"test","skills":["cchub:ada/review@2"]}`
	f.files["/hub/ada/review/v/2/raw"] = "---\nname: review\ndescription: Review the diff.\n---\nBody"
	if err := f.deps.Install(t.Context(), "cchub:ada/agent@1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(f.out.String(), "Skill review from cchub:ada/review@2") {
		t.Fatalf("output %q does not name the skill", f.out.String())
	}
	b, err := os.ReadFile(filepath.Join(f.confDir, remote.TrustFile))
	if err != nil || !strings.Contains(string(b), "cchub:ada/review") {
		t.Fatalf("trust.json = %s, %v; want the skill trusted with the profile", b, err)
	}

	delete(f.files, "/hub/ada/review/v/2/raw")
	f.files["/hub/ada/broken/v/1/raw"] = `{"model":"test","skills":["cchub:ada/review@3"]}`
	if err := f.deps.Trust(t.Context(), "cchub:ada/broken@1"); err == nil || !strings.Contains(err.Error(), "cchub:ada/review@3") {
		t.Fatalf("missing skill: err = %v, want it named", err)
	}
}

// update with no name asks before it takes a version nobody trusted; with
// nobody to ask, it changes nothing.
func TestUpdate_allAsksBeforeANewVersion(t *testing.T) {
	f := newFixture(t)
	if err := f.deps.Install(t.Context(), "cchub:ada/bot@1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	f.files["/hub/ada/bot/raw"] = `{"model":"test","prompt":"v2"}`
	var asked []string
	answer := false
	asking := func() remote.Prompter {
		return func(_ context.Context, p remote.TrustPrompt) (bool, error) {
			asked = append(asked, p.Target)
			return answer, nil
		}
	}
	f.deps.NewPrompt = asking
	var untrusted *remote.UntrustedError
	if err := f.deps.Update(t.Context(), ""); !errors.As(err, &untrusted) || len(asked) != 1 {
		t.Fatalf("declined: err = %v, asked %v", err, asked)
	}
	if p := f.profile(t, "bot"); p["prompt"] != "v1" {
		t.Fatalf("a declined version was written: %v", p)
	}
	f.deps.NewPrompt = func() remote.Prompter { return nil }
	if err := f.deps.Update(t.Context(), ""); !errors.As(err, &untrusted) {
		t.Fatalf("nobody to ask: err = %v, want an UntrustedError", err)
	}
	answer, f.deps.NewPrompt = true, asking
	if err := f.deps.Update(t.Context(), ""); err != nil {
		t.Fatalf("accepted: %v", err)
	}
	if p := f.profile(t, "bot"); p["prompt"] != "v2" {
		t.Fatalf("accepted version not written: %v", p)
	}
}
