package agent

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/diag/diagfake"

	"github.com/baalimago/clai/internal/diag"
	privmodels "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/clai/pkg/text/models"
)

var errReportFailed = errors.New("report failed")

// newRecordingReporter is the real routing Reporter over the agent's
// diagnostic types, every one shown, delivering to a recording fake that
// runs the Deliverer contract suite; deliveryErr makes every delivery fail.
func newRecordingReporter(t *testing.T, deliveryErr error) (diagnostic.Reporter, *diagfake.Deliverer) {
	t.Helper()
	destination := diagfake.New()
	if deliveryErr != nil {
		destination = diagfake.Failing(deliveryErr)
	}
	levels := diag.ResolvedLevels{"": diagnostic.LevelTrace}
	for _, diagnosticType := range DiagnosticTypes() {
		levels[diagnosticType.Subsystem] = diagnostic.LevelTrace
	}
	reporter, err := diag.NewReporter(DiagnosticTypes(), levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: destination,
		diagnostic.Buffered:  destination,
	})
	if err != nil {
		t.Fatalf("build the recording reporter: %v", err)
	}
	return reporter, destination
}

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

type diagnosticsQuerier struct {
	reply models.Chat
	err   error
}

func (q diagnosticsQuerier) Query(context.Context) error { return q.err }

func (q diagnosticsQuerier) TextQuery(context.Context, models.Chat) (models.Chat, error) {
	return q.reply, q.err
}

// querierScenario is what the fake text querier does and where the agent
// keeps its configuration.
type querierScenario struct {
	configDir string
	createErr error
	querier   diagnosticsQuerier
	created   *text.Configurations
}

type setupQuerierFactory func(scenario querierScenario, options ...Option) SetupQuerier

func setupQuerierImplementations() map[string]setupQuerierFactory {
	return map[string]setupQuerierFactory{
		"Agent": func(scenario querierScenario, options ...Option) SetupQuerier {
			a := New(append([]Option{WithConfigDir(scenario.configDir)}, options...)...)
			a.querierCreator = func(_ context.Context, conf text.Configurations) (privmodels.Querier, error) {
				if scenario.created != nil {
					*scenario.created = conf
				}
				if scenario.createErr != nil {
					return nil, scenario.createErr
				}
				return scenario.querier, nil
			}
			return &a
		},
	}
}

func replyChat() models.Chat {
	return models.Chat{
		ID: "chat",
		Messages: []models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}
}

// producingScenario makes Setup fail to create the configuration
// directories, which the agent reports, and Query succeed.
func producingScenario(t *testing.T) querierScenario {
	t.Helper()
	return querierScenario{
		configDir: filepath.Join(t.TempDir(), "missing", "parent"),
		querier:   diagnosticsQuerier{reply: replyChat()},
	}
}

func runSetupAndQuery(t *testing.T, sq SetupQuerier) (models.Chat, error, error) {
	t.Helper()
	setupErr := sq.Setup(context.Background())
	if setupErr != nil {
		return models.Chat{}, setupErr, nil
	}
	chat, queryErr := sq.Query(context.Background(), models.Chat{ID: "chat"})
	return chat, nil, queryErr
}

func eventTypes(events []diagnostic.Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Type)
	}
	return names
}

func containsType(events []diagnostic.Event, name string) bool {
	for _, event := range events {
		if event.Type == name {
			return true
		}
	}
	return false
}

// TestAgentDiagnosticsContract proves WithDiagnosticReporter.1, Setup.1,
// Setup.2, Query.1, and Query.2.
func TestAgentDiagnosticsContract(t *testing.T) {
	t.Parallel()
	for name, factory := range setupQuerierImplementations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("WithDiagnosticReporter.1 Setup.1 Query.1: the supplied Reporter receives setup and query events", func(t *testing.T) {
				t.Parallel()
				reporter, recorded := newRecordingReporter(t, nil)
				chat, setupErr, queryErr := runSetupAndQuery(t, factory(producingScenario(t), WithDiagnosticReporter(reporter)))
				if setupErr != nil || queryErr != nil {
					t.Fatalf("unexpected errors: setup %v, query %v", setupErr, queryErr)
				}
				events := recorded.Events()
				for _, want := range []string{diagnosticConfigDirUnavailable, diagnosticSetupCompleted, diagnosticQueryCompleted} {
					if !containsType(events, want) {
						t.Fatalf("event %q not reported; got %v", want, eventTypes(events))
					}
				}
				if !reflect.DeepEqual(chat, replyChat()) {
					t.Fatalf("Query.1: diagnostics changed the chat sent back: %#v", chat)
				}
			})

			t.Run("Setup.1 Query.1: diagnostics never enter the chat or the message logger", func(t *testing.T) {
				t.Parallel()
				handler := &recordingHandler{}
				var created text.Configurations
				scenario := producingScenario(t)
				scenario.created = &created
				reporter, recorded := newRecordingReporter(t, nil)
				chat, _, _ := runSetupAndQuery(t, factory(scenario, WithDiagnosticReporter(reporter), WithLogger(slog.New(handler))))
				if len(chat.Messages) != len(replyChat().Messages) {
					t.Fatalf("diagnostic sent to the model: %#v", chat.Messages)
				}
				if handler.count() != 0 {
					t.Fatalf("diagnostic written to the message logger: %d records", handler.count())
				}
				if len(recorded.Events()) == 0 {
					t.Fatal("expected events at the Reporter")
				}
			})

			t.Run("Setup.2 Query.2: a failing Reporter changes neither result nor error", func(t *testing.T) {
				t.Parallel()
				typed := claierr.NewTransport(errors.New("transport failed"))
				cases := map[string]func(*testing.T) querierScenario{
					"success": producingScenario,
					"setup failure": func(t *testing.T) querierScenario {
						s := producingScenario(t)
						s.createErr = typed
						return s
					},
					"query failure": func(t *testing.T) querierScenario {
						s := producingScenario(t)
						s.querier = diagnosticsQuerier{err: typed}
						return s
					},
				}
				for caseName, scenario := range cases {
					t.Run(caseName, func(t *testing.T) {
						t.Parallel()
						failing, failingDestination := newRecordingReporter(t, errReportFailed)
						wantChat, wantSetup, wantQuery := runSetupAndQuery(t, factory(scenario(t)))
						gotChat, gotSetup, gotQuery := runSetupAndQuery(t, factory(scenario(t), WithDiagnosticReporter(failing)))
						if !reflect.DeepEqual(gotChat, wantChat) {
							t.Fatalf("chat = %#v, want %#v", gotChat, wantChat)
						}
						assertSameError(t, "setup", gotSetup, wantSetup)
						assertSameError(t, "query", gotQuery, wantQuery)
						if errors.Is(gotSetup, errReportFailed) || errors.Is(gotQuery, errReportFailed) {
							t.Fatal("F6: Report failure reached the caller's error")
						}
						if failingDestination.Calls() == 0 {
							t.Fatal("expected the failing Reporter to be called")
						}
					})
				}
			})
		})
	}
}

func assertSameError(t *testing.T, operation string, got, want error) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s error = %v, want %v", operation, got, want)
	}
	if got != nil && got.Error() != want.Error() {
		t.Fatalf("%s error = %q, want %q", operation, got, want)
	}
}

// TestAgentDiagnosticsCompatibility proves Setup.1, Query.1, F2, F3, and F9.
func TestAgentDiagnosticsCompatibility(t *testing.T) {
	t.Parallel()
	for name, factory := range setupQuerierImplementations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("F3: existing callers need no option and stay silent", func(t *testing.T) {
				t.Parallel()
				handler := &recordingHandler{}
				chat, setupErr, queryErr := runSetupAndQuery(t, factory(producingScenario(t), WithLogger(slog.New(handler))))
				if setupErr != nil || queryErr != nil {
					t.Fatalf("unexpected errors: setup %v, query %v", setupErr, queryErr)
				}
				if !reflect.DeepEqual(chat, replyChat()) {
					t.Fatalf("chat = %#v", chat)
				}
				if handler.count() != 0 {
					t.Fatalf("diagnostics printed by default: %d records", handler.count())
				}
			})

			t.Run("F2: typed setup and query errors reach the caller", func(t *testing.T) {
				t.Parallel()
				cause := errors.New("transport failed")
				typed := claierr.NewTransport(cause)
				for _, scenario := range []querierScenario{
					{configDir: t.TempDir(), createErr: typed},
					{configDir: t.TempDir(), querier: diagnosticsQuerier{err: typed}},
				} {
					reporter, recorded := newRecordingReporter(t, nil)
					_, setupErr, queryErr := runSetupAndQuery(t, factory(scenario, WithDiagnosticReporter(reporter)))
					err := errors.Join(setupErr, queryErr)
					var transport *claierr.TransportError
					if !errors.As(err, &transport) || transport != typed || !errors.Is(err, cause) {
						t.Fatalf("typed error lost: %v", err)
					}
					if containsType(recorded.Events(), diagnosticQueryCompleted) {
						t.Fatal("F2: a failed query reported as completed")
					}
				}
			})

			t.Run("F9: a failed tool call stays on the tool error path", func(t *testing.T) {
				t.Parallel()
				toolFailure := models.Chat{
					ID: "chat",
					Messages: []models.Message{
						{Role: "user", Content: "run it"},
						{Role: "tool", Content: "ERROR: command failed"},
						{Role: "assistant", Content: "it failed"},
					},
				}
				reporter, recorded := newRecordingReporter(t, nil)
				scenario := querierScenario{configDir: t.TempDir(), querier: diagnosticsQuerier{reply: toolFailure}}
				chat, setupErr, queryErr := runSetupAndQuery(t, factory(scenario, WithDiagnosticReporter(reporter)))
				if setupErr != nil || queryErr != nil {
					t.Fatalf("unexpected errors: setup %v, query %v", setupErr, queryErr)
				}
				if !reflect.DeepEqual(chat, toolFailure) {
					t.Fatalf("tool error path changed: %#v", chat)
				}
				for _, event := range recorded.Events() {
					if event.Message == "ERROR: command failed" {
						t.Fatal("tool error entered the diagnostic path")
					}
					for _, fact := range event.Facts {
						if fact.Value.String() == "ERROR: command failed" {
							t.Fatal("tool error entered the diagnostic path")
						}
					}
				}
			})
		})
	}
}

// TestAgentLoggerCompatibility proves WithDiagnosticReporter.1 and F3: the
// message logger and the Reporter each receive their own records.
func TestAgentLoggerCompatibility(t *testing.T) {
	t.Parallel()
	for name, factory := range setupQuerierImplementations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(&recordingHandler{})
			var created text.Configurations
			scenario := producingScenario(t)
			scenario.created = &created
			reporter, recorded := newRecordingReporter(t, nil)
			if _, setupErr, queryErr := runSetupAndQuery(t, factory(scenario, WithLogger(logger), WithSlogLevel(slog.LevelInfo), WithDiagnosticReporter(reporter))); setupErr != nil || queryErr != nil {
				t.Fatalf("unexpected errors: setup %v, query %v", setupErr, queryErr)
			}
			if created.AgentSettings == nil || created.AgentSettings.Logger != logger || created.AgentSettings.Level != slog.LevelInfo {
				t.Fatalf("message logger not carried to the querier: %#v", created.AgentSettings)
			}
			if len(recorded.Events()) == 0 {
				t.Fatal("Reporter received no diagnostics")
			}
		})
	}
}

// TestAgentDelivererOption proves WithDiagnosticDeliverer.1.
func TestAgentDelivererOption(t *testing.T) {
	t.Parallel()
	for name, factory := range setupQuerierImplementations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, level := range []diagnostic.Level{diagnostic.LevelDebug, diagnostic.LevelWarn, diagnostic.LevelError} {
				t.Run("level filter", func(t *testing.T) {
					t.Parallel()
					deliverer := diagfake.New()
					if _, setupErr, queryErr := runSetupAndQuery(t, factory(producingScenario(t), WithDiagnosticDeliverer(deliverer, level))); setupErr != nil || queryErr != nil {
						t.Fatalf("unexpected errors: setup %v, query %v", setupErr, queryErr)
					}
					got := deliverer.Events()
					// The scenario produces the agent's own events; the
					// internal registry's are TestAgentInternalDiagnostics'.
					for _, diagnosticType := range agentDiagnosticTypes() {
						want := diagnosticType.Level <= level || diagnosticType.ActionRequired
						if containsType(got, diagnosticType.Name) != want {
							t.Fatalf("level %d: %q delivered = %v, want %v (mode %d)", level, diagnosticType.Name, !want, want, diagnosticType.Mode)
						}
					}
					for _, event := range got {
						if event.Level > level && !isActionRequired(event.Type) {
							t.Fatalf("event %q at level %d delivered at %d", event.Type, event.Level, level)
						}
						if event.Subsystem == "" {
							t.Fatalf("event %q delivered without its subsystem", event.Type)
						}
					}
				})
			}

			t.Run("the later of the two options wins", func(t *testing.T) {
				t.Parallel()
				deliverer := diagfake.New()
				reporter, recorded := newRecordingReporter(t, nil)
				runSetupAndQuery(t, factory(producingScenario(t), WithDiagnosticReporter(reporter), WithDiagnosticDeliverer(deliverer, diagnostic.LevelDebug)))
				if len(deliverer.Events()) == 0 || len(recorded.Events()) != 0 {
					t.Fatalf("deliverer last: deliverer %d, reporter %d", len(deliverer.Events()), len(recorded.Events()))
				}

				deliverer = diagfake.New()
				reporter, recorded = newRecordingReporter(t, nil)
				runSetupAndQuery(t, factory(producingScenario(t), WithDiagnosticDeliverer(deliverer, diagnostic.LevelDebug), WithDiagnosticReporter(reporter)))
				if len(deliverer.Events()) != 0 || len(recorded.Events()) == 0 {
					t.Fatalf("reporter last: deliverer %d, reporter %d", len(deliverer.Events()), len(recorded.Events()))
				}
			})

			t.Run("both output modes reach the one Deliverer", func(t *testing.T) {
				t.Parallel()
				modes := map[diagnostic.OutputMode]bool{}
				for _, diagnosticType := range DiagnosticTypes() {
					modes[diagnosticType.Mode] = true
				}
				if !modes[diagnostic.Immediate] || !modes[diagnostic.Buffered] {
					t.Fatalf("agent types do not cover both modes: %v", modes)
				}
			})

			t.Run("a nil Deliverer leaves the call silent and unchanged", func(t *testing.T) {
				t.Parallel()
				chat, setupErr, queryErr := runSetupAndQuery(t, factory(producingScenario(t), WithDiagnosticDeliverer(nil, diagnostic.LevelDebug)))
				if setupErr != nil || queryErr != nil || !reflect.DeepEqual(chat, replyChat()) {
					t.Fatalf("call changed: chat %#v, setup %v, query %v", chat, setupErr, queryErr)
				}
			})
		})
	}
}

func isActionRequired(name string) bool {
	for _, diagnosticType := range DiagnosticTypes() {
		if diagnosticType.Name == name {
			return diagnosticType.ActionRequired
		}
	}
	return false
}

// erringReporter records every refusal of the routing Reporter it wraps.
type erringReporter struct {
	inner  diagnostic.Reporter
	mu     sync.Mutex
	errors []error
	types  []string
}

func (r *erringReporter) Report(ctx context.Context, event diagnostic.Event) error {
	err := r.inner.Report(ctx, event)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types = append(r.types, event.Type)
	if err != nil {
		r.errors = append(r.errors, err)
	}
	return err
}

// TestAgentDiagnosticTypes proves DiagnosticTypes.1.
func TestAgentDiagnosticTypes(t *testing.T) {
	t.Parallel()

	types := DiagnosticTypes()
	if len(types) == 0 {
		t.Fatal("DiagnosticTypes.1: no types declared")
	}
	names := map[string]bool{}
	levels := diag.ResolvedLevels{"": diagnostic.LevelTrace}
	for _, diagnosticType := range types {
		if names[diagnosticType.Name] {
			t.Fatalf("DiagnosticTypes.1: duplicate name %q", diagnosticType.Name)
		}
		names[diagnosticType.Name] = true
		levels[diagnosticType.Subsystem] = diagnostic.LevelTrace
	}

	types[0].Name = "mutated"
	if DiagnosticTypes()[0].Name == "mutated" {
		t.Fatal("DiagnosticTypes shares its slice between callers")
	}

	for name, factory := range setupQuerierImplementations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deliverer := diagfake.New()
			routing, err := diag.NewReporter(DiagnosticTypes(), levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
				diagnostic.Immediate: deliverer,
				diagnostic.Buffered:  deliverer,
			})
			if err != nil {
				t.Fatalf("NewReporter refused the declared types: %v", err)
			}
			reporter := &erringReporter{inner: routing}
			typed := claierr.NewTransport(errors.New("down"))
			for _, scenario := range []querierScenario{
				producingScenario(t),
				{configDir: t.TempDir(), createErr: typed},
				{configDir: t.TempDir(), querier: diagnosticsQuerier{err: typed}},
			} {
				runSetupAndQuery(t, factory(scenario, WithDiagnosticReporter(reporter)))
			}
			if len(reporter.errors) != 0 {
				t.Fatalf("DiagnosticTypes.1: events refused: %v", reporter.errors)
			}
			for _, reported := range reporter.types {
				if !names[reported] {
					t.Fatalf("DiagnosticTypes.1: event type %q is not declared", reported)
				}
			}
			// The scenarios produce the agent's own types; the internal
			// registry's are TestAgentInternalDiagnostics'.
			for _, declared := range agentDiagnosticTypes() {
				if !names[declared.Name] {
					t.Fatalf("DiagnosticTypes.1: the agent's type %q is not declared", declared.Name)
				}
				if !containsType(deliverer.Events(), declared.Name) {
					t.Fatalf("declared type %q never produced", declared.Name)
				}
			}
		})
	}
}
