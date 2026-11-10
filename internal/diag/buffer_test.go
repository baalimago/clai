package diag_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/diag/diagfake"

	. "github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/pkg/diagnostic"
)

var errImmediate = errors.New("immediate failed")

// snapshot is the fake's accepted events and its call count.
func snapshot(d *diagfake.Deliverer) ([]diagnostic.Event, int) {
	return d.Events(), d.Calls()
}

func event(typeName, message string) diagnostic.Event {
	return diagnostic.Event{Type: typeName, Subsystem: "agent", Level: diagnostic.LevelWarn, Message: message, Facts: []slog.Attr{slog.Int("n", 1)}}
}

func deliver(t *testing.T, deliverer BufferedDeliverer, events ...diagnostic.Event) {
	t.Helper()
	for _, e := range events {
		if err := deliverer.Deliver(context.Background(), e); err != nil {
			t.Fatalf("Deliver(%q): %v", e.Message, err)
		}
	}
}

func spillFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	return files
}

func messages(events []diagnostic.Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.Message)
	}
	return out
}

func finish(t *testing.T, deliverer BufferedDeliverer, immediate *diagfake.Deliverer) (paths []string, remainder []diagnostic.Event) {
	t.Helper()
	before, _ := snapshot(immediate)
	if err := deliverer.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, _ := snapshot(immediate)
	got = got[len(before):]
	if len(got) > 0 && len(got[0].Facts) == 1 && got[0].Facts[0].Key == "paths" {
		listed, ok := got[0].Facts[0].Value.Any().([]string)
		if !ok {
			t.Fatalf("paths fact = %v", got[0].Facts[0].Value)
		}
		return listed, got[1:]
	}
	return nil, got
}

func TestBufferedDelivererContract(t *testing.T) {
	t.Run("Deliver.2", func(t *testing.T) {
		dir := t.TempDir()
		immediate := diagfake.New()
		deliverer := NewBufferedDeliverer(dir, 4096, immediate)
		deliver(t, deliverer, event("first", "a"), event("second", "b"), event("first", "c"))
		if got, _ := snapshot(immediate); len(got) != 0 {
			t.Fatalf("buffered events reached immediate before Finish: %v", messages(got))
		}
		paths, remainder := finish(t, deliverer, immediate)
		if len(paths) != 0 || strings.Join(messages(remainder), ",") != "a,b,c" {
			t.Fatalf("paths = %v, remainder = %v", paths, messages(remainder))
		}
		if remainder[1].Type != "second" || remainder[1].Facts[0].Key != "n" {
			t.Fatalf("event changed in buffer: %+v", remainder[1])
		}

		oversizedImmediate := diagfake.New()
		oversized := NewBufferedDeliverer(t.TempDir(), 120, oversizedImmediate)
		deliver(t, oversized, event("big", strings.Repeat("é", 500)))
		_, remainder = finish(t, oversized, oversizedImmediate)
		if len(remainder) != 1 {
			t.Fatalf("oversized remainder = %d events", len(remainder))
		}
		if msg := remainder[0].Message; len(msg) >= 120 || !strings.HasSuffix(msg, "[truncated]") || !strings.HasPrefix(msg, "é") {
			t.Fatalf("oversized message not truncated and marked: %q", msg)
		}

		boundedDir := t.TempDir()
		boundedImmediate := diagfake.New()
		bounded := NewBufferedDeliverer(boundedDir, 200, boundedImmediate)
		for i := range 300 {
			deliver(t, bounded, event("many", fmt.Sprintf("event %d", i)))
		}
		paths, remainder = finish(t, bounded, boundedImmediate)
		if len(remainder) > 5 || len(paths) < 50 {
			t.Fatalf("buffer not bounded: %d remaining, %d spills", len(remainder), len(paths))
		}
		if last := remainder[len(remainder)-1].Message; last != "event 299" {
			t.Fatalf("last remaining event = %q", last)
		}
	})

	t.Run("NewBufferedDeliverer.1", func(t *testing.T) {
		cache := t.TempDir()
		t.Setenv("CLAI_CACHE_DIR", cache)

		dir := t.TempDir()
		immediate := diagfake.New()
		unbuffered := NewBufferedDeliverer(dir, 0, immediate)
		deliver(t, unbuffered, event("one", "x"), event("two", "y"), event("three", "z"))
		if files := spillFiles(t, dir); len(files) != 3 {
			t.Fatalf("size 0 spilled %d files, want 3", len(files))
		}
		paths, remainder := finish(t, unbuffered, immediate)
		if len(paths) != 3 || len(remainder) != 0 {
			t.Fatalf("size 0 Finish: paths %v, remainder %v", paths, messages(remainder))
		}

		emptyImmediate := diagfake.New()
		noDir := NewBufferedDeliverer("", 0, emptyImmediate)
		deliver(t, noDir, event("one", "fallback"))
		if got, _ := snapshot(emptyImmediate); strings.Join(messages(got), ",") != "fallback" {
			t.Fatalf("empty dir did not fall back: %v", messages(got))
		}

		negative := diagfake.New()
		negativeDir := t.TempDir()
		deliver(t, NewBufferedDeliverer(negativeDir, -1, negative), event("one", "x"))
		if files := spillFiles(t, negativeDir); len(files) != 1 {
			t.Fatalf("negative size spilled %d files, want 1", len(files))
		}

		if files := spillFiles(t, cache); len(files) != 0 {
			t.Fatalf("deliverer wrote under CLAI_CACHE_DIR: %v", files)
		}
	})
}

func TestBufferedDelivererOverflow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	immediate := diagfake.New()
	deliverer := NewBufferedDeliverer(dir, 100, immediate)
	for i := range 30 {
		deliver(t, deliverer, event("overflow", fmt.Sprintf("message-%02d", i)))
	}
	paths, _ := finish(t, deliverer, immediate)
	files := spillFiles(t, dir)
	if len(paths) < 5 || len(files) != len(paths) {
		t.Fatalf("spills listed %d, on disk %d", len(paths), len(files))
	}
	name := regexp.MustCompile(`^diagnostics-\d{8}T\d{6}\.\d{9}Z-\d+\.log$`)
	seen := map[string]bool{}
	next := 0
	for _, path := range paths {
		if seen[path] {
			t.Fatalf("spill file %s reused", path)
		}
		seen[path] = true
		if filepath.Dir(path) != dir || !name.MatchString(filepath.Base(path)) {
			t.Fatalf("spill file %s outside %s or not timestamped", path, dir)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("spill file not retained: %v", err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("spill file %s permissions %v", path, perm)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read spill: %v", err)
		}
		lines := strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n")
		for line := range lines {
			want := fmt.Sprintf("message-%02d", next)
			if !strings.Contains(line, want) || !strings.Contains(line, "overflow") || !strings.Contains(line, `n="1"`) {
				t.Fatalf("spill line %q, want %s in flush order", line, want)
			}
			next++
		}
	}
}

func unwritableDir(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}
	return filepath.Join(file, "diagnostics")
}

func TestBufferedDelivererFallback(t *testing.T) {
	t.Parallel()

	t.Run("Deliver.3", func(t *testing.T) {
		t.Parallel()
		immediate := diagfake.New()
		deliverer := NewBufferedDeliverer(unwritableDir(t), 100, immediate)
		for i := range 10 {
			deliver(t, deliverer, event("fallback", fmt.Sprintf("message-%02d", i)))
		}
		got, calls := snapshot(immediate)
		if len(got) == 0 || calls != len(got) {
			t.Fatalf("fallback delivered %d events in %d calls", len(got), calls)
		}
		for i, e := range got {
			if want := fmt.Sprintf("message-%02d", i); e.Message != want {
				t.Fatalf("fallback event %d = %q, want %q", i, e.Message, want)
			}
		}
		paths, remainder := finish(t, deliverer, immediate)
		if len(paths) != 0 || len(got)+len(remainder) != 10 {
			t.Fatalf("buffer not reset after fallback: %d delivered, %d remaining, paths %v", len(got), len(remainder), paths)
		}
	})

	t.Run("Deliver.3 double failure", func(t *testing.T) {
		t.Parallel()
		immediate := diagfake.Failing(errImmediate)
		deliverer := NewBufferedDeliverer(unwritableDir(t), 100, immediate)
		var failures int
		for i := range 10 {
			err := deliverer.Deliver(context.Background(), event("lost", fmt.Sprintf("message-%02d", i)))
			if err == nil {
				continue
			}
			failures++
			var pathErr *fs.PathError
			if !errors.Is(err, ErrUndeliverable) || !errors.Is(err, errImmediate) || !errors.As(err, &pathErr) {
				t.Fatalf("double failure error = %v", err)
			}
		}
		_, calls := snapshot(immediate)
		if failures == 0 || calls > 10 {
			t.Fatalf("double failures %d, immediate calls %d", failures, calls)
		}
		immediate.Fail(nil)
		_, remainder := finish(t, deliverer, immediate)
		if calls+len(remainder) != 10 {
			t.Fatalf("buffer grew after double failure: %d attempted, %d remaining", calls, len(remainder))
		}
	})
}

func TestBufferedDelivererFinish(t *testing.T) {
	t.Parallel()

	t.Run("Finish.1", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		immediate := diagfake.New()
		deliverer := NewBufferedDeliverer(dir, 100, immediate)
		for i := range 8 {
			deliver(t, deliverer, event("finish", fmt.Sprintf("message-%02d", i)))
		}
		paths, remainder := finish(t, deliverer, immediate)
		if len(paths) < 2 || len(remainder) == 0 {
			t.Fatalf("want several spills and a remainder: paths %v, remainder %v", paths, messages(remainder))
		}
		got, _ := snapshot(immediate)
		if strings.Contains(fmt.Sprint(got[0]), "message-00") {
			t.Fatalf("path event carries spill contents: %+v", got[0])
		}
		next := 0
		for _, path := range paths {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("spill file deleted: %v", err)
			}
			for line := range strings.SplitSeq(strings.TrimSuffix(string(content), "\n"), "\n") {
				if !strings.Contains(line, fmt.Sprintf("message-%02d", next)) {
					t.Fatalf("paths not in flush order at %s: %q", path, line)
				}
				next++
			}
		}
		for i, e := range remainder {
			if want := fmt.Sprintf("message-%02d", next+i); e.Message != want {
				t.Fatalf("remainder %d = %q, want %q", i, e.Message, want)
			}
		}
		if len(spillFiles(t, dir)) != len(paths) {
			t.Fatal("Finish removed spill files")
		}
	})

	t.Run("Finish.1 failure", func(t *testing.T) {
		t.Parallel()
		immediate := diagfake.New()
		deliverer := NewBufferedDeliverer(t.TempDir(), 100, immediate)
		for i := range 8 {
			deliver(t, deliverer, event("finish", fmt.Sprintf("message-%02d", i)))
		}
		immediate.Fail(errImmediate)
		err := deliverer.Finish(context.Background())
		if !errors.Is(err, ErrUndeliverable) || !errors.Is(err, errImmediate) {
			t.Fatalf("all deliveries failed: error = %v", err)
		}

		partial := diagfake.FailingAfter(1, errImmediate)
		withPartial := NewBufferedDeliverer(t.TempDir(), 100, partial)
		for i := range 8 {
			deliver(t, withPartial, event("finish", fmt.Sprintf("message-%02d", i)))
		}
		err = withPartial.Finish(context.Background())
		if err == nil || errors.Is(err, ErrUndeliverable) || !errors.Is(err, errImmediate) {
			t.Fatalf("partial delivery error = %v", err)
		}

		empty := diagfake.Failing(errImmediate)
		if err := NewBufferedDeliverer(t.TempDir(), 100, empty).Finish(context.Background()); err != nil {
			t.Fatalf("empty Finish = %v", err)
		}
	})
}
