package agent

import (
	"testing"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// Test_audioTranscribeEngineWired pins the mode-as-tool bridge: importing
// pkg/agent must link package internal, whose init wires the
// audio_transcribe engine. Guards the blank internal import in agent.go.
func Test_audioTranscribeEngineWired(t *testing.T) {
	if pkgtools.AudioTranscribeEngine == nil {
		t.Fatal("audio_transcribe engine not wired; package internal's init did not run")
	}
}
