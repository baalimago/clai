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

func TestPublishAlias_sendsDeduplicatedAliasFromTextConfig(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/strong", Version: 1, URL: "https://www.clai.chat/hub/ada/strong/", SHA256: "abc", Created: true}}
	deps, conf, out := publishDeps(t, hub)
	textConf := `{"model":"strong","model-aliases":{"strong":["claude-opus-4-7","gpt-5.4","claude-opus-4-7"]}}`
	if err := os.WriteFile(filepath.Join(conf, "textConfig.json"), []byte(textConf), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Summary: "Top models.", Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias: %v", err)
	}
	want := PublishRequest{
		Kind:    "alias",
		Name:    "strong",
		Body:    `["claude-opus-4-7","gpt-5.4"]`,
		Summary: "Top models.",
		Handle:  "ada",
	}
	if len(hub.got) != 1 || hub.got[0] != want {
		t.Fatalf("sent %+v, want %+v", hub.got, want)
	}
	for _, s := range []string{
		"Published cchub:ada/strong@1",
		"https://www.clai.chat/hub/ada/strong/",
		"clai -chat-model cchub:ada/strong@1#sha256=abc query",
	} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("output %q lacks %q", out.String(), s)
		}
	}
}

func TestPublishAlias_takesAJSONFilePathAndRefusesInvalidOrSecretBodies(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/fast", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)
	path := filepath.Join(t.TempDir(), "fast.json")
	if err := os.WriteFile(path, []byte(`["gpt-5.4-mini","gpt-5.4-mini","claude-haiku-4-5"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishAlias(t.Context(), path, PublishOptions{}); err != nil {
		t.Fatalf("PublishAlias(path): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Name != "fast" || hub.got[0].Body != `["gpt-5.4-mini","claude-haiku-4-5"]` {
		t.Fatalf("sent %+v", hub.got)
	}

	secretPath := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(secretPath, []byte(`["sk-secret-key"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishAlias(t.Context(), secretPath, PublishOptions{}); err == nil || !strings.Contains(err.Error(), "holds what looks like a credential") {
		t.Fatalf("secret alias: err = %v", err)
	}

	// Broken local alias (references another alias) is refused before sending.
	brokenConf := `{"model":"strong","model-aliases":{"strong":["fast"],"fast":["gpt-5.4"]}}`
	if err := os.WriteFile(filepath.Join(conf, "textConfig.json"), []byte(brokenConf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{}); err == nil || !strings.Contains(err.Error(), "names another alias") {
		t.Fatalf("nested alias: err = %v", err)
	}
}

func TestPublishAlias_modeDisambiguationAndMacroByNameAndIndex(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/strong", Version: 1, URL: "https://www.clai.chat/hub/ada/strong/", SHA256: "abc", Created: true}}
	deps, conf, out := publishDeps(t, hub)
	textConf := `{"model":"strong","model-aliases":{"strong":["claude-opus-4-7","gpt-5.4"]}}`
	photoConf := `{"model":"strong","model-aliases":{"strong":["dall-e-3","gpt-image-1"]}}`
	if err := os.WriteFile(filepath.Join(conf, "textConfig.json"), []byte(textConf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "photoConfig.json"), []byte(photoConf), 0o644); err != nil {
		t.Fatal(err)
	}

	// Macro mode by name: "photo"
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "photo", Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias(mode=photo): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("mode=photo sent %+v", hub.got)
	}
	if !strings.Contains(out.String(), "clai -photo-model cchub:ada/strong@1#sha256=abc photo") {
		t.Fatalf("mode=photo output %q lacks -photo-model hint", out.String())
	}

	// Macro mode by name: "text"
	hub.got = nil
	out.Reset()
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "text", Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias(mode=text): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["claude-opus-4-7","gpt-5.4"]` {
		t.Fatalf("mode=text sent %+v", hub.got)
	}
	if !strings.Contains(out.String(), "clai -chat-model cchub:ada/strong@1#sha256=abc query") {
		t.Fatalf("mode=text output %q lacks -chat-model hint", out.String())
	}

	// Macro mode by sorted index: "0" = photo, "1" = text
	hub.got = nil
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "0", Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias(mode=0): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("mode=0 sent %+v", hub.got)
	}
	hub.got = nil
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "1", Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias(mode=1): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["claude-opus-4-7","gpt-5.4"]` {
		t.Fatalf("mode=1 sent %+v", hub.got)
	}

	// Interactive mode selection when Mode is omitted: "0\n" picks photo.
	hub.got = nil
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{In: strings.NewReader("0\n"), Handle: "ada"}); err != nil {
		t.Fatalf("PublishAlias(interactive mode=0): %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("interactive mode=0 sent %+v", hub.got)
	}

	// Ambiguous without Mode or interactive input fails with a clear hint.
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Handle: "ada"}); err == nil || !strings.Contains(err.Error(), "clai cloud publish alias strong") {
		t.Fatalf("ambiguous without input: err = %v", err)
	}

	// Mode where the alias does not exist returns fs.ErrNotExist.
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "audio"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing in audio mode: err = %v, want fs.ErrNotExist", err)
	}

	// Unknown mode name fails.
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{Mode: "video"}); err == nil || !strings.Contains(err.Error(), "expected text, photo or audio") {
		t.Fatalf("unknown mode: err = %v", err)
	}
}

func TestPublishInteractive_traversesTreeAndSupportsMacroArgs(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/strong", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)

	// Empty config dir has nothing to publish for each kind.
	for _, kind := range []string{"alias", "profile", "skill"} {
		if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n"), []string{kind}, PublishOptions{}); err == nil {
			t.Fatalf("empty dir for %s: want error", kind)
		}
	}

	if err := os.WriteFile(filepath.Join(conf, "textConfig.json"), []byte(`{"model":"strong","model-aliases":{"strong":["claude-opus-4-7","gpt-5.4"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "photoConfig.json"), []byte(`{"model":"strong","model-aliases":{"strong":["dall-e-3","gpt-image-1"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, conf, "go-reviewer", publishBody)
	skillDir := filepath.Join(conf, "skills", "triage")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: triage\ndescription: Triage.\n---\nBody"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 0 args (`clai cloud publish`):
	// Step 1 picks kind 0 (alias), Step 2 picks alias 0 (strong), Step 3 picks mode 0 (photo).
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n0\n0\n"), nil, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("0 args alias->strong->photo: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Kind != "alias" || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("0 args alias sent %+v", hub.got)
	}

	// 0 args: Step 1 picks kind 1 (profile), Step 2 picks profile 0 (go-reviewer).
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("1\n0\n"), nil, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("0 args profile->go-reviewer: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Kind != "" || hub.got[0].Name != "go-reviewer" {
		t.Fatalf("0 args profile sent %+v", hub.got)
	}

	// 0 args: Step 1 picks kind 2 (skill), Step 2 picks skill 0 (triage).
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("2\n0\n"), nil, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("0 args skill->triage: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Kind != "skill" || hub.got[0].Name != "triage" {
		t.Fatalf("0 args skill sent %+v", hub.got)
	}

	// 1 arg (`clai cloud publish alias`): skips Step 1, Step 2 picks alias 0 (strong), Step 3 picks mode 1 (text).
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n1\n"), []string{"alias"}, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("1 arg alias: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Kind != "alias" || hub.got[0].Body != `["claude-opus-4-7","gpt-5.4"]` {
		t.Fatalf("1 arg alias sent %+v", hub.got)
	}

	// 2 args (`clai cloud publish alias strong`): skips Steps 1 & 2, Step 3 picks mode 0 (photo).
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n"), []string{"alias", "strong"}, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("2 args alias strong: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("2 args alias strong sent %+v", hub.got)
	}

	// 3 args (`clai cloud publish alias strong photo`): macro mode, no interactive input read.
	hub.got = nil
	if err := deps.PublishInteractive(t.Context(), strings.NewReader(""), []string{"alias", "strong", "photo"}, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("3 args macro photo: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Body != `["dall-e-3","gpt-image-1"]` {
		t.Fatalf("3 args macro photo sent %+v", hub.got)
	}

	// User quits at Step 1 with 'q'.
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("q\n"), nil, PublishOptions{}); err == nil {
		t.Fatal("quit: want error")
	}
}

func TestPublishAlias_errorPaths(t *testing.T) {
	hub := &fakeHub{err: &HandleRequiredError{Suggested: "ada"}}
	deps, conf, _ := publishDeps(t, hub)
	if err := os.WriteFile(filepath.Join(conf, "photoConfig.json"), []byte(`{"model-aliases":{"art":["dall-e-3"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishAlias(t.Context(), "art", PublishOptions{}); err == nil || !strings.Contains(err.Error(), "clai cloud publish alias art -handle ada") {
		t.Fatalf("handle required hint: err = %v", err)
	}
	if err := deps.PublishAlias(t.Context(), "missing", PublishOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing alias: err = %v", err)
	}
	if err := deps.PublishAlias(t.Context(), "/nonexistent/alias.json", PublishOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: err = %v", err)
	}
	if err := deps.PublishAlias(t.Context(), "art", PublishOptions{Name: "Bad Name"}); err == nil || !strings.Contains(err.Error(), "-name") {
		t.Fatalf("invalid hub name: err = %v", err)
	}
}

func TestPublishInteractive_refusesBadArgumentsAndModes(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/strong", Version: 1, Created: true}}
	deps, conf, out := publishDeps(t, hub)
	if err := os.WriteFile(filepath.Join(conf, "textConfig.json"), []byte(`{"model-aliases":{"strong":["a"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "photoConfig.json"), []byte(`{"model-aliases":{"strong":["b"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"widget"}, `unknown publish kind "widget"`},
		{[]string{"profile", "x", "y"}, `unexpected argument "y"`},
		{[]string{"alias", "strong", "text", "extra"}, `unexpected argument "extra"`},
		{[]string{"alias", "strong", "5"}, "mode index 5 out of range"},
		{[]string{"alias", "strong", "video"}, "expected text, photo or audio"},
	} {
		if err := deps.PublishInteractive(t.Context(), strings.NewReader(""), tc.args, PublishOptions{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	var amb *AmbiguousAliasError
	if err := deps.PublishAlias(t.Context(), "strong", PublishOptions{}); !errors.As(err, &amb) || strings.Join(amb.Modes, ",") != "photo,text" {
		t.Fatalf("ambiguous: err = %v", err)
	}
	var unknown *UnknownAliasModeError
	path := filepath.Join(t.TempDir(), "fast.json")
	if err := os.WriteFile(path, []byte(`["x"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishAlias(t.Context(), path, PublishOptions{Mode: "video"}); !errors.As(err, &unknown) {
		t.Fatalf("file with unknown mode: err = %v", err)
	}
	if err := deps.PublishAlias(t.Context(), path, PublishOptions{Mode: "audio"}); err != nil {
		t.Fatalf("file with audio mode: %v", err)
	}
	if !strings.Contains(out.String(), "clai audio transcribe -audio-model cchub:ada/strong@1") {
		t.Fatalf("audio hint missing: %q", out.String())
	}
	if err := os.WriteFile(filepath.Join(conf, "audioConfig.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n"), []string{"alias"}, PublishOptions{}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("corrupt audioConfig: err = %v", err)
	}
}

// TestPublishInteractive_nilInputNeverPrompts pins that nil input means
// nobody can answer at every level: a missing level is an error naming the
// argument to pass, never a read from the terminal behind the caller's back.
func TestPublishInteractive_nilInputNeverPrompts(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/fast", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)
	for _, f := range []string{"textConfig.json", "photoConfig.json"} {
		if err := os.WriteFile(filepath.Join(conf, f), []byte(`{"model-aliases":{"fast":["m"]}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(t, conf, "bot", publishBody)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "clai cloud publish <alias|profile|skill>"},
		{[]string{"alias"}, "clai cloud publish alias <name>"},
		{[]string{"profile"}, "clai cloud publish profile <name>"},
		{[]string{"alias", "fast"}, "clai cloud publish alias fast <photo|text>"},
	} {
		if err := deps.PublishInteractive(t.Context(), nil, tc.args, PublishOptions{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	if len(hub.got) != 0 {
		t.Fatalf("sent %+v, want nothing", hub.got)
	}
	// The real wizard path: kind, alias, then mode all answered on one reader.
	if err := deps.PublishInteractive(t.Context(), strings.NewReader("0\n0\n1\n"), nil, PublishOptions{Handle: "ada"}); err != nil {
		t.Fatalf("wizard through mode: %v", err)
	}
	if len(hub.got) != 1 || !strings.HasSuffix(hub.got[0].Body, `["m"]`) {
		t.Fatalf("wizard sent %+v", hub.got)
	}
}
