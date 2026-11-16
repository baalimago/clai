package text

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSelection(t *testing.T) {
	for _, tc := range []struct {
		name, selector, want, wantErr string
		files                         map[string]string
	}{
		{"filename stays stable", "me", "me", "", map[string]string{"me.json": `{"name":"Myself","model":"selected"}`}},
		{"legacy display name", "Myself", "me", "", map[string]string{"me.json": `{"name":"Myself","model":"selected"}`, "ignore.txt": `{`}},
		{"filename wins", "Myself", "Myself", "", map[string]string{"Myself.json": `{"name":"Other","model":"selected"}`, "me.json": `{"name":"Myself","model":"wrong"}`}},
		{"ambiguous display name", "Myself", "", "ambiguous profile display name", map[string]string{"me.json": `{"name":"Myself"}`, "other.json": `{"name":"Myself"}`}},
		{"missing display name", "Myself", "", "Myself", map[string]string{"me.json": `{"name":"Other"}`}},
		{"invalid exact file cannot fall back", "Myself", "", "Myself.json", map[string]string{"Myself.json": `{`, "me.json": `{"name":"Myself"}`}},
		{"unreadable candidate", "Myself", "", "broken.json", map[string]string{"broken.json": `{`, "me.json": `{"name":"Myself"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAI_CONFIG_DIR", dir)
			if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
				t.Fatal(err)
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, "profiles", name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			conf := Default
			conf.UseProfile = tc.selector
			err := conf.ProfileOverrides()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if conf.UseProfile != tc.want || conf.Model != "selected" {
				t.Fatalf("selection = %q, model = %q", conf.UseProfile, conf.Model)
			}
		})
	}
}

func TestProfilePathSelectionIsAbsolute(t *testing.T) {
	file := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(file, []byte(`{"name":"Myself","model":"selected"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, file)
	if err != nil {
		t.Fatal(err)
	}
	conf := Default
	conf.ProfilePath = relative
	if err := conf.ProfileOverrides(); err != nil {
		t.Fatal(err)
	}
	if conf.UseProfile != file {
		t.Fatalf("UseProfile = %q, want %q", conf.UseProfile, file)
	}
}
