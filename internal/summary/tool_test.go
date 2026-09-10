package summary

import (
	"strings"
	"testing"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSubmitSummary_validation(t *testing.T) {
	longTitle := strings.Repeat("t", TitleMaxRunes+1)
	longSummary := strings.Repeat("s", SummaryMaxRunes+1)
	tests := []struct {
		name  string
		input pub_models.Input
		want  string
	}{
		{"title missing", pub_models.Input{"summary": "ok"}, "title: required"},
		{"title not a string", pub_models.Input{"title": 1, "summary": "ok"}, "title: required"},
		{"title empty", pub_models.Input{"title": " \t ", "summary": "ok"}, "title: empty"},
		{"title newline", pub_models.Input{"title": "a\nb", "summary": "ok"}, "title: single line"},
		{"title too long", pub_models.Input{"title": longTitle, "summary": "ok"}, "title: at most 60 runes"},
		{"summary missing", pub_models.Input{"title": "ok"}, "summary: required"},
		{"summary not a string", pub_models.Input{"title": "ok", "summary": []string{"x"}}, "summary: required"},
		{"summary empty", pub_models.Input{"title": "ok", "summary": "\n"}, "summary: empty"},
		{"summary too long", pub_models.Input{"title": "ok", "summary": longSummary}, "summary: at most 240 runes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			holder := &submission{}
			tool := newSubmitSummaryTool(holder)
			out, err := tool.Call(tc.input)
			if err == nil {
				t.Fatalf("Call(%v) = %q, want error %q", tc.input, out, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if _, _, ok := holder.get(); ok {
				t.Fatalf("holder must stay empty after a rejection")
			}
		})
	}
}

func TestSubmitSummary_storesLastValid(t *testing.T) {
	holder := &submission{}
	tool := newSubmitSummaryTool(holder)
	out, err := tool.Call(pub_models.Input{"title": "  Fix   the\tbuild ", "summary": " It broke.\n\nNow it   works. "})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "accepted" {
		t.Fatalf("out = %q, want accepted", out)
	}
	title, sum, ok := holder.get()
	if !ok || title != "Fix the build" || sum != "It broke. Now it works." {
		t.Fatalf("holder = (%q, %q, %v), want collapsed values", title, sum, ok)
	}

	// Exactly at the limit, with multi-byte runes, is still valid.
	edge := strings.Repeat("é", TitleMaxRunes)
	if _, err := tool.Call(pub_models.Input{"title": edge, "summary": "Second."}); err != nil {
		t.Fatalf("Call at limit: %v", err)
	}
	title, sum, _ = holder.get()
	if utf8.RuneCountInString(title) != TitleMaxRunes || sum != "Second." {
		t.Fatalf("later submission must replace the earlier: (%q, %q)", title, sum)
	}

	spec := tool.Specification()
	if spec.Name != ToolName {
		t.Fatalf("spec name = %q, want %q", spec.Name, ToolName)
	}
	for _, want := range []string{"title", "summary"} {
		if _, ok := spec.Inputs.Properties[want]; !ok {
			t.Fatalf("spec lacks property %q", want)
		}
	}
	if len(spec.Inputs.Required) != 2 {
		t.Fatalf("required = %v, want both fields", spec.Inputs.Required)
	}
	if !strings.Contains(spec.Description, "60") || !strings.Contains(spec.Description, "240") {
		t.Fatalf("description must state both limits by value: %q", spec.Description)
	}
}
