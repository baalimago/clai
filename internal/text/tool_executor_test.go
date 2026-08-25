package text

import (
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_toolCallError(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{name: "error convention output", out: "ERROR: command exploded", wantErr: true},
		{name: "plain output", out: "42 files found", wantErr: false},
		{name: "empty output", out: "", wantErr: false},
		{name: "error marker mid-output", out: "the log said ERROR: nope", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := toolCallError(tt.out)
			if (err != nil) != tt.wantErr {
				t.Errorf("toolCallError(%q) = %v, wantErr %v", tt.out, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "command exploded") {
				t.Errorf("error lost the tool output: %v", err)
			}
		})
	}
}

func Test_finalizeAssistantTextPlain_EchoedToolCallDropped(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		Raw:  true, // raw display: no terminal clearing, no pretty print
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	e := toolExecutor[*MockQuerier]{querier: q}
	call := pub_models.Call{Name: "cat", Inputs: &pub_models.Input{}}
	session := &QuerySession{}

	if err := e.finalizeAssistantTextPlain(t.Context(), session, call.PrettyPrint(), call); err != nil {
		t.Fatalf("finalizeAssistantTextPlain: %v", err)
	}
	if session.FinalAssistantText != "" {
		t.Errorf("echoed tool-call text kept as assistant prose: %q", session.FinalAssistantText)
	}
	if q.fullMsg != "" {
		t.Errorf("echoed tool-call text kept in querier state: %q", q.fullMsg)
	}
}

func Test_finalizeAssistantTextPlain_ProseIsFinalized(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:              &out,
		outputModeKnown:  true,
		outputIsTerminal: true,
		dims:             dimensions.Dimensions{Width: 80, Height: 24},
	}
	e := toolExecutor[*MockQuerier]{querier: q}
	call := pub_models.Call{Name: "cat", Inputs: &pub_models.Input{}}
	session := &QuerySession{}

	if err := e.finalizeAssistantTextPlain(t.Context(), session, "let me check the file", call); err != nil {
		t.Fatalf("finalizeAssistantTextPlain: %v", err)
	}
	if session.FinalAssistantText != "let me check the file" {
		t.Errorf("FinalAssistantText = %q, want the streamed prose", session.FinalAssistantText)
	}
	if !strings.Contains(out.String(), "let me check the file") {
		t.Errorf("prose not re-printed as a proper message; out: %q", out.String())
	}
}
