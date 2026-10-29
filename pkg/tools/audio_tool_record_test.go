package tools

import (
	"context"
	"errors"
	"testing"
)

func TestAudioRecord_Specification(t *testing.T) {
	spec := AudioRecord.Specification()
	if spec.Name != "audio_record" {
		t.Fatalf("Name = %q, want audio_record", spec.Name)
	}
	if spec.Inputs == nil || spec.Inputs.Type != "object" {
		t.Fatalf("Inputs = %+v, want an object schema", spec.Inputs)
	}
	if len(spec.Inputs.Required) != 0 {
		t.Fatalf("Required = %v, want empty", spec.Inputs.Required)
	}
	if len(spec.Inputs.Properties) != 7 {
		t.Fatalf("Properties = %d, want 7", len(spec.Inputs.Properties))
	}
	sinks := spec.Inputs.Properties["sinks"]
	if sinks.Enum == nil || len(*sinks.Enum) != 3 {
		t.Fatalf("sinks enum = %v, want three values", sinks.Enum)
	}
	devices := spec.Inputs.Properties["devices"]
	if devices.Items == nil || devices.Items.Type != "string" {
		t.Fatalf("devices items = %+v, want strings", devices.Items)
	}
}

func TestAudioRecord_Call(t *testing.T) {
	t.Run("bad arguments never reach the engine", func(t *testing.T) {
		called := false
		useAudioRecordEngine(t, func(context.Context, AudioRecordCall) (string, error) {
			called = true
			return "", nil
		})

		cases := []struct {
			name  string
			input map[string]any
			want  string
		}{
			{name: "devices not an array", input: map[string]any{"devices": "mic"}, want: "devices must be an array"},
			{name: "devices entry not a string", input: map[string]any{"devices": []any{"a", 7}}, want: "devices[1] must be a non-empty string"},
			{name: "devices entry empty", input: map[string]any{"devices": []any{""}}, want: "devices[0] must be a non-empty string"},
			{name: "max_seconds", input: map[string]any{"max_seconds": "10"}, want: "max_seconds must be an integer"},
			{name: "max_seconds out of range", input: map[string]any{"max_seconds": 1e300}, want: "max_seconds is out of range"},
			{name: "silence_timeout_seconds", input: map[string]any{"silence_timeout_seconds": 1.5}, want: "silence_timeout_seconds must be an integer"},
			{name: "name_suffix", input: map[string]any{"name_suffix": 1}, want: "name_suffix must be a string"},
			{name: "dir", input: map[string]any{"dir": 1}, want: "dir must be a string"},
			{name: "sinks", input: map[string]any{"sinks": 1}, want: "sinks must be a string"},
			{name: "mic", input: map[string]any{"mic": "true"}, want: "mic must be a boolean"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				called = false
				_, err := AudioRecord.CallWithContext(context.Background(), tc.input)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v, want %q", err, tc.want)
				}
				if called {
					t.Fatalf("engine ran on a bad argument")
				}
			})
		}
	})

	t.Run("arguments reach the engine with the call context", func(t *testing.T) {
		type key struct{}
		ctx := context.WithValue(context.Background(), key{}, "value")
		mic := false
		useAudioRecordEngine(t, func(engineCtx context.Context, call AudioRecordCall) (string, error) {
			if engineCtx.Value(key{}) != "value" {
				t.Fatalf("engine did not receive the call context")
			}
			if len(call.Devices) != 2 || call.Devices[0] != "a" || call.Devices[1] != "b" {
				t.Fatalf("Devices = %v", call.Devices)
			}
			if call.MaxSeconds != 10 || call.SilenceTimeout != 20 {
				t.Fatalf("seconds = %d/%d", call.MaxSeconds, call.SilenceTimeout)
			}
			if call.NameSuffix != "standup" || call.Dir != "/tmp/recordings" || call.Sinks != "none" {
				t.Fatalf("call = %+v", call)
			}
			if call.Mic == nil || *call.Mic != mic {
				t.Fatalf("Mic = %v, want false", call.Mic)
			}
			return "facts", nil
		})

		out, err := AudioRecord.CallWithContext(ctx, map[string]any{
			"devices":                 []any{"a", "b"},
			"max_seconds":             float64(10),
			"silence_timeout_seconds": float64(20),
			"name_suffix":             "standup",
			"dir":                     "/tmp/recordings",
			"sinks":                   "none",
			"mic":                     mic,
		})
		if err != nil {
			t.Fatalf("CallWithContext: %v", err)
		}
		if out != "facts" {
			t.Fatalf("out = %q, want the engine text", out)
		}
	})

	t.Run("engine error passes through", func(t *testing.T) {
		sentinel := errors.New("backend down")
		useAudioRecordEngine(t, func(context.Context, AudioRecordCall) (string, error) {
			return "", sentinel
		})
		if _, err := AudioRecord.Call(nil); !errors.Is(err, sentinel) {
			t.Fatalf("error = %v, want the engine error", err)
		}
	})

	t.Run("uninstalled engine", func(t *testing.T) {
		useAudioRecordEngine(t, nil)
		_, err := AudioRecord.CallWithContext(context.Background(), nil)
		if err == nil || err.Error() != "audio capture is not available in this runtime" {
			t.Fatalf("error = %v, want the runtime error", err)
		}
	})
}

func useAudioRecordEngine(t *testing.T, engine func(context.Context, AudioRecordCall) (string, error)) {
	t.Helper()
	previous := AudioRecordEngine
	AudioRecordEngine = engine
	t.Cleanup(func() { AudioRecordEngine = previous })
}
