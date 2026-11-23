package records

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_e2e_audio_transcribe_tool_voices pins Flag.3 through the whole run: a
// query's -voices reaches the audio_transcribe tool, whose result carries the
// named segments and the naming report.
func Test_e2e_audio_transcribe_tool_voices(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	e2etest.SeedVoice(t, confDir, "Lorentz", 5, voices.Confirmed)
	e2etest.NamingStubs(t)
	e2etest.NamingScript(t, []e2etest.NamingPick{{Seconds: 5, Label: "X"}}, "X", "Y")
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Fields("-r -cm mock_test -t=audio_transcribe -am test-diarize -af json -voices Lorentz q tool_audio_transcribe"))
	})

	testboil.FailTestIfDiff(t, status, 0)
	for _, want := range []string{`"naming": {`, `"origin": "flag"`, "\"speaker\": \"Lorentz\",\n      \"voice\": {"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("tool result lacks %q:\nstdout %s\nstderr %s", want, stdout, stderr)
		}
	}
	if !strings.HasPrefix(stdout, "Call: 'audio_transcribe'") {
		t.Fatalf("the transcript was written raw to stdout: %q", stdout)
	}
}
