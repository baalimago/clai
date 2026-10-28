package tools

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestAudioSources_Specification(t *testing.T) {
	spec := AudioSources.Specification()
	if spec.Name != "audio_sources" {
		t.Fatalf("Name = %q, want %q", spec.Name, "audio_sources")
	}
	if spec.Inputs == nil {
		t.Fatalf("Inputs nil")
	}
	if got := spec.Inputs.Type; got != "object" {
		t.Fatalf("Inputs.Type = %q, want object", got)
	}
	if len(spec.Inputs.Required) != 0 {
		t.Fatalf("Inputs.Required = %v, want empty", spec.Inputs.Required)
	}
	if len(spec.Inputs.Properties) != 0 {
		t.Fatalf("Inputs.Properties = %v, want empty", spec.Inputs.Properties)
	}
}

func TestAudioSources_Call(t *testing.T) {
	prev := AudioSourcesEngine
	t.Cleanup(func() { AudioSourcesEngine = prev })

	const listing = "system_audio: available\nmic \"mic0\" [Microphone] default\n"
	var gotCtx context.Context
	AudioSourcesEngine = func(ctx context.Context) (string, error) {
		gotCtx = ctx
		return listing, nil
	}

	out, err := AudioSources.CallWithContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("CallWithContext: %v", err)
	}
	if out != listing {
		t.Fatalf("CallWithContext text = %q, want the engine text", out)
	}
	if gotCtx == nil {
		t.Fatalf("engine did not receive a context")
	}

	gotCtx = nil
	out, err = AudioSources.Call(nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != listing {
		t.Fatalf("Call text = %q, want the engine text", out)
	}
	if gotCtx == nil || gotCtx.Err() != nil {
		t.Fatalf("Call must reach the engine with a live context, got %v", gotCtx)
	}

	sentinel := errors.New("backend down")
	AudioSourcesEngine = func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("%w: %v", sentinel, ctx.Err())
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AudioSources.CallWithContext(cancelled, nil); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the engine's error unchanged", err)
	}

	AudioSourcesEngine = nil
	_, err = AudioSources.CallWithContext(context.Background(), nil)
	if err == nil || err.Error() != "audio discovery is not available in this runtime" {
		t.Fatalf("error = %v, want the runtime error", err)
	}
}
