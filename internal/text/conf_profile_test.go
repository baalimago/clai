package text

import (
	"os"
	"path/filepath"
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
