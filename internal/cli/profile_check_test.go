package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

// TestCheckProfileBody_nestedUnreadMembersReachIgnored proves the wiring from
// the checker to remote.Checked.Ignored keeps a member at any depth, so the
// trust prompt lists audio.language and an auth header the way HEAD did.
func TestCheckProfileBody_nestedUnreadMembersReachIgnored(t *testing.T) {
	body := `{"prompt":"p",
		"audio":{"language":"en"},
		"mcp_servers":{"s":{"command":"x","auth":{"headers":{"A":"B"}}}},
		"notes":"anything"}`
	checked := checkProfileBody([]byte(body), profile.Env{})
	if len(checked.Problems) != 0 {
		t.Fatalf("Problems = %v, want none", checked.Problems)
	}
	for _, want := range []string{"notes", "audio.language", "mcp_servers.s.auth.headers"} {
		if !slices.Contains(checked.Ignored, want) {
			t.Fatalf("Ignored = %v, want it to name %q", checked.Ignored, want)
		}
	}
}

// TestNewProfileEnv_audioModelRoutesThroughTheAudioAliases pins the audio table for audio.model.
func TestNewProfileEnv_audioModelRoutesThroughTheAudioAliases(t *testing.T) {
	writeConf := func(t *testing.T, dir, name string, conf any) {
		t.Helper()
		if err := utils.CreateFile(filepath.Join(dir, name), &conf); err != nil {
			t.Fatalf("CreateFile(%v): %v", name, err)
		}
	}
	t.Run("the audio config's ladder", func(t *testing.T) {
		dir := t.TempDir()
		writeConf(t, dir, "textConfig.json", &text.Default)
		aConf := audio.Default
		aConf.ModelAliases = modelalias.Table{"meeting": {"gpt-4o-transcribe-diarize", "or:elevenlabs/scribe-v2+diarize"}}
		writeConf(t, dir, "audioConfig.json", &aConf)
		env, err := newProfileEnv(dir)
		if err != nil {
			t.Fatalf("newProfileEnv: %v", err)
		}
		got, err := env.ResolveAudioModel("meeting")
		if err != nil || !slices.Equal(got, aConf.ModelAliases["meeting"]) {
			t.Fatalf("ResolveAudioModel(meeting) = %v, %v, want the audio ladder", got, err)
		}
		if checked := checkProfileBody([]byte(`{"audio":{"model":"meeting"}}`), env); len(checked.Problems) != 0 {
			t.Fatalf("Problems = %v, want an audio alias accepted", checked.Problems)
		}
		if checked := checkProfileBody([]byte(`{"audio":{"model":"fast"}}`), env); len(checked.Problems) != 0 {
			t.Fatalf("Problems = %v, want a verb both tables hold accepted for audio", checked.Problems)
		}
	})
	t.Run("no audio config reads the shipped ladders", func(t *testing.T) {
		dir := t.TempDir()
		writeConf(t, dir, "textConfig.json", &text.Default)
		env, err := newProfileEnv(dir)
		if err != nil {
			t.Fatalf("newProfileEnv: %v", err)
		}
		got, err := env.ResolveAudioModel("fast")
		if err != nil || !slices.Equal(got, audio.Default.ModelAliases["fast"]) {
			t.Fatalf("ResolveAudioModel(fast) = %v, %v, want the shipped audio ladder", got, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "audioConfig.json")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat audioConfig.json = %v, want the check to write no file", err)
		}
	})
	t.Run("an unreadable audio config fails only a profile that names an audio model", func(t *testing.T) {
		dir := t.TempDir()
		writeConf(t, dir, "textConfig.json", &text.Default)
		if err := os.WriteFile(filepath.Join(dir, "audioConfig.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		env, err := newProfileEnv(dir)
		if err != nil {
			t.Fatalf("newProfileEnv: %v, want the audio table read only when asked", err)
		}
		if checked := checkProfileBody([]byte(`{"model":"test","prompt":"p"}`), env); len(checked.Problems) != 0 {
			t.Fatalf("Problems = %v, want a profile without audio unaffected", checked.Problems)
		}
		checked := checkProfileBody([]byte(`{"audio":{"model":"meeting"}}`), env)
		if len(checked.Problems) != 1 || !strings.Contains(checked.Problems[0], "audioConfig.json") {
			t.Fatalf("Problems = %v, want one naming audioConfig.json", checked.Problems)
		}
	})
}
