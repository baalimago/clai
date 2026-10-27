package profiles

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const publishBody = `{"model":"gpt-5.2","prompt":"Review Go."}`

type fakeHub struct {
	got  []PublishRequest
	err  error
	resp Published
}

func (f *fakeHub) publish(_ context.Context, r PublishRequest) (Published, error) {
	f.got = append(f.got, r)
	return f.resp, f.err
}

func publishDeps(t *testing.T, hub *fakeHub) (CommandDeps, string, *bytes.Buffer) {
	t.Helper()
	conf := t.TempDir()
	if err := os.MkdirAll(filepath.Join(conf, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return CommandDeps{
		ConfigDir:       func() (string, error) { return conf, nil },
		Out:             &out,
		NewHubPublisher: func(string) (HubPublisher, error) { return hub.publish, nil },
		FindSecret: func(_ string, b []byte) (string, bool) {
			if bytes.Contains(b, []byte("sk-")) {
				return "prompt", true
			}
			return "", false
		},
	}, conf, &out
}

func writeProfile(t *testing.T, conf, name, body string) string {
	t.Helper()
	p := filepath.Join(conf, "profiles", name+".json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublish_sendsTheProfileByNameAndShowsHowToRunIt(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/go-reviewer", Version: 2, URL: "https://www.clai.chat/hub/ada/go-reviewer/", SHA256: "abc", Created: false}}
	deps, conf, out := publishDeps(t, hub)
	writeProfile(t, conf, "go-reviewer", publishBody)

	err := deps.Publish(t.Context(), "go-reviewer", PublishOptions{Note: "stricter", Summary: "Strict."})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	want := PublishRequest{Name: "go-reviewer", Body: publishBody, Summary: "Strict.", Note: "stricter"}
	if len(hub.got) != 1 || hub.got[0] != want {
		t.Fatalf("sent %+v, want %+v", hub.got, want)
	}
	for _, s := range []string{
		"Published cchub:ada/go-reviewer@2 (a new version)",
		"https://www.clai.chat/hub/ada/go-reviewer/",
		"clai -profile-path cchub:ada/go-reviewer@2#sha256=abc query",
	} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("output %q lacks %q", out.String(), s)
		}
	}
}

func TestPublish_takesAPathAndANameOverride(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/bot", Version: 1, Created: true}}
	deps, _, out := publishDeps(t, hub)
	p := filepath.Join(t.TempDir(), "My Bot.json")
	if err := os.WriteFile(p, []byte(publishBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.Publish(t.Context(), p, PublishOptions{}); err == nil || !strings.Contains(err.Error(), "-name") {
		t.Fatalf("a file name that is no hub name: err = %v, want a hint to -name", err)
	}
	if err := deps.Publish(t.Context(), p, PublishOptions{Name: "bot", Title: "Bot", Handle: "ada"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if hub.got[0].Name != "bot" || hub.got[0].Title != "Bot" || hub.got[0].Handle != "ada" {
		t.Fatalf("sent %+v", hub.got[0])
	}
	if !strings.Contains(out.String(), "Published cchub:ada/bot@1\n") {
		t.Fatalf("output %q", out.String())
	}
}

func TestPublish_refusesBeforeAnythingLeaves(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"secret", `{"model":"m","prompt":"use sk-abc"}`, "holds what looks like a credential (prompt)"},
		{"typo", `{"modle":"m"}`, `did you mean "model"`},
		{"envfile", `{"model":"m","mcp_servers":{"x":{"command":"x","envfile":"x.env"}}}`, "relative path"},
		{"notjson", `{`, "not one JSON object"},
		{"shellcontext", `{"model":"m","shell_context":"git"}`, "never applies a shell context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := &fakeHub{}
			deps, conf, _ := publishDeps(t, hub)
			writeProfile(t, conf, "bot", tc.body)
			err := deps.Publish(t.Context(), "bot", PublishOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(hub.got) != 0 {
				t.Fatalf("sent %d requests, want none", len(hub.got))
			}
		})
	}
}

func TestPublish_errors(t *testing.T) {
	hub := &fakeHub{err: &HandleRequiredError{Suggested: "ada-l"}}
	deps, conf, _ := publishDeps(t, hub)
	writeProfile(t, conf, "bot", publishBody)
	err := deps.Publish(t.Context(), "bot", PublishOptions{})
	if err == nil || !strings.Contains(err.Error(), "clai profiles publish bot -handle ada-l") {
		t.Fatalf("handle required: err = %v", err)
	}

	if err := deps.Publish(t.Context(), "missing", PublishOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing profile: err = %v, want fs.ErrNotExist", err)
	}

	deps.NewHubPublisher = func(string) (HubPublisher, error) { return nil, errors.New("not logged in") }
	if err := deps.Publish(t.Context(), "bot", PublishOptions{}); err == nil || err.Error() != "not logged in" {
		t.Fatalf("no login: err = %v", err)
	}
	deps.NewHubPublisher = nil
	if err := deps.Publish(t.Context(), "bot", PublishOptions{}); err == nil {
		t.Fatal("no publisher: want error")
	}
}

func TestPublishCommand_parsesFlagsAfterTheProfile(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/bot", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)
	writeProfile(t, conf, "bot", publishBody)
	c := Command(deps)
	sub := c.Subs["publish"]
	fs := sub.Flagset()
	if err := fs.Parse([]string{"bot", "-note", "first", "-summary", "Watches prices."}); err != nil {
		t.Fatal(err)
	}
	if err := sub.Setup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sub.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Note != "first" || hub.got[0].Summary != "Watches prices." {
		t.Fatalf("sent %+v", hub.got)
	}
}

func TestPublish_refusesASkillThatCanChange(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/bot", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)
	writeProfile(t, conf, "bot", `{"model":"m","skills":["cchub:ada/review@2","gh:ada/kit/triage"]}`)
	err := deps.Publish(t.Context(), "bot", PublishOptions{})
	if err == nil || !strings.Contains(err.Error(), "gh:ada/kit/triage") || len(hub.got) != 0 {
		t.Fatalf("unpinned skill: err = %v, sent %d", err, len(hub.got))
	}
	writeProfile(t, conf, "bot", `{"model":"m","skills":["cchub:ada/review@2","gh:ada/kit/triage@v1"]}`)
	if err := deps.Publish(t.Context(), "bot", PublishOptions{}); err != nil {
		t.Fatalf("pinned skills: %v", err)
	}
}
