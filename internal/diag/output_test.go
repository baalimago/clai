package diag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
)

var errWriter = errors.New("diagnostic writer failed")

type destination struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (d *destination) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.Write(p)
}

func (d *destination) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.String()
}

type failingWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *failingWriter) Write([]byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	return 0, errWriter
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

type recordingViewport struct {
	mu    sync.Mutex
	lines []string
}

func (v *recordingViewport) Log(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = append(v.lines, line)
}

func (v *recordingViewport) snapshot() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.lines...)
}

type remedyError struct {
	message  string
	remedies []diagnostic.Remedy
}

func (e *remedyError) Error() string                 { return e.message }
func (e *remedyError) Remedies() []diagnostic.Remedy { return e.remedies }

type presenterFactory struct {
	name string
	json bool
	make func(w io.Writer, level diagnostic.Level) Presenter
}

func presenterFactories() []presenterFactory {
	return []presenterFactory{
		{name: "text plain", make: func(w io.Writer, level diagnostic.Level) Presenter {
			return NewTextPresenter(w, Plain, level)
		}},
		{name: "text colored", make: func(w io.Writer, level diagnostic.Level) Presenter {
			return NewTextPresenter(w, Colored, level)
		}},
		{name: "json", json: true, make: func(w io.Writer, level diagnostic.Level) Presenter {
			return NewJSONPresenter(jsonl.NewEncoder(w), level)
		}},
	}
}

func decodeRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.Lines(output) {
		if !strings.HasSuffix(line, "\n") || !strings.HasPrefix(line, `{"type":`) {
			t.Fatalf("line is not a record: %q", line)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// visible is what a reader sees once per incident: the text output, or the
// causes of the JSON output's error records.
func visible(t *testing.T, factory presenterFactory, output string) string {
	t.Helper()
	if !factory.json {
		return output
	}
	var causes strings.Builder
	for _, record := range decodeRecords(t, output) {
		encoded, err := json.Marshal(record["causes"])
		if err != nil {
			t.Fatalf("marshal causes: %v", err)
		}
		causes.Write(encoded)
	}
	return causes.String()
}

func sampleEvent() diagnostic.Event {
	return diagnostic.Event{
		Type:      "mcp_stderr",
		Subsystem: "tools",
		Level:     diagnostic.LevelWarn,
		Message:   "server wrote to stderr",
		Facts:     []slog.Attr{slog.String("server", "files")},
	}
}

func TestPresenterContract(t *testing.T) {
	t.Parallel()

	t.Run("NewTextPresenter.1 and Deliver.3", func(t *testing.T) {
		t.Parallel()
		colored := &destination{}
		plain := &destination{}
		result := &destination{}
		if err := NewTextPresenter(colored, Colored, diagnostic.LevelInfo).Deliver(context.Background(), sampleEvent()); err != nil {
			t.Fatalf("colored deliver: %v", err)
		}
		if err := NewTextPresenter(plain, Plain, diagnostic.LevelInfo).Deliver(context.Background(), sampleEvent()); err != nil {
			t.Fatalf("plain deliver: %v", err)
		}
		if !strings.Contains(colored.String(), "\x1b[") {
			t.Fatalf("terminal output has no theme: %q", colored.String())
		}
		if strings.Contains(plain.String(), "\x1b") {
			t.Fatalf("pipe or NO_COLOR output has ANSI: %q", plain.String())
		}
		for _, output := range []string{colored.String(), plain.String()} {
			for _, want := range []string{"warn", "tools", "server wrote to stderr", "server=files"} {
				if !strings.Contains(output, want) {
					t.Fatalf("output %q lacks %q", output, want)
				}
			}
			if strings.Count(output, "\n") != 1 || !strings.HasSuffix(output, "\n") {
				t.Fatalf("output is not one line: %q", output)
			}
		}
		if result.String() != "" {
			t.Fatalf("result writer received diagnostics: %q", result.String())
		}
		if timestamp := regexp.MustCompile(`\d{2}:\d{2}:\d{2}|\d{4}-\d{2}-\d{2}`); timestamp.MatchString(plain.String()) {
			t.Fatalf("timestamp written with Terminal timestamps off: %q", plain.String())
		}
	})

	for _, factory := range presenterFactories() {
		t.Run("Deliver.4 "+factory.name, func(t *testing.T) {
			t.Parallel()
			failing := &failingWriter{}
			presenter := factory.make(failing, diagnostic.LevelTrace)
			if err := presenter.Deliver(context.Background(), sampleEvent()); !errors.Is(err, errWriter) {
				t.Fatalf("failed write error = %v", err)
			}
			if failing.calls != 1 {
				t.Fatalf("failed writer called %d times, want 1", failing.calls)
			}
			if err := presenter.ReportError(context.Background(), errors.New("boom")); !errors.Is(err, errWriter) {
				t.Fatalf("failed ReportError write = %v", err)
			}
			short := factory.make(shortWriter{}, diagnostic.LevelTrace)
			if err := short.Deliver(context.Background(), sampleEvent()); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short write error = %v", err)
			}
			if err := short.ReportError(context.Background(), errors.New("boom")); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short ReportError write = %v", err)
			}
		})
	}

	for _, factory := range presenterFactories() {
		t.Run("ReportError.1 nil "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			if err := factory.make(out, diagnostic.LevelTrace).ReportError(context.Background(), nil); err != nil {
				t.Fatalf("nil error: %v", err)
			}
			if out.String() != "" {
				t.Fatalf("nil error wrote %q", out.String())
			}
		})
	}
}

func TestPresenterViewport(t *testing.T) {
	t.Parallel()

	t.Run("Attach.1", func(t *testing.T) {
		t.Parallel()
		out := &destination{}
		presenter := NewTextPresenter(out, Colored, diagnostic.LevelInfo)
		outer := &recordingViewport{}
		inner := &recordingViewport{}
		event := sampleEvent()

		detachOuter := presenter.Attach(outer)
		event.Message = "first"
		if err := presenter.Deliver(context.Background(), event); err != nil {
			t.Fatalf("deliver to outer: %v", err)
		}
		detachInner := presenter.Attach(inner)
		event.Message = "second"
		if err := presenter.Deliver(context.Background(), event); err != nil {
			t.Fatalf("deliver to inner: %v", err)
		}
		if err := presenter.ReportError(context.Background(), errors.New("terminal")); err != nil {
			t.Fatalf("report to inner: %v", err)
		}
		detachInner()
		detachInner()
		event.Message = "third"
		if err := presenter.Deliver(context.Background(), event); err != nil {
			t.Fatalf("deliver to restored outer: %v", err)
		}
		detachOuter()
		event.Message = "fourth"
		if err := presenter.Deliver(context.Background(), event); err != nil {
			t.Fatalf("deliver after detach: %v", err)
		}

		outerLines := outer.snapshot()
		innerLines := inner.snapshot()
		if len(outerLines) != 2 || !strings.Contains(outerLines[0], "first") || !strings.Contains(outerLines[1], "third") {
			t.Fatalf("outer viewport lines = %q", outerLines)
		}
		if len(innerLines) != 2 || !strings.Contains(innerLines[0], "second") || !strings.Contains(innerLines[1], "terminal") {
			t.Fatalf("inner viewport lines = %q", innerLines)
		}
		for _, line := range append(outerLines, innerLines...) {
			if strings.Contains(line, "\n") {
				t.Fatalf("viewport line holds a newline: %q", line)
			}
		}
		if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "fourth") {
			t.Fatalf("destination after detach = %q", got)
		}
	})

	t.Run("Deliver.5 Attach has no effect", func(t *testing.T) {
		t.Parallel()
		out := &destination{}
		presenter := NewJSONPresenter(jsonl.NewEncoder(out), diagnostic.LevelInfo)
		viewport := &recordingViewport{}
		detach := presenter.Attach(viewport)
		defer detach()
		if err := presenter.Deliver(context.Background(), sampleEvent()); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		if err := presenter.ReportError(context.Background(), errors.New("terminal")); err != nil {
			t.Fatalf("report: %v", err)
		}
		if lines := viewport.snapshot(); len(lines) != 0 {
			t.Fatalf("json presenter used the viewport: %q", lines)
		}
		if records := decodeRecords(t, out.String()); len(records) != 2 {
			t.Fatalf("records = %v", records)
		}
	})

	t.Run("concurrent deliveries keep whole lines", func(t *testing.T) {
		t.Parallel()
		out := &destination{}
		presenter := NewTextPresenter(out, Plain, diagnostic.LevelInfo)
		viewport := &recordingViewport{}
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				detach := presenter.Attach(viewport)
				_ = presenter.Deliver(context.Background(), sampleEvent())
				detach()
				_ = presenter.Deliver(context.Background(), sampleEvent())
			})
		}
		wg.Wait()
		total := len(viewport.snapshot()) + strings.Count(out.String(), "\n")
		if total != 40 {
			t.Fatalf("delivered %d lines, want 40", total)
		}
		for line := range strings.Lines(out.String()) {
			if !strings.HasPrefix(line, "warn") {
				t.Fatalf("interleaved line %q", line)
			}
		}
	})
}

func TestPresenterErrors(t *testing.T) {
	t.Parallel()

	fix := diagnostic.Remedy{Instruction: "set OPENAI_API_KEY", Command: []string{"export", "OPENAI_API_KEY=..."}}
	missing := &remedyError{message: "missing key", remedies: []diagnostic.Remedy{fix, fix}}
	located := Errorf("load profile: %w", missing)
	stacked := ErrorfWithStack("open config: %w", errors.New("no such file"))
	joined := errors.Join(located, stacked)
	classified := errors.Join(Errorf("call model: %w", claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401})), claierr.ErrAuthFailed)
	duplicateRemedy := errors.Join(Errorf("first: %w", missing), Errorf("second: %w", &remedyError{message: "other", remedies: []diagnostic.Remedy{fix}}))

	for _, factory := range presenterFactories() {
		t.Run("ReportError.1 "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			presenter := factory.make(out, diagnostic.LevelInfo)
			before := joined.Error()
			if err := presenter.ReportError(context.Background(), joined); err != nil {
				t.Fatalf("report: %v", err)
			}
			if joined.Error() != before || !errors.Is(joined, missing) {
				t.Fatalf("error tree changed")
			}
			got := visible(t, factory, out.String())
			for _, want := range []string{"load profile: missing key", "open config: no such file", "set OPENAI_API_KEY", "export OPENAI_API_KEY=..."} {
				if strings.Count(got, want) != 1 {
					t.Fatalf("%q appears %d times in %q", want, strings.Count(got, want), got)
				}
			}
			if strings.Contains(got, "output_test.go") {
				t.Fatalf("location shown below LevelDebug: %q", got)
			}
			if factory.json {
				records := decodeRecords(t, out.String())
				if len(records) != 1 || records[0]["type"] != "error" {
					t.Fatalf("records = %v", records)
				}
				if causes := records[0]["causes"].([]any); len(causes) != 2 {
					t.Fatalf("causes = %v", causes)
				}
			}
		})

		t.Run("ReportError.1 classification "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			presenter := factory.make(out, diagnostic.LevelInfo)
			if err := presenter.ReportError(context.Background(), classified); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := visible(t, factory, out.String())
			if strings.Count(got, "authentication failed") != 1 {
				t.Fatalf("classification sentinel shown as an incident: %q", got)
			}
			if factory.json {
				records := decodeRecords(t, out.String())
				if causes := records[0]["causes"].([]any); len(causes) != 1 {
					t.Fatalf("causes = %v", causes)
				}
			}
		})

		t.Run("ReportError.1 duplicate remedies "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			if err := factory.make(out, diagnostic.LevelInfo).ReportError(context.Background(), duplicateRemedy); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := visible(t, factory, out.String())
			if strings.Count(got, "set OPENAI_API_KEY") != 1 || !strings.Contains(got, "first: missing key") || !strings.Contains(got, "second: other") {
				t.Fatalf("remedies or causes: %q", got)
			}
		})

		t.Run("ReportError.1 joined duplicates "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			plain := errors.New("disk full")
			if err := factory.make(out, diagnostic.LevelInfo).ReportError(context.Background(), errors.Join(plain, plain, errors.New("quota"))); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := out.String()
			if factory.json {
				records := decodeRecords(t, got)
				if causes := records[0]["causes"].([]any); len(causes) != 2 {
					t.Fatalf("causes = %v", causes)
				}
				return
			}
			if strings.Count(got, "disk full") != 1 || strings.Count(got, "quota") != 1 {
				t.Fatalf("duplicate incident: %q", got)
			}
		})

		t.Run("ReportError.1 one incident "+factory.name, func(t *testing.T) {
			t.Parallel()
			response := &claierr.APIError{StatusCode: 429, ProviderCode: "insufficient_quota"}
			shared := errors.Join(claierr.NewRateLimited(response, time.Time{}, 0, 0), claierr.NewInsufficientCredits(response))
			operands := Errorf("flush buffer: %w: spill: %w", ErrUndeliverable, Errorf("write spill: %w", missing))
			for _, err := range []error{shared, operands} {
				out := &destination{}
				if reportErr := factory.make(out, diagnostic.LevelDebug).ReportError(context.Background(), err); reportErr != nil {
					t.Fatalf("report: %v", reportErr)
				}
				got := out.String()
				if factory.json {
					if causes := decodeRecords(t, got)[0]["causes"].([]any); len(causes) != 1 {
						t.Fatalf("causes of %q = %v", err, causes)
					}
				} else if strings.Contains(got, "independent errors") {
					t.Fatalf("one incident split: %q", got)
				}
			}
			out := &destination{}
			if err := factory.make(out, diagnostic.LevelDebug).ReportError(context.Background(), operands); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := visible(t, factory, out.String())
			if !strings.Contains(got, "set OPENAI_API_KEY") || !strings.Contains(got, "output_test.go:") {
				t.Fatalf("operand remedies or location lost: %q", got)
			}
		})

		t.Run("ReportError.2 "+factory.name, func(t *testing.T) {
			t.Parallel()
			debug := &destination{}
			trace := &destination{}
			if err := factory.make(debug, diagnostic.LevelDebug).ReportError(context.Background(), joined); err != nil {
				t.Fatalf("debug report: %v", err)
			}
			if err := factory.make(trace, diagnostic.LevelTrace).ReportError(context.Background(), joined); err != nil {
				t.Fatalf("trace report: %v", err)
			}
			for _, got := range []string{debug.String(), trace.String()} {
				for _, want := range []string{"internal/diag/output_test.go:", "load profile", "open config"} {
					if !strings.Contains(got, want) {
						t.Fatalf("output %q lacks %q", got, want)
					}
				}
			}
			if factory.json {
				records := decodeRecords(t, debug.String())
				for _, cause := range records[0]["causes"].([]any) {
					fields := cause.(map[string]any)
					if fields["operation"] == nil || fields["location"] == nil {
						t.Fatalf("cause lacks operation or location: %v", fields)
					}
				}
				return
			}
			stackFrame := "TestPresenterErrors"
			if strings.Contains(debug.String(), stackFrame) {
				t.Fatalf("stack shown at LevelDebug: %q", debug.String())
			}
			if strings.Count(trace.String(), stackFrame) < 1 {
				t.Fatalf("stack missing at LevelTrace: %q", trace.String())
			}
			traceLines := strings.Split(trace.String(), "\n")
			for i, line := range traceLines {
				if strings.Contains(line, "load profile") {
					for _, following := range traceLines[i+1:] {
						if strings.Contains(following, "open config") {
							break
						}
						if strings.Contains(following, stackFrame) {
							t.Fatalf("stack invented for a cause without one: %q", trace.String())
						}
					}
				}
			}
		})

		t.Run("missing data "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			if err := factory.make(out, diagnostic.LevelTrace).ReportError(context.Background(), errors.New("bare failure")); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "bare failure") || strings.Contains(got, ".go:") || strings.Contains(got, "hint") {
				t.Fatalf("invented data: %q", got)
			}
			if factory.json {
				cause := decodeRecords(t, got)[0]["causes"].([]any)[0].(map[string]any)
				if len(cause) != 1 {
					t.Fatalf("bare cause has fields beyond message: %v", cause)
				}
			}
		})
	}
}

func TestPresenterSafety(t *testing.T) {
	t.Parallel()

	secrets := []string{
		"sk-proj-abcdefghijklmnopqrstuvwx",
		"sk-ant-api03-abcdefghijklmnopqrstuvwx",
		"AKIAABCDEFGHIJKLMNOP",
		"ghp_abcdefghijklmnopqrstuvwxyz0123",
		"xoxb-123456-abcdef",
		"AIzaSyAbcdefghijklmnopqrstuvwxyz0123456",
		"eyJhbGciOiJIUzI1NiJ9.payloadpayload.signaturesig",
		"hunter2secret",
	}
	external := "auth failed for Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payloadpayload.signaturesig key sk-proj-abcdefghijklmnopqrstuvwx " +
		"aws AKIAABCDEFGHIJKLMNOP gh ghp_abcdefghijklmnopqrstuvwxyz0123 slack xoxb-123456-abcdef google AIzaSyAbcdefghijklmnopqrstuvwxyz0123456 " +
		"https://example.test/?api_key=hunter2secret&x=1 anthropic sk-ant-api03-abcdefghijklmnopqrstuvwx \x1b[31mred\x1b]0;title\x07\r\x00 done"
	event := diagnostic.Event{
		Type:      "provider",
		Subsystem: "vendor.openai",
		Level:     diagnostic.LevelWarn,
		Message:   external,
		Facts: []slog.Attr{
			slog.String("body", external),
			slog.Group("request", slog.String("header", external)),
			slog.Any("cause", errors.New(external)),
			slog.Any("lines", []string{external}),
			slog.Int("status", 401),
		},
	}
	failure := Errorf("call %s: %w", external, &remedyError{message: external, remedies: []diagnostic.Remedy{{Instruction: "rotate the key\x1b[2J"}}})

	for _, factory := range presenterFactories() {
		t.Run("Deliver.2 "+factory.name, func(t *testing.T) {
			t.Parallel()
			out := &destination{}
			presenter := factory.make(out, diagnostic.LevelTrace)
			if err := presenter.Deliver(context.Background(), event); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			if err := presenter.ReportError(context.Background(), failure); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := out.String()
			for _, secret := range secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("secret %q written: %q", secret, got)
				}
			}
			if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "done") {
				t.Fatalf("redaction marker or surrounding text missing: %q", got)
			}
			if strings.ContainsAny(regexp.MustCompile(`\x1b\[(0|1;31|33|36|2|90)m`).ReplaceAllString(got, ""), "\x1b\x07\r\x00") {
				t.Fatalf("raw control written: %q", got)
			}
			if factory.json {
				records := decodeRecords(t, got)
				if len(records) != 2 || records[0]["type"] != "diagnostic" || records[1]["type"] != "error" {
					t.Fatalf("records = %v", records)
				}
				if facts := records[0]["facts"].(map[string]any); facts["status"] != float64(401) {
					t.Fatalf("non-string fact changed: %v", facts)
				}
				return
			}
			if !strings.Contains(got, `\x1b`) {
				t.Fatalf("control not escaped visibly: %q", got)
			}
		})
	}
}

func TestPresenterJSON(t *testing.T) {
	t.Parallel()

	levels := []diagnostic.Level{diagnostic.LevelError, diagnostic.LevelWarn, diagnostic.LevelInfo, diagnostic.LevelDebug, diagnostic.LevelTrace}
	names := []string{"error", "warn", "info", "debug", "trace"}
	auth := Errorf("query openai: %w", claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401, ProviderCode: "invalid_api_key"}))
	joined := errors.Join(auth, errors.New("cleanup failed"))

	t.Run("Deliver.5 NewJSONPresenter.1", func(t *testing.T) {
		t.Parallel()
		stdout := &destination{}
		stderr := &destination{}
		presenter := NewJSONPresenter(jsonl.NewEncoder(stderr), diagnostic.LevelInfo)
		viewport := &recordingViewport{}
		detach := presenter.Attach(viewport)
		defer detach()
		for _, level := range levels {
			event := sampleEvent()
			event.Level = level
			if err := presenter.Deliver(context.Background(), event); err != nil {
				t.Fatalf("deliver %d: %v", level, err)
			}
		}
		empty := diagnostic.Event{Type: "bare", Subsystem: "cli", Level: diagnostic.LevelInfo, Message: "m"}
		if err := presenter.Deliver(context.Background(), empty); err != nil {
			t.Fatalf("deliver empty: %v", err)
		}
		if stdout.String() != "" || len(viewport.snapshot()) != 0 {
			t.Fatalf("records left the diagnostic destination")
		}
		got := stderr.String()
		if strings.Contains(got, "\x1b") {
			t.Fatalf("ANSI in JSON: %q", got)
		}
		records := decodeRecords(t, got)
		if len(records) != len(levels)+1 {
			t.Fatalf("records = %v", records)
		}
		for i, record := range records[:len(levels)] {
			want := map[string]any{"type": "diagnostic", "name": "mcp_stderr", "subsystem": "tools", "level": names[i], "message": "server wrote to stderr", "facts": map[string]any{"server": "files"}}
			if !jsonEqual(record, want) {
				t.Fatalf("record %d = %v, want %v", i, record, want)
			}
		}
		if facts := records[len(levels)]["facts"]; !jsonEqual(facts, map[string]any{}) {
			t.Fatalf("empty facts = %v", facts)
		}
		var redacted bytes.Buffer
		if err := jsonl.NewEncoder(&redacted).Encode(jsonl.EventRecord(empty)); err != nil {
			t.Fatalf("encode reference: %v", err)
		}
		if !strings.HasSuffix(got, redacted.String()) {
			t.Fatalf("record is not jsonl.EventRecord: %q vs %q", got, redacted.String())
		}
	})

	t.Run("ReportError.3", func(t *testing.T) {
		t.Parallel()
		for i, level := range levels {
			out := &destination{}
			if err := NewJSONPresenter(jsonl.NewEncoder(out), level).ReportError(context.Background(), joined); err != nil {
				t.Fatalf("report at %s: %v", names[i], err)
			}
			records := decodeRecords(t, out.String())
			if len(records) != 1 {
				t.Fatalf("records at %s = %v", names[i], records)
			}
			record := records[0]
			if record["type"] != "error" || record["message"] != joined.Error() {
				t.Fatalf("record at %s = %v", names[i], record)
			}
			for key := range record {
				if key != "type" && key != "message" && key != "causes" {
					t.Fatalf("unlisted field %q", key)
				}
			}
			causes := record["causes"].([]any)
			if len(causes) != 2 {
				t.Fatalf("causes at %s = %v", names[i], causes)
			}
			first := causes[0].(map[string]any)
			if first["status"] != float64(401) || first["provider_code"] != "invalid_api_key" || first["message"] != auth.Error() {
				t.Fatalf("provider fields at %s = %v", names[i], first)
			}
			second := causes[1].(map[string]any)
			if !jsonEqual(second, map[string]any{"message": "cleanup failed"}) {
				t.Fatalf("plain cause at %s = %v", names[i], second)
			}
			_, hasLocation := first["location"]
			_, hasOperation := first["operation"]
			if (level >= diagnostic.LevelDebug) != (hasLocation && hasOperation) {
				t.Fatalf("location and operation at %s: %v", names[i], first)
			}
			if hasOperation && first["operation"] != "query openai" {
				t.Fatalf("operation = %v", first["operation"])
			}
			if hasLocation && !strings.HasPrefix(first["location"].(string), "internal/diag/output_test.go:") {
				t.Fatalf("location = %v", first["location"])
			}
			for key := range first {
				switch key {
				case "message", "operation", "location", "remedies", "status", "provider_code":
				default:
					t.Fatalf("unlisted cause field %q", key)
				}
			}
		}
	})

	t.Run("ReportError.3 stack at LevelTrace", func(t *testing.T) {
		t.Parallel()
		stacked := ErrorfWithStack("open config: %w", errors.New("no such file"))
		var want []string
		if locator, ok := errors.AsType[diagnostic.Locator](stacked); ok {
			for _, frame := range locator.Stack() {
				want = append(want, fmt.Sprintf("%s:%d %s", frame.File, frame.Line, frame.Function))
			}
		}
		if len(want) == 0 {
			t.Fatal("the stacked error captured no frames")
		}
		for i, level := range levels {
			out := &destination{}
			if err := NewJSONPresenter(jsonl.NewEncoder(out), level).ReportError(context.Background(), stacked); err != nil {
				t.Fatalf("report at %s: %v", names[i], err)
			}
			records := decodeRecords(t, out.String())
			if len(records) != 1 {
				t.Fatalf("records at %s = %v", names[i], records)
			}
			cause := records[0]["causes"].([]any)[0].(map[string]any)
			stack, hasStack := cause["stack"]
			if (level >= diagnostic.LevelTrace) != hasStack {
				t.Fatalf("stack at %s: %v", names[i], cause)
			}
			if !hasStack {
				continue
			}
			if !jsonEqual(stack, want) {
				t.Fatalf("stack at %s = %v, want %v", names[i], stack, want)
			}
			if !strings.HasPrefix(want[0], "internal/diag/output_test.go:") {
				t.Fatalf("first frame = %q, want this test's call", want[0])
			}
		}
		out := &destination{}
		if err := NewJSONPresenter(jsonl.NewEncoder(out), diagnostic.LevelTrace).ReportError(context.Background(), errors.New("plain")); err != nil {
			t.Fatalf("report plain: %v", err)
		}
		if strings.Contains(out.String(), `"stack"`) {
			t.Fatalf("an error without an origin stack claims one: %q", out.String())
		}
	})
}

func jsonEqual(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

// TestPresenterTrimsTrailingNewlines pins that an error text ending in
// newlines, as go_away_boilerplate's usage errors do, is shown without
// them: no empty continuation line in text, no "\n" at the end of a JSON
// message (ReportError.1, ReportError.3).
func TestPresenterTrimsTrailingNewlines(t *testing.T) {
	t.Parallel()
	usage := errors.New("'nosuchcmd' is not a valid argument\n")
	joined := errors.Join(usage, errors.New("second\n\n"))

	text := &destination{}
	if err := NewTextPresenter(text, Plain, diagnostic.LevelInfo).ReportError(context.Background(), usage); err != nil {
		t.Fatalf("report: %v", err)
	}
	if want := "error: 'nosuchcmd' is not a valid argument\n"; text.String() != want {
		t.Fatalf("text = %q, want %q", text.String(), want)
	}

	out := &destination{}
	if err := NewJSONPresenter(jsonl.NewEncoder(out), diagnostic.LevelInfo).ReportError(context.Background(), joined); err != nil {
		t.Fatalf("report: %v", err)
	}
	records := decodeRecords(t, out.String())
	if len(records) != 1 || records[0]["message"] != "'nosuchcmd' is not a valid argument\nsecond" {
		t.Fatalf("records = %v", records)
	}
	causes := records[0]["causes"].([]any)
	for i, want := range []string{"'nosuchcmd' is not a valid argument", "second"} {
		if got := causes[i].(map[string]any)["message"]; got != want {
			t.Fatalf("cause %d message = %q, want %q", i, got, want)
		}
	}
}
