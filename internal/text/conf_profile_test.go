package text

import (
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
