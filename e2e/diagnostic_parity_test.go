package e2e

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/diag/diagfake"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/pkg/agent"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var timestampPattern = regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}|\d{2}:\d{2}:\d{2}\.\d+`)

// TestDiagnosticExecutableParity proves criterion 6: the executable path,
// cli.RunProfiled as main.go calls it, and the in-process cli.Run apply the
// same policy to stdout and stderr, both over cli.DefaultDeps.
func TestDiagnosticExecutableParity(t *testing.T) {
	for _, args := range []string{
		"-r -cm test -t no_such_tool q hello",
		"-r -cm test -p no_such_profile q hi",
		"-json -r -cm test -t no_such_tool q hello",
	} {
		t.Run(args, func(t *testing.T) {
			e2etest.SetupMainTestConfigDir(t)
			t.Setenv("DEBUG_LOOKBACK", "1")
			h := &e2etest.Harness{Deps: cli.DefaultDeps()}
			h.Deps.NewSummarizer = e2etest.RefusingSummarizer
			var inStatus, exeStatus int
			inOut, inErr := e2etest.CaptureStdoutStderr(t, func() { inStatus = h.Run(strings.Fields(args)) })
			exeOut, exeErr := e2etest.CaptureStdoutStderr(t, func() { exeStatus = h.RunProfiled(strings.Fields(args)) })
			if inStatus != exeStatus || inOut != exeOut || inErr != exeErr {
				t.Fatalf("paths differ:\nRun        %d stdout %q stderr %q\nRunProfiled %d stdout %q stderr %q", inStatus, inOut, inErr, exeStatus, exeOut, exeErr)
			}
			if inErr == "" {
				t.Fatalf("the scenario reported nothing on stderr")
			}
			if strings.Contains(exeOut+exeErr, "\x1b") || timestampPattern.MatchString(exeErr) {
				t.Fatalf("the executable path adds color or timestamps in a pipe: %q", exeErr)
			}
		})
	}
}

// isolationReporter is the real routing Reporter over the agent's
// diagnostic types, every one shown, delivering to a recording fake that
// runs the Deliverer contract suite.
func isolationReporter(t *testing.T) (diagnostic.Reporter, *diagfake.Deliverer) {
	t.Helper()
	destination := diagfake.New()
	levels := diag.ResolvedLevels{"": diagnostic.LevelTrace}
	for _, diagnosticType := range agent.DiagnosticTypes() {
		levels[diagnosticType.Subsystem] = diagnostic.LevelTrace
	}
	reporter, err := diag.NewReporter(agent.DiagnosticTypes(), levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: destination,
		diagnostic.Buffered:  destination,
	})
	if err != nil {
		t.Fatalf("build the isolation reporter: %v", err)
	}
	return reporter, destination
}

var errToolBroke = errors.New("tool broke on purpose")

type failingTool struct{}

func (failingTool) Call(pub_models.Input) (string, error) { return "", errToolBroke }

func (failingTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "failing",
		Description: "A tool that always fails.",
		Inputs:      &pub_models.InputSchema{Type: "object", Properties: map[string]pub_models.ParameterObject{}},
	}
}

// TestDiagnosticAgentIsolation proves criterion 7: an agent without a
// Reporter writes nothing to the process streams, an agent with one reports
// to it, and a failed tool call reaches the model on the existing tool error
// path, not through the diagnostic Reporter.
func TestDiagnosticAgentIsolation(t *testing.T) {
	// Agent.Setup turns the chat index off for the process; the CLI tests
	// that run after this one need it.
	skipIndex := chat.SkipIndex
	t.Cleanup(func() { chat.SkipIndex = skipIndex })
	run := func(t *testing.T, opts ...agent.Option) (pub_models.Chat, string, string) {
		t.Helper()
		e2etest.SetupMainTestConfigDir(t)
		a := agent.New(append([]agent.Option{
			agent.WithConfigDir(t.TempDir()),
			agent.WithModel("mock_test"),
			agent.WithTools([]pub_models.LLMTool{failingTool{}}),
			agent.WithMaxToolCalls(2),
		}, opts...)...)
		var chat pub_models.Chat
		var queryErr error
		stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
			ctx := context.Background()
			if err := a.Setup(ctx); err != nil {
				queryErr = err
				return
			}
			chat, queryErr = a.Query(ctx, pub_models.Chat{
				Created:  time.Now(),
				ID:       "agent-isolation",
				Messages: []pub_models.Message{{Role: "user", Content: "please tool_failing"}},
			})
		})
		if queryErr != nil {
			t.Fatalf("agent run: %v", queryErr)
		}
		return chat, stdout, stderr
	}
	toolError := func(t *testing.T, chat pub_models.Chat) string {
		t.Helper()
		for _, m := range chat.Messages {
			if m.Role == "tool" {
				return m.Content
			}
		}
		t.Fatalf("no tool result in the chat: %+v", chat.Messages)
		return ""
	}

	t.Run("no Reporter is silent", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		chat, stdout, stderr := run(t)
		if stdout != "" || stderr != "" {
			t.Fatalf("an agent without a Reporter wrote stdout %q stderr %q", stdout, stderr)
		}
		if got := toolError(t, chat); !strings.Contains(got, errToolBroke.Error()) {
			t.Fatalf("the tool result %q does not carry the tool's error", got)
		}
	})
	t.Run("a Reporter receives diagnostics, not the tool error", func(t *testing.T) {
		reporter, destination := isolationReporter(t)
		chat, stdout, stderr := run(t, agent.WithDiagnosticReporter(reporter))
		if stdout != "" || stderr != "" {
			t.Fatalf("an agent with a Reporter wrote stdout %q stderr %q", stdout, stderr)
		}
		got := toolError(t, chat)
		if !strings.Contains(got, "ERROR: ") || !strings.Contains(got, errToolBroke.Error()) {
			t.Fatalf("the tool result %q is not the existing tool error", got)
		}
		for _, e := range destination.Attempts() {
			if strings.Contains(e.Message, errToolBroke.Error()) {
				t.Fatalf("the tool error was routed through the Reporter: %+v", e)
			}
		}
	})
}
