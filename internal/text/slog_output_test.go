package text

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/models"
	inttools "github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

func TestTruncateMiddleRunes(t *testing.T) {
	t.Run("within_limit", func(t *testing.T) {
		s := "hello world"
		got, truncated := truncateMiddleRunes(s, 20)
		if truncated || got != s {
			t.Fatalf("within limit: got %q truncated=%t, want %q truncated=false", got, truncated, s)
		}
	})

	t.Run("at_limit", func(t *testing.T) {
		s := "hello world" // 11 runes
		got, truncated := truncateMiddleRunes(s, 11)
		if truncated || got != s {
			t.Fatalf("at limit: got %q truncated=%t, want %q truncated=false", got, truncated, s)
		}
	})

	t.Run("over_limit", func(t *testing.T) {
		got, truncated := truncateMiddleRunes("abcdefghij", 5)
		if !truncated {
			t.Fatal("expected truncated=true")
		}
		want := "ab" + slogTruncationMarker + "ij"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if n := utf8.RuneCountInString(got); n != 5 {
			t.Fatalf("expected 5 runes, got %d (%q)", n, got)
		}
	})

	t.Run("multibyte_rune", func(t *testing.T) {
		// é (U+00E9) and ö (U+00F6) are multi-byte runes; the head/tail cut
		// must never land inside one.
		cases := []struct {
			name  string
			s     string
			limit int
			want  string
		}{
			{"head_cut_mid_rune", "ééé", 2, "é" + slogTruncationMarker},
			{"tail_cut_mid_rune", "abcéé", 3, "a" + slogTruncationMarker + "é"},
			{"mixed_unicode", "héllo wörld", 4, "hé" + slogTruncationMarker + "d"},
			{"ascii_balanced", "abcdefghij", 5, "ab" + slogTruncationMarker + "ij"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, truncated := truncateMiddleRunes(tc.s, tc.limit)
				if !truncated {
					t.Fatalf("expected truncated=true")
				}
				if got != tc.want {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
				if !utf8.ValidString(got) {
					t.Fatalf("output is invalid UTF-8: %q", got)
				}
				if n := utf8.RuneCountInString(got); n != tc.limit {
					t.Fatalf("expected %d runes, got %d", tc.limit, n)
				}
			})
		}
	})

	t.Run("nonpositive_limit", func(t *testing.T) {
		for _, limit := range []int{0, -1, -42} {
			got, truncated := truncateMiddleRunes("any text", limit)
			if truncated || got != "any text" {
				t.Fatalf("limit %d: got %q truncated=%t, want input unchanged", limit, got, truncated)
			}
		}
	})

	t.Run("single_rune_limit", func(t *testing.T) {
		got, truncated := truncateMiddleRunes("hello", 1)
		if !truncated {
			t.Fatal("expected truncated=true")
		}
		if got != slogTruncationMarker {
			t.Fatalf("got %q, want %q", got, slogTruncationMarker)
		}
	})

	t.Run("empty_input", func(t *testing.T) {
		got, truncated := truncateMiddleRunes("", 5)
		if truncated || got != "" {
			t.Fatalf("empty input: got %q truncated=%t, want unchanged", got, truncated)
		}
		got, truncated = truncateMiddleRunes("", 0)
		if truncated || got != "" {
			t.Fatalf("empty input, no cap: got %q truncated=%t, want unchanged", got, truncated)
		}
	})
}

// captureHandler records every slog record with the context passed to Handle.
// Enabled always returns true, so the caller-set AgentSettings.Level never
// filters a record away in tests.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	ctxs    []context.Context
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctxs = append(h.ctxs, ctx)
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// recordAttrs flattens one record's attributes into a map for assertions.
func recordAttrs(r slog.Record) map[string]any {
	attrs := make(map[string]any, 6)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

func TestQuerier_LogMessage_nilLogger(t *testing.T) {
	h := &captureHandler{}
	logger := slog.New(h)
	q := &Querier[*MockQuerier]{}

	q.logMessage(context.Background(), "assistant", "text", "")
	q.agentSettings = &AgentSettings{}
	q.logMessage(context.Background(), "assistant", "text", "")
	if len(h.records) != 0 {
		t.Fatalf("nil agentSettings or nil logger must not log, got %d records", len(h.records))
	}

	q.agentSettings = &AgentSettings{Logger: logger}
	q.logMessage(context.Background(), "assistant", "text", "")
	if len(h.records) != 1 {
		t.Fatalf("expected one record with a live logger, got %d", len(h.records))
	}
}

func TestQuerier_LogMessage_truncates(t *testing.T) {
	h := &captureHandler{}
	q := &Querier[*MockQuerier]{agentSettings: &AgentSettings{
		Logger:    slog.New(h),
		Level:     slog.LevelDebug,
		RuneLimit: 5,
	}}
	q.logMessage(context.Background(), "assistant", "abcdefghij", "")

	record := h.records[0]
	attrs := recordAttrs(record)
	if got, want := attrs["text"], "ab"+slogTruncationMarker+"ij"; got != want {
		t.Fatalf("text = %v, want %q", got, want)
	}
	if attrs["truncated"] != true {
		t.Fatalf("truncated = %v, want true", attrs["truncated"])
	}
	if attrs["kind"] != "assistant" {
		t.Fatalf("kind = %v, want assistant", attrs["kind"])
	}
	if record.Message != "clai message" {
		t.Fatalf("message = %q, want %q", record.Message, "clai message")
	}
	if record.Level != slog.LevelDebug {
		t.Fatalf("level = %v, want %v", record.Level, slog.LevelDebug)
	}
}

// logMessageCtxKey is a private context key proving logMessage forwards the
// live query context to the handler (worklog 2026-08-15-agent-slog-output, D6).
type logMessageCtxKey string

func TestQuerier_LogMessage_ctx(t *testing.T) {
	h := &captureHandler{}
	q := &Querier[*MockQuerier]{agentSettings: &AgentSettings{Logger: slog.New(h)}}
	ctx := context.WithValue(context.Background(), logMessageCtxKey("marker"), "value")
	q.logMessage(ctx, "assistant", "text", "")

	if len(h.ctxs) != 1 {
		t.Fatalf("expected one handled record, got %d", len(h.ctxs))
	}
	if got := h.ctxs[0].Value(logMessageCtxKey("marker")); got != "value" {
		t.Fatalf("handler context lost the value: got %v, want %q", got, "value")
	}
}

func TestQuerier_LogMessage_kind(t *testing.T) {
	for _, kind := range []string{"assistant", "reasoning", "tool_call", "tool_result", "final_answer"} {
		t.Run(kind, func(t *testing.T) {
			h := &captureHandler{}
			q := &Querier[*MockQuerier]{agentSettings: &AgentSettings{Logger: slog.New(h)}}
			q.logMessage(context.Background(), kind, "text", "")

			if len(h.records) != 1 {
				t.Fatalf("expected exactly one record, got %d", len(h.records))
			}
			attrs := recordAttrs(h.records[0])
			if attrs["kind"] != kind {
				t.Fatalf("kind = %v, want %q", attrs["kind"], kind)
			}
			if attrs["text"] != "text" {
				t.Fatalf("text = %v, want %q", attrs["text"], "text")
			}
			if attrs["truncated"] != false {
				t.Fatalf("truncated = %v, want false", attrs["truncated"])
			}
		})
	}
}

// scriptedDisplayQuerier builds a querier whose mock model streams one fixed
// script: reasoning, assistant prose, a tool call, then reasoning and a final
// answer. The stream exercises every display site — reasoning open/close,
// streamed tokens, tool-call echo, tool result, and final answer — so the
// worklog 2026-08-15-agent-slog-output Phase 4 byte-identity proof covers all of them.
func scriptedDisplayQuerier(out *strings.Builder, raw bool, agentSettings *AgentSettings) *Querier[*MockQuerier] {
	var callCount int
	model := &MockQuerier{}
	model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		ch := make(chan models.CompletionEvent, 4)
		if callCount == 1 {
			ch <- models.ReasoningEvent{Content: "thinking through the steps"}
			ch <- "I will look that up."
			ch <- pub_models.Call{ID: "call-1", Name: "test", Inputs: &pub_models.Input{}}
		} else {
			ch <- models.ReasoningEvent{Content: "final reasoning"}
			ch <- "final answer"
		}
		close(ch)
		return ch, nil
	}
	return &Querier[*MockQuerier]{
		Raw:   raw,
		Model: model,
		out:   out,
		dims:  dimensions.Dimensions{Width: 80, Height: 24},
		chat: pub_models.Chat{Messages: []pub_models.Message{
			{Role: "user", Content: "look it up"},
		}},
		agentSettings: agentSettings,
	}
}

// TestSlogLogger_DoesNotPerturbDisplay proves display invariance: the same
// scripted stream writes byte-identical output to out with and without an
// attached slog logger (worklog 2026-08-15-agent-slog-output, Phase 4). Both
// the raw display path and the default rolling-window path run, so every
// display site is covered. The capturing logger must receive one record per
// completed message in stream order while the display bytes stay untouched.
func TestSlogLogger_DoesNotPerturbDisplay(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		inttools.Registry.Set("test", stubTool{output: "tool output"})

		for _, tc := range []struct {
			name string
			raw  bool
		}{
			{name: "raw", raw: true},
			{name: "rolling", raw: false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if !tc.raw {
					// The default theme enables the rolling activity window;
					// pin it so the interactive CLI display path runs.
					withRollingTheme(t)
				}
				runOnce := func(withLogger bool) (string, *captureHandler) {
					t.Helper()
					var out strings.Builder
					h := &captureHandler{}
					var settings *AgentSettings
					if withLogger {
						settings = &AgentSettings{Logger: slog.New(h)}
					}
					if err := scriptedDisplayQuerier(&out, tc.raw, settings).Query(context.Background()); err != nil {
						t.Fatalf("Query() error = %v", err)
					}
					return out.String(), h
				}

				plainOut, plainHandler := runOnce(false)
				loggedOut, loggedHandler := runOnce(true)
				if plainOut != loggedOut {
					t.Fatalf("display bytes differ with a logger attached\n--- without logger ---\n%q\n--- with logger ---\n%q", plainOut, loggedOut)
				}
				if len(plainHandler.records) != 0 {
					t.Fatalf("nil agentSettings must produce no log records, got %d", len(plainHandler.records))
				}
				kinds := make([]string, 0, len(loggedHandler.records))
				for _, rec := range loggedHandler.records {
					attrs := recordAttrs(rec)
					kind, _ := attrs["kind"].(string)
					kinds = append(kinds, kind)
					if (kind == "tool_call" || kind == "tool_result") && attrs["tool"] != "test" {
						t.Fatalf("%v record tool = %v, want test", kind, attrs["tool"])
					}
				}
				want := "reasoning,assistant,tool_call,tool_result,reasoning,final_answer"
				if got := strings.Join(kinds, ","); got != want {
					t.Fatalf("logged kinds = %q, want %q", got, want)
				}
			})
		}
	})
}

// TestQuerier_StructuredOutput_EmitsFinalAnswer proves the final-answer
// record fires on the structured-output path too. The typed-agent path is
// always structured (json_object), and the finalizer's structured branch
// skips postProcessOutput's display sites — the record must be emitted
// there, not lost (worklog 2026-08-15-agent-slog-output, Phase 3; gap found
// by sakfråga worklog 26-08-15-agent-slog-wiring, Phase 2). The stream is
// the same scriptedDisplayQuerier sequence as the raw/rolling invariance
// test, so the kind sequence must match it exactly.
func TestQuerier_StructuredOutput_EmitsFinalAnswer(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		inttools.Registry.Set("test", stubTool{output: "tool output"})

		var out strings.Builder
		h := &captureHandler{}
		q := scriptedDisplayQuerier(&out, false, &AgentSettings{Logger: slog.New(h)})
		q.structuredOutput = true
		if err := q.Query(context.Background()); err != nil {
			t.Fatalf("Query() error = %v", err)
		}

		kinds := make([]string, 0, len(h.records))
		for _, rec := range h.records {
			attrs := recordAttrs(rec)
			kind, _ := attrs["kind"].(string)
			kinds = append(kinds, kind)
		}
		want := "reasoning,assistant,tool_call,tool_result,reasoning,final_answer"
		if got := strings.Join(kinds, ","); got != want {
			t.Fatalf("logged kinds = %q, want %q", got, want)
		}
		if got := strings.TrimSpace(out.String()); got != "final answer" {
			t.Fatalf("structured display = %q, want %q", got, "final answer")
		}
	})
}

func TestQuerier_LogMessage_toolAttr(t *testing.T) {
	cases := []struct {
		kind     string
		tool     string
		wantTool bool
	}{
		{"tool_call", "ls", true},
		{"tool_result", "ls", true},
		{"assistant", "", false},
		{"reasoning", "", false},
		{"final_answer", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			h := &captureHandler{}
			q := &Querier[*MockQuerier]{agentSettings: &AgentSettings{Logger: slog.New(h)}}
			q.logMessage(context.Background(), tc.kind, "text", tc.tool)

			attrs := recordAttrs(h.records[0])
			gotTool, ok := attrs["tool"]
			if tc.wantTool {
				if !ok || gotTool != tc.tool {
					t.Fatalf("tool = %v (present %t), want %q", gotTool, ok, tc.tool)
				}
			} else if ok {
				t.Fatalf("kind %q must not carry a tool attribute, got %q", tc.kind, gotTool)
			}
		})
	}
}
