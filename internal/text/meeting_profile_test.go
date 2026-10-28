package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The meeting-transcriber profile's prompt is the workflow a model follows,
// so each clause is pinned here instead of only in prose (worklog
// 2026-10-28-audio-record-tool, profile.2-profile.8).
func Test_meeting_transcriber_profile_prompt(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "profiles", "meeting-transcriber.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	prompt := profile.Prompt

	for _, want := range []struct {
		clause string
		text   string
	}{
		{"profile.2", "Call audio_sources first"},
		{"profile.2", "pass exactly that name in devices"},
		{"profile.2", "let the default markers decide"},
		{"profile.3", `mic: true and sinks: "none"`},
		{"profile.3", `mic: true and sinks: "default"`},
		{"profile.3", `mic: false and sinks: "default"`},
		{"profile.4", "name_suffix"},
		{"profile.4", "Enter stops the recording"},
		{"profile.4", "duration_seconds"},
		{"profile.4", "Report those six facts"},
		{"profile.5", "output_format \"json\""},
		{"profile.5", "byte for byte"},
		{"profile.7", "audio_record is not among your tools"},
		{"profile.7", "ffmpeg"},
		{"profile.7", "linux or macOS"},
		{"profile.8", "show the error exactly as it is and stop"},
		{"profile.8", "system_audio: unavailable"},
		{"profile.8", "record the microphone alone"},
	} {
		if !strings.Contains(prompt, want.text) {
			t.Errorf("%s: prompt does not contain %q", want.clause, want.text)
		}
	}

	order := []string{
		"Call audio_sources first",
		"Call audio_record",
		"output_format \"json\"",
		"byte for byte",
		"render a readable transcript",
		"write the meeting notes",
	}
	previous := -1
	for _, step := range order {
		at := strings.Index(prompt, step)
		if at < 0 {
			t.Fatalf("prompt does not contain %q", step)
		}
		if at <= previous {
			t.Fatalf("prompt step %q is not after the previous step", step)
		}
		previous = at
	}

	// profile.6: organization, directory, index and notebook service policy
	// belong to the user's message and the user's MCP servers, not the file.
	if strings.Contains(prompt, "/") {
		t.Errorf("prompt encodes a directory literal: %q", prompt)
	}
	if strings.Contains(strings.ToLower(prompt), "slivingdoc") {
		t.Errorf("prompt names a notebook service: %q", prompt)
	}
	if strings.Contains(strings.ToLower(prompt), "index") {
		t.Errorf("prompt carries an index rule: %q", prompt)
	}
}
