package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type notificationTestCompletion struct {
	suppress bool
}

func (c notificationTestCompletion) SuppressCompletionNotification() bool {
	return c.suppress
}

func Test_triggerCompletionNotification(t *testing.T) {
	t.Run("emits bell when completion does not suppress it", func(t *testing.T) {
		gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
			triggerCompletionNotification(notificationTestCompletion{})
		})
		testboil.FailTestIfDiff(t, gotStdout, "\a")
	})

	t.Run("suppressed completion emits nothing", func(t *testing.T) {
		gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
			triggerCompletionNotification(notificationTestCompletion{suppress: true})
		})
		testboil.FailTestIfDiff(t, gotStdout, "")
	})
}

type stubQuerier struct {
	called bool
	err    error
}

func (s *stubQuerier) Query(context.Context) error {
	s.called = true
	return s.err
}

// Test_Command_adapter pins the adapter mechanics: memoized flagset with
// registered groups, parent-shared Flags on subcommands, Setup populating
// Conf/Args, Help composing the flag list, and the default Run driving the
// querier.
func Test_Command_adapter(t *testing.T) {
	newCmd := func() (*Command, *RawFlag) {
		raw := &RawFlag{}
		return &Command{
			Name:     "stub",
			Desc:     "stub command",
			HelpText: "stub help",
			Register: raw.Register,
			Raw:      raw,
		}, raw
	}

	t.Run("flagset is memoized and carries registered flags", func(t *testing.T) {
		c, _ := newCmd()
		fs := c.Flagset()
		if fs != c.Flagset() {
			t.Fatal("Flagset must be memoized")
		}
		if fs.Lookup("r") == nil || fs.Lookup("raw") == nil {
			t.Fatal("registered raw group missing from flagset")
		}
	})

	t.Run("setup populates args and session globals", func(t *testing.T) {
		c, raw := newCmd()
		if err := c.Flagset().Parse([]string{"-r", "positional"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if !raw.Value() {
			t.Fatal("expected -r to set the raw flag")
		}
		if !utils.ReadonlyConfig {
			t.Fatal("expected raw run to set utils.ReadonlyConfig")
		}
		wantArgs := []string{"stub", "positional"}
		if len(c.Args()) != 2 || c.Args()[0] != wantArgs[0] || c.Args()[1] != wantArgs[1] {
			t.Fatalf("args: got %v want %v", c.Args(), wantArgs)
		}
	})

	t.Run("subcommand shares the tree's flag values", func(t *testing.T) {
		parent, raw := newCmd()
		sub := &Command{
			Name:     "sub",
			Register: raw.Register,
			Raw:      raw,
		}
		if err := parent.Flagset().Parse([]string{"-r"}); err != nil {
			t.Fatalf("parent Parse: %v", err)
		}
		if err := sub.Flagset().Parse(nil); err != nil {
			t.Fatalf("sub Parse: %v", err)
		}
		if err := sub.Setup(context.Background()); err != nil {
			t.Fatalf("sub Setup: %v", err)
		}
		if !raw.Value() {
			t.Fatal("parent-level -r must be visible to the executing sub")
		}
	})

	t.Run("help appends the flag list", func(t *testing.T) {
		c, _ := newCmd()
		help := c.Help()
		if !strings.Contains(help, "stub help") || !strings.Contains(help, "-raw") {
			t.Fatalf("help must contain text and flag list, got: %q", help)
		}
		if c.Describe() != "stub command" {
			t.Fatalf("describe: got %q", c.Describe())
		}
	})

	t.Run("default run drives the querier", func(t *testing.T) {
		c, _ := newCmd()
		q := &stubQuerier{}
		var _ models.Querier = q
		c.SetQuerier(q)
		_ = testboil.CaptureStdout(t, func(t *testing.T) {
			if err := c.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
		if !q.called {
			t.Fatal("expected querier to be queried")
		}
	})

	t.Run("completion hooks default to nil-safe", func(t *testing.T) {
		c, _ := newCmd()
		if got := c.CompleteFlagValue("r", ""); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
		if got := c.CompleteArgs(nil, ""); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}
