package text

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const verificationOutputRuneLimit = 4000

// LoopStatus classifies the terminal state reported by the complete_task tool. (F1)
type LoopStatus string

const (
	LoopStatusNone     LoopStatus = ""
	LoopStatusAchieved LoopStatus = "achieved"
	LoopStatusBlocked  LoopStatus = "blocked"
)

// LoopSubmission holds the validated outcome recorded by a CompletionRecorder. (F1)
type LoopSubmission struct {
	Status  LoopStatus
	Summary string
}

// CompletionRecorder is an LLMTool that validates and records one loop
// iteration's terminal completion call. (F1, F2)
type CompletionRecorder interface {
	pub_models.LLMTool

	// CallWithContext validates input, runs the optional verification command
	// when status is achieved, and records the submission on success.
	CallWithContext(ctx context.Context, input pub_models.Input) (string, error)

	// Submission returns the last valid LoopSubmission recorded since creation
	// or the latest Reset. Safe for concurrent use.
	Submission() (LoopSubmission, bool)

	// Reset clears any recorded submission. Safe for concurrent use.
	Reset()
}

type completionRecorder struct {
	mu         sync.Mutex
	checkCmd   string
	submission LoopSubmission
	recorded   bool
}

// NewCompletionRecorder returns a CompletionRecorder gated by checkCmd.
// Its Specification advertises complete_task; Call uses context.Background(). (F1, F2)
func NewCompletionRecorder(checkCmd string) CompletionRecorder {
	return &completionRecorder{checkCmd: strings.TrimSpace(checkCmd)}
}

func (r *completionRecorder) Call(input pub_models.Input) (string, error) {
	return r.CallWithContext(context.Background(), input)
}

func (r *completionRecorder) Specification() pub_models.Specification {
	statuses := []string{string(LoopStatusAchieved), string(LoopStatusBlocked)}
	return pub_models.Specification{
		Name:        "complete_task",
		Description: "Finish the loop task. Call it when the goal is achieved or you are blocked. An achieved call is rejected when a configured verification command fails; fix the failure and call it again once the command passes.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"status", "summary"},
			Properties: map[string]pub_models.ParameterObject{
				"status": {
					Type:        "string",
					Description: "achieved when the goal is complete; blocked when you cannot continue.",
					Enum:        &statuses,
				},
				"summary": {
					Type:        "string",
					Description: "Non-empty summary of what was achieved or why the task is blocked.",
				},
			},
		},
	}
}

func (r *completionRecorder) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	status, err := parseLoopStatus(input)
	if err != nil {
		return "", err
	}
	summary, err := parseLoopSummary(input)
	if err != nil {
		return "", err
	}
	if status == LoopStatusAchieved {
		if err := r.verify(ctx); err != nil {
			return "", err
		}
	}

	r.mu.Lock()
	r.submission = LoopSubmission{Status: status, Summary: summary}
	r.recorded = true
	r.mu.Unlock()
	return "recorded", nil
}

func (r *completionRecorder) Submission() (LoopSubmission, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submission, r.recorded
}

func (r *completionRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submission = LoopSubmission{}
	r.recorded = false
}

func (r *completionRecorder) verify(ctx context.Context) error {
	if r.checkCmd == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "sh", "-c", r.checkCmd).CombinedOutput()
	if err == nil {
		return nil
	}
	return fmt.Errorf("verification command %q failed: %w; output: %s", r.checkCmd, err, boundedVerificationOutput(string(out)))
}

func parseLoopStatus(input pub_models.Input) (LoopStatus, error) {
	raw, ok := input["status"]
	if !ok {
		return LoopStatusNone, errors.New("status: required")
	}
	var value string
	switch typed := raw.(type) {
	case string:
		value = typed
	case LoopStatus:
		value = string(typed)
	default:
		return LoopStatusNone, errors.New("status: must be a string")
	}
	status := LoopStatus(strings.TrimSpace(value))
	switch status {
	case LoopStatusAchieved, LoopStatusBlocked:
		return status, nil
	case LoopStatusNone:
		return LoopStatusNone, errors.New("status: required")
	default:
		return LoopStatusNone, fmt.Errorf("status: %q is invalid, want %q or %q", value, LoopStatusAchieved, LoopStatusBlocked)
	}
}

func parseLoopSummary(input pub_models.Input) (string, error) {
	raw, ok := input["summary"]
	if !ok {
		return "", errors.New("summary: required")
	}
	value, ok := raw.(string)
	if !ok {
		return "", errors.New("summary: must be a string")
	}
	summary := strings.TrimSpace(value)
	if summary == "" {
		return "", errors.New("summary: empty")
	}
	return summary, nil
}

func boundedVerificationOutput(out string) string {
	if strings.TrimSpace(out) == "" {
		return "(no output)"
	}
	if utf8.RuneCountInString(out) <= verificationOutputRuneLimit {
		return out
	}
	return string([]rune(out)[:verificationOutputRuneLimit]) + "\n...[truncated]"
}
