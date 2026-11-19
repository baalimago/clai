package tools

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestAudioTranscribeTool_VoicesSchema(t *testing.T) {
	spec := AudioTranscribe.Specification()
	props := slices.Sorted(maps.Keys(spec.Inputs.Properties))
	if !slices.Equal(props, []string{"file_path", "output_format", "voices"}) || !slices.Equal(spec.Inputs.Required, []string{"file_path"}) {
		t.Fatalf("properties = %v, required = %v; want voices beside the two, file_path alone required", props, spec.Inputs.Required)
	}
	if voices := spec.Inputs.Properties["voices"]; voices.Type != "array" || voices.Items == nil || voices.Items.Type != "string" {
		t.Fatalf("voices = %+v, want an array of strings", voices)
	}
}

func TestAudioTranscribeTool_Voices(t *testing.T) {
	const refusal = "voices must be an array of strings, the names of the people expected in the recording"
	for _, tc := range []struct {
		name    string
		input   pub_models.Input
		want    []string
		refused bool
	}{
		{name: "the names reach the engine as given", input: pub_models.Input{"voices": []any{"Lorentz", "Christoph"}}, want: []string{"Lorentz", "Christoph"}},
		{name: "a name the library may not hold is not checked", input: pub_models.Input{"voices": []string{"Nobody Known"}}, want: []string{"Nobody Known"}},
		{name: "blanks are trimmed and dropped", input: pub_models.Input{"voices": []any{" Lorentz ", "", " \t"}}, want: []string{"Lorentz"}},
		{name: "F3 no voices", input: pub_models.Input{}},
		{name: "null", input: pub_models.Input{"voices": nil}},
		{name: "an empty array", input: pub_models.Input{"voices": []any{}}},
		{name: "a string", input: pub_models.Input{"voices": "Lorentz"}, refused: true},
		{name: "an array of numbers", input: pub_models.Input{"voices": []any{"Lorentz", 1.0}}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []AudioTranscribeCall
			orig := AudioTranscribeEngine
			AudioTranscribeEngine = func(_ context.Context, call AudioTranscribeCall) (string, error) {
				calls = append(calls, call)
				return "the transcript", nil
			}
			t.Cleanup(func() { AudioTranscribeEngine = orig })
			tc.input["file_path"], tc.input["output_format"] = "f.wav", "json"

			got, err := AudioTranscribe.Call(tc.input)
			if tc.refused {
				if got != "" || err == nil || err.Error() != refusal || len(calls) != 0 {
					t.Fatalf("Call = %q, %v with the engine called %d times; want %q before the engine", got, err, len(calls), refusal)
				}
				return
			}
			want := []AudioTranscribeCall{{FilePath: "f.wav", OutputFormat: "json", Voices: tc.want}}
			if err != nil || got != "the transcript" || !reflect.DeepEqual(calls, want) {
				t.Fatalf("Call = %q, %v with the engine calls %+v; want the engine's result for %+v", got, err, calls, want)
			}
		})
	}
}
