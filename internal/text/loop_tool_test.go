package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func loopInput(status, summary string) pub_models.Input {
	return pub_models.Input{"status": status, "summary": summary}
}

func TestCompletionRecorder_AchievedWithoutCheck(t *testing.T) {
	recorder := NewCompletionRecorder("")

	got, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "  Mock loop\nsummary.  "))
	if err != nil {
		t.Fatalf("CallWithContext: %v", err)
	}
	if got != "recorded" {
		t.Fatalf("confirmation = %q, want recorded", got)
	}

	submission, ok := recorder.Submission()
	if !ok {
		t.Fatal("Submission() = false, want a recorded submission")
	}
	if submission.Status != LoopStatusAchieved {
		t.Fatalf("status = %q, want %q", submission.Status, LoopStatusAchieved)
	}
	if submission.Summary != "Mock loop\nsummary." {
		t.Fatalf("summary = %q, want the outer whitespace trimmed", submission.Summary)
	}

	typed := NewCompletionRecorder("")
	if _, err := typed.CallWithContext(t.Context(), pub_models.Input{"status": LoopStatusAchieved, "summary": "done"}); err != nil {
		t.Fatalf("typed status: %v", err)
	}
}

func TestCompletionRecorder_CheckGate(t *testing.T) {
	t.Run("failing command rejects", func(t *testing.T) {
		recorder := NewCompletionRecorder("printf boom; exit 3")

		_, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "done"))
		if err == nil {
			t.Fatal("CallWithContext returned nil, want a verification error")
		}
		if !strings.Contains(err.Error(), "verification") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("error = %q, want the verification failure and its output", err)
		}
		if _, ok := recorder.Submission(); ok {
			t.Fatal("Submission() recorded a failed verification")
		}
	})

	t.Run("oversized output is truncated", func(t *testing.T) {
		outputPath := filepath.Join(t.TempDir(), "output")
		if err := os.WriteFile(outputPath, []byte(strings.Repeat("z", verificationOutputRuneLimit+1000)), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		recorder := NewCompletionRecorder("cat '" + outputPath + "'; exit 1")

		_, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "done"))
		if err == nil {
			t.Fatal("CallWithContext returned nil, want a verification error")
		}
		if !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("error does not mention truncation: %q", err)
		}
		if utf8.RuneCountInString(err.Error()) >= verificationOutputRuneLimit+1000 {
			t.Fatalf("error is %d runes, want the output capped", utf8.RuneCountInString(err.Error()))
		}
	})

	t.Run("passing command records", func(t *testing.T) {
		recorder := NewCompletionRecorder("exit 0")

		if _, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "done")); err != nil {
			t.Fatalf("CallWithContext: %v", err)
		}
		if submission, ok := recorder.Submission(); !ok || submission.Status != LoopStatusAchieved {
			t.Fatalf("Submission() = (%+v, %v), want achieved", submission, ok)
		}
	})

	t.Run("cancelled context rejects", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		recorder := NewCompletionRecorder("exit 0")

		_, err := recorder.CallWithContext(ctx, loopInput("achieved", "done"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
		if _, ok := recorder.Submission(); ok {
			t.Fatal("Submission() recorded a cancelled verification")
		}
	})

	t.Run("rejected achieved call can be retried", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "verified")
		recorder := NewCompletionRecorder("if [ -f '" + marker + "' ]; then exit 0; fi; touch '" + marker + "'; exit 1")

		if _, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "done")); err == nil {
			t.Fatal("first CallWithContext returned nil, want the verification error")
		}
		if _, ok := recorder.Submission(); ok {
			t.Fatal("rejected achieved call recorded a submission")
		}

		if _, err := recorder.CallWithContext(t.Context(), loopInput("achieved", "done")); err != nil {
			t.Fatalf("retried CallWithContext: %v", err)
		}
		if submission, ok := recorder.Submission(); !ok || submission.Status != LoopStatusAchieved {
			t.Fatalf("Submission() = (%+v, %v), want the retried achieved call", submission, ok)
		}
	})
}

func TestCompletionRecorder_BlockedSkipsCheck(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	recorder := NewCompletionRecorder("touch '" + marker + "'; exit 1")

	if _, err := recorder.CallWithContext(t.Context(), loopInput("blocked", "  Need credentials.  ")); err != nil {
		t.Fatalf("CallWithContext: %v", err)
	}
	if submission, ok := recorder.Submission(); !ok || submission.Status != LoopStatusBlocked || submission.Summary != "Need credentials." {
		t.Fatalf("Submission() = (%+v, %v), want blocked with the trimmed summary", submission, ok)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker stat error = %v, want the check command not to run", err)
	}
}

func TestCompletionRecorder_Validation(t *testing.T) {
	cases := []struct {
		name      string
		input     pub_models.Input
		wantField string
	}{
		{"missing status", pub_models.Input{"summary": "done"}, "status"},
		{"non-string status", pub_models.Input{"status": 1, "summary": "done"}, "status"},
		{"unknown status", pub_models.Input{"status": "done", "summary": "done"}, "status"},
		{"blank status", pub_models.Input{"status": "  ", "summary": "done"}, "status"},
		{"missing summary", pub_models.Input{"status": "achieved"}, "summary"},
		{"non-string summary", pub_models.Input{"status": "achieved", "summary": 1}, "summary"},
		{"blank summary", pub_models.Input{"status": "achieved", "summary": " \n\t "}, "summary"},
	}

	seeded := NewCompletionRecorder("")
	if _, err := seeded.CallWithContext(t.Context(), loopInput("achieved", "prior")); err != nil {
		t.Fatalf("seed CallWithContext: %v", err)
	}
	prior, ok := seeded.Submission()
	if !ok {
		t.Fatal("seed Submission() = false")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := NewCompletionRecorder("")
			_, err := fresh.CallWithContext(t.Context(), tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("error = %v, want it to name %q", err, tc.wantField)
			}
			if _, ok := fresh.Submission(); ok {
				t.Fatal("invalid input recorded a submission")
			}

			if _, err := seeded.CallWithContext(t.Context(), tc.input); err == nil {
				t.Fatal("invalid input was accepted")
			}
			if got, ok := seeded.Submission(); !ok || got != prior {
				t.Fatalf("Submission() = (%+v, %v), want the prior submission untouched", got, ok)
			}
		})
	}
}

func TestCompletionRecorder_ResetAndSpec(t *testing.T) {
	recorder := NewCompletionRecorder("")

	spec := recorder.Specification()
	if spec.Name != "complete_task" {
		t.Fatalf("name = %q, want complete_task", spec.Name)
	}
	if spec.Inputs == nil {
		t.Fatal("Specification inputs = nil")
	}
	if spec.Inputs.Type != "object" {
		t.Fatalf("schema type = %q, want object", spec.Inputs.Type)
	}
	if len(spec.Inputs.Required) != 2 {
		t.Fatalf("required = %v, want status and summary", spec.Inputs.Required)
	}
	required := map[string]bool{}
	for _, field := range spec.Inputs.Required {
		required[field] = true
	}
	if !required["status"] || !required["summary"] {
		t.Fatalf("required = %v, want status and summary", spec.Inputs.Required)
	}
	statusSpec, ok := spec.Inputs.Properties["status"]
	if !ok || statusSpec.Type != "string" || statusSpec.Enum == nil {
		t.Fatalf("status property = %+v, want a string enum", statusSpec)
	}
	if got, want := strings.Join(*statusSpec.Enum, ","), "achieved,blocked"; got != want {
		t.Fatalf("status enum = %q, want %q", got, want)
	}
	if summarySpec, ok := spec.Inputs.Properties["summary"]; !ok || summarySpec.Type != "string" {
		t.Fatalf("summary property = %+v, want a string", summarySpec)
	}

	if _, err := recorder.Call(loopInput("achieved", "done")); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if _, ok := recorder.Submission(); !ok {
		t.Fatal("Call did not record a submission")
	}
	recorder.Reset()
	if _, ok := recorder.Submission(); ok {
		t.Fatal("Reset did not clear the submission")
	}
}
