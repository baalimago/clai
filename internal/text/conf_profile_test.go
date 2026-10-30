package text

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFindProfile_OmittedUseSkillsStaysNil(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	filePath := filepath.Join(profilePath, "john.json")
	profileJSON := `{"name":"John","model":"test","prompt":"x","use_tools":true}`
	if err := os.WriteFile(filePath, []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	prof, err := findProfile("john")
	if err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	if prof.UseSkills != nil {
		t.Fatalf("expected omitted use_skills to remain nil, got %#v", prof.UseSkills)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(profile): %v", err)
	}
	if got := string(updated); strings.Contains(got, `"use_skills"`) {
		t.Fatalf("expected profile file to remain without use_skills migration, got: %s", got)
	}
}

func TestFindProfile_rehydrates_legacy_escaped_prompt(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"name":"legacy","model":"test","prompt":"Rules:\\n\\t* stay short\\n\\nBe concise"}`
	if err := os.WriteFile(filepath.Join(profilePath, "legacy.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	prof, err := findProfile("legacy")
	if err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	want := "Rules:\n\t* stay short\n\nBe concise"
	if prof.Prompt != want {
		t.Fatalf("prompt not rehydrated:\nwant: %q\n got: %q", want, prof.Prompt)
	}
}

func TestFindProfile_keeps_prompt_that_already_has_real_newlines(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"name":"mixed","model":"test","prompt":"line1\nline2 \\n kept"}`
	if err := os.WriteFile(filepath.Join(profilePath, "mixed.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	prof, err := findProfile("mixed")
	if err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	want := "line1\nline2 \\n kept"
	if prof.Prompt != want {
		t.Fatalf("hand written prompt mutated:\nwant: %q\n got: %q", want, prof.Prompt)
	}
}

func TestConfigurations_ProfileOverrides_DefaultProfileKeepsSaveReplyAsConvEnabled(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"name":"nonsaving","model":"test"}`
	if err := os.WriteFile(filepath.Join(profilePath, "nonsaving.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	conf := Default
	conf.UseProfile = "nonsaving"
	conf.SaveReplyAsConv = true

	if err := conf.ProfileOverrides(); err != nil {
		t.Fatalf("ProfileOverrides: %v", err)
	}

	if !conf.SaveReplyAsConv {
		t.Fatalf("expected omitted profile save-reply-as-conv to keep save enabled")
	}
}

func TestConfigurations_ProfileOverrides_ExplicitFalseKeepsSaveReplyAsConvDisabled(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"name":"nonsaving","model":"test","save-reply-as-conv":false}`
	if err := os.WriteFile(filepath.Join(profilePath, "nonsaving.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	conf := Default
	conf.UseProfile = "nonsaving"
	conf.SaveReplyAsConv = true

	if err := conf.ProfileOverrides(); err != nil {
		t.Fatalf("ProfileOverrides: %v", err)
	}

	if conf.SaveReplyAsConv {
		t.Fatalf("expected explicit false profile save-reply-as-conv to stay disabled")
	}
}

func TestConfigurations_ProfileOverrides_CmdBanCascade(t *testing.T) {
	tests := []struct {
		name        string
		profileJSON string
		want        []string
	}{
		{
			name:        "Explicit cmd-ban merges onto file base",
			profileJSON: `{"name":"gopher","model":"test","cmd-ban":["git commit"]}`,
			want:        []string{"rm", "git commit"},
		},
		{
			name:        "Omitted cmd-ban contributes nothing",
			profileJSON: `{"name":"gopher","model":"test"}`,
			want:        []string{"rm"},
		},
		{
			name:        "Explicit empty cmd-ban contributes nothing",
			profileJSON: `{"name":"gopher","model":"test","cmd-ban":[]}`,
			want:        []string{"rm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confDir := t.TempDir()
			t.Setenv("CLAI_CONFIG_DIR", confDir)

			profilePath := filepath.Join(confDir, "profiles")
			if err := os.MkdirAll(profilePath, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q): %v", profilePath, err)
			}
			if err := os.WriteFile(filepath.Join(profilePath, "gopher.json"), []byte(tt.profileJSON), 0o644); err != nil {
				t.Fatalf("WriteFile(profile): %v", err)
			}

			conf := Default
			conf.UseProfile = "gopher"
			conf.CmdBan = []string{"rm"}

			if err := conf.ProfileOverrides(); err != nil {
				t.Fatalf("ProfileOverrides: %v", err)
			}

			if !slices.Equal(conf.CmdBan, tt.want) {
				t.Fatalf("expected CmdBan %v, got %v", tt.want, conf.CmdBan)
			}
		})
	}
}

// TestFindProfile_NameLessProfileStaysNameLess pins that the migration never
// writes the placeholder default name into user profile files: a profile's
// name is derived from its file name at load time (findProfile normalizes an
// empty name), so name-less profiles must stay name-less on disk.
func TestFindProfile_NameLessProfileStaysNameLess(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"model":"test","prompt":"x"}`
	if err := os.WriteFile(filepath.Join(profilePath, "john.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	prof, err := findProfile("john")
	if err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	if prof.Name != "john" {
		t.Fatalf("expected name normalized to the profile file name, got %q", prof.Name)
	}
	updated, err := os.ReadFile(filepath.Join(profilePath, "john.json"))
	if err != nil {
		t.Fatalf("ReadFile(profile): %v", err)
	}
	if got := string(updated); strings.Contains(got, "example-name") {
		t.Fatalf("migration must not write the placeholder default name into user profile files, got: %s", got)
	}
}

func TestFindProfile_CmdBanPersistsViaJSON(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profilePath := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profilePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profilePath, err)
	}
	profileJSON := `{"name":"john","model":"test","cmd-ban":["git commit","sudo"]}`
	if err := os.WriteFile(filepath.Join(profilePath, "john.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	prof, err := findProfile("john")
	if err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	want := []string{"git commit", "sudo"}
	if !slices.Equal(prof.CmdBan, want) {
		t.Fatalf("expected CmdBan %v, got %v", want, prof.CmdBan)
	}
}

func Test_findProfileByPath_LoadsProfileFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "reviewer.json")
	if err := os.WriteFile(p, []byte(`{"name":"reviewer","model":"gpt-x","use_tools":true}`), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	got, err := findProfileByPath(p)
	if err != nil {
		t.Fatalf("findProfileByPath: %v", err)
	}
	if got.Name != "reviewer" || got.Model != "gpt-x" || !got.UseTools {
		t.Errorf("loaded profile = %+v, want reviewer/gpt-x/tools-on", got)
	}
}

func TestConfigurations_ProfileOverrides_EnvFileHomeResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	profileDir := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", profileDir, err)
	}

	tests := []struct {
		name    string
		envfile string
		want    string
	}{
		{name: "tilde resolves to home", envfile: "~/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "HOME var resolves to home", envfile: "$HOME/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "relative joins profile dir", envfile: ".envfile", want: filepath.Join(profileDir, ".envfile")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profileJSON := fmt.Sprintf(
				`{"name":"gopher","model":"test","mcp_servers":{"srv":{"command":"echo","envfile":%q}}}`,
				tt.envfile)
			if err := os.WriteFile(filepath.Join(profileDir, "gopher.json"), []byte(profileJSON), 0o644); err != nil {
				t.Fatalf("WriteFile(profile): %v", err)
			}

			conf := Default
			conf.UseProfile = "gopher"
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatalf("ProfileOverrides: %v", err)
			}
			if len(conf.McpServers) != 1 {
				t.Fatalf("expected 1 mcp server, got %d", len(conf.McpServers))
			}
			if conf.McpServers[0].EnvFile != tt.want {
				t.Fatalf("EnvFile = %q, want %q", conf.McpServers[0].EnvFile, tt.want)
			}
		})
	}
}

func TestConfigurations_ProfileOverrides_RejectsInvalidRequiredEnvName(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "medic.json")
	profileJSON := `{"name":"medic","model":"test","mcp_servers":{
		"sentry":{"command":"npx","auth":{"required_env":["SENTRY-TOKEN"]}}}}`
	if err := os.WriteFile(path, []byte(profileJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	conf := Default
	conf.ProfilePath = path
	err := conf.ProfileOverrides()
	if err == nil || !strings.Contains(err.Error(), "sentry") || !strings.Contains(err.Error(), "auth.required_env[0]") || strings.Contains(err.Error(), "SENTRY-TOKEN") {
		t.Fatalf("ProfileOverrides = %v, want an error naming the server and the entry's index, not its value", err)
	}

	valid := strings.Replace(profileJSON, "SENTRY-TOKEN", "SENTRY_ACCESS_TOKEN", 1)
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	conf = Default
	conf.ProfilePath = path
	if err := conf.ProfileOverrides(); err != nil {
		t.Fatalf("ProfileOverrides with a valid name: %v", err)
	}
	if got := conf.McpServers[0].Auth.RequiredEnv; len(got) != 1 || got[0] != "SENTRY_ACCESS_TOKEN" {
		t.Fatalf("RequiredEnv = %v", got)
	}
}

// A fetched profile gets only the tools it names: use_tools alone would hand
// a prompt-injected model every local tool and MCP server.
func TestConfigurations_ProfileOverrides_FetchedProfileGetsOnlyNamedTools(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name, body string
		remote     bool
		wantTools  bool
	}{
		{"local use_tools keeps every tool", `{"use_tools":true}`, false, true},
		{"fetched by -prp gets none", `{"use_tools":true}`, true, false},
		{"installed from the hub gets none", `{"use_tools":true,"source":{"target":"cchub:ada/bot"}}`, false, false},
		{"fetched with a tools list keeps it", `{"use_tools":true,"tools":["date"]}`, true, true},
		{"fetched with its own servers keeps them", `{"mcp_servers":{"m":{"command":"node","args":["m.js"]}}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			conf := Default
			conf.ProfilePath = path
			conf.ProfileFetched = tc.remote
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatal(err)
			}
			if conf.UseTools != tc.wantTools {
				t.Fatalf("UseTools = %v, want %v", conf.UseTools, tc.wantTools)
			}
		})
	}
}

// A fetched or installed profile cannot keep its runs out of the chat
// history: the run is the record of what it did on this machine.
func TestConfigurations_ProfileOverrides_FetchedProfileRunsAreSaved(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name, body string
		remote     bool
		base, want bool
	}{
		{"local profile may opt out", `{"save-reply-as-conv":false}`, false, true, false},
		{"fetched by -prp is saved", `{"save-reply-as-conv":false}`, true, true, true},
		{"installed from the hub is saved", `{"save-reply-as-conv":false,"source":{"target":"cchub:ada/bot"}}`, false, true, true},
		{"your own opt-out still holds", `{"save-reply-as-conv":true}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			conf := Default
			conf.SaveReplyAsConv = tc.base
			conf.ProfilePath = path
			conf.ProfileFetched = tc.remote
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatal(err)
			}
			if conf.SaveReplyAsConv != tc.want {
				t.Fatalf("SaveReplyAsConv = %v, want %v", conf.SaveReplyAsConv, tc.want)
			}
		})
	}
}

// A fetched or installed profile never picks a shell context: it would run
// this machine's shell context commands on the profile's say.
func TestConfigurations_ProfileOverrides_FetchedProfileGetsNoShellContext(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name, body string
		remote     bool
		want       string
	}{
		{"local profile picks one", `{"shell_context":"git"}`, false, "git"},
		{"fetched by -prp does not", `{"shell_context":"git"}`, true, ""},
		{"installed from the hub does not", `{"shell_context":"git","source":{"target":"cchub:ada/bot"}}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			conf := Default
			conf.ProfilePath = path
			conf.ProfileFetched = tc.remote
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatal(err)
			}
			if conf.ShellContext != tc.want {
				t.Fatalf("ShellContext = %q, want %q", conf.ShellContext, tc.want)
			}
		})
	}
}

// A run with a remote profile is stamped with the target it was run as, so
// `clai chat list` shows which hub profile a chat came from.
func TestConfigurations_ProfileOverrides_FetchedProfileIsNamedByItsTarget(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name, body, target, want string
	}{
		{"remote target wins over the profile's name", `{"name":"reviewer"}`, "cchub:clai/reviewer@5", "cchub:clai/reviewer@5"},
		{"a pinned hash is left out", `{}`, "cchub:clai/reviewer@5#sha256=abc", "cchub:clai/reviewer@5"},
		{"a local path keeps the profile's name", `{"name":"reviewer"}`, "", "reviewer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			conf := Default
			conf.ProfilePath = path
			conf.ProfileTarget = tc.target
			conf.ProfileFetched = tc.target != ""
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatal(err)
			}
			if conf.UseProfile != tc.want {
				t.Fatalf("UseProfile = %q, want %q", conf.UseProfile, tc.want)
			}
		})
	}
}

// A profile may pin the model the audio_transcribe tool uses for its runs.
func TestConfigurations_ProfileOverrides_AudioModel(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name, body, want string
	}{
		{"the profile's audio model is carried to the run", `{"audio":{"model":"diarize"}}`, "diarize"},
		{"an omitted audio block sets nothing", `{"model":"m"}`, ""},
		{"an empty audio block sets nothing", `{"audio":{}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			conf := Default
			conf.ProfilePath = path
			if err := conf.ProfileOverrides(); err != nil {
				t.Fatal(err)
			}
			if conf.AudioModel != tc.want {
				t.Fatalf("AudioModel = %q, want %q", conf.AudioModel, tc.want)
			}
		})
	}
}
