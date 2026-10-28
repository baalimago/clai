package audio

import (
	"testing"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func Test_audioSourcesEngineWired(t *testing.T) {
	if pkgtools.AudioSourcesEngine == nil {
		t.Fatalf("AudioSourcesEngine is nil; internal/audio init did not wire it")
	}
}

func Test_audioRecordEngineWired(t *testing.T) {
	if pkgtools.AudioRecordEngine == nil {
		t.Fatalf("AudioRecordEngine is nil; internal/audio init did not wire it")
	}
}
