package tools

import (
	"os/exec"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func TestRegisterLocalTools_AudioSourcesGate(t *testing.T) {
	requireAudioToolGate(t, pkgtools.AudioSources)
}

func TestRegisterLocalTools_AudioRecordGate(t *testing.T) {
	requireAudioToolGate(t, pkgtools.AudioRecord)
}

func requireAudioToolGate(t *testing.T, tool pub_models.LLMTool) {
	t.Helper()
	name := tool.Specification().Name
	reg := NewRegistry()

	registerLocalTools(reg, func(name string) (string, error) {
		if name == "ffmpeg" {
			return "/bin/ffmpeg", nil
		}
		return "", exec.ErrNotFound
	})
	if _, ok := reg.Get(name); !ok {
		t.Fatalf("expected %s to be registered when ffmpeg lookup succeeds", name)
	}

	reg = NewRegistry()
	registerLocalTools(reg, func(name string) (string, error) {
		return "", exec.ErrNotFound
	})
	if _, ok := reg.Get(name); ok {
		t.Fatalf("expected %s to be absent when ffmpeg lookup fails", name)
	}
}
