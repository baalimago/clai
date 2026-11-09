package errctx_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/baalimago/clai/internal/errctx"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestMain(m *testing.M) { testenv.Main(m) }

const thisFile = "internal/errctx/errctx_test.go"

func line(t *testing.T) int {
	t.Helper()
	_, _, current, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return current
}

func locatorOf(t *testing.T, err error) diagnostic.Locator {
	t.Helper()
	locator, ok := errors.AsType[diagnostic.Locator](err)
	if !ok {
		t.Fatalf("%T is not a diagnostic.Locator", err)
	}
	return locator
}

func TestErrorfLocatesItsCaller(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")
	err, at := errctx.Errorf("read config: %w", cause), line(t)
	locator := locatorOf(t, err)
	if got := locator.Location(); got.File != thisFile || got.Line != at {
		t.Fatalf("Location() = %#v, want %s:%d", got, thisFile, at)
	}
	if got, want := locator.Operation(), "read config"; got != want {
		t.Fatalf("Operation() = %q, want %q", got, want)
	}
	if got, want := err.Error(), "read config: cause"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("cause is not reachable")
	}
	if len(locator.Stack()) != 0 {
		t.Fatal("Errorf captured a stack")
	}
}

func TestErrorfWithStackStartsAtItsCaller(t *testing.T) {
	t.Parallel()

	err, at := errctx.ErrorfWithStack("dial: %w", errors.New("refused")), line(t)
	locator := locatorOf(t, err)
	stack := locator.Stack()
	if len(stack) == 0 {
		t.Fatal("stack is empty")
	}
	if stack[0] != locator.Location() || stack[0].File != thisFile || stack[0].Line != at {
		t.Fatalf("stack[0] = %#v, location = %#v, want %s:%d", stack[0], locator.Location(), thisFile, at)
	}
	stack[0] = diagnostic.SourceLocation{}
	if locator.Stack()[0] == (diagnostic.SourceLocation{}) {
		t.Fatal("Stack exposed mutable internal storage")
	}
}

// delegate stands in for a wrapper such as diag.Errorf: one frame between the
// attachment site and errctx.
func delegate(capture errctx.Capture, format string, arguments ...any) error {
	return errctx.ErrorfAt(1, capture, format, arguments...)
}

func TestErrorfAtSkipsDelegatingFrames(t *testing.T) {
	t.Parallel()

	for _, capture := range []errctx.Capture{errctx.LocationOnly, errctx.WithStack} {
		err, at := delegate(capture, "op: %w", errors.New("cause")), line(t)
		locator := locatorOf(t, err)
		if got := locator.Location(); got.File != thisFile || got.Line != at {
			t.Fatalf("capture %d: Location() = %#v, want %s:%d", capture, got, thisFile, at)
		}
		stack := locator.Stack()
		if capture == errctx.LocationOnly && len(stack) != 0 {
			t.Fatalf("capture %d: unexpected stack", capture)
		}
		if capture == errctx.WithStack && (len(stack) == 0 || stack[0] != locator.Location()) {
			t.Fatalf("capture %d: stack %#v does not start at %#v", capture, stack, locator.Location())
		}
	}
}

func TestErrorfWithoutCauseRetainsFormatting(t *testing.T) {
	t.Parallel()

	err := errctx.Errorf("plain %s", "failure")
	if got, want := err.Error(), "plain failure"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if _, ok := errors.AsType[diagnostic.Locator](err); ok {
		t.Fatal("format without %w returned a Locator")
	}
}

func TestErrorfJoinsMultipleCauses(t *testing.T) {
	t.Parallel()

	first, second := errors.New("first"), errors.New("second")
	err := errctx.Errorf("combine %w and %w", first, second)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("multiple wrapped causes are not reachable")
	}
	if got, want := locatorOf(t, err).Operation(), "combine first and second"; got != want {
		t.Fatalf("Operation() = %q, want %q", got, want)
	}
}

func TestOperationTrimsTrailingCause(t *testing.T) {
	t.Parallel()

	err := errctx.Errorf("load; %w", errors.New("missing"))
	if got, want := locatorOf(t, err).Operation(), "load"; got != want {
		t.Fatalf("Operation() = %q, want %q", got, want)
	}
}

func TestSourceLocation(t *testing.T) {
	t.Parallel()

	unavailable := []diagnostic.SourceLocation{
		errctx.SourceLocation("", "/tmp/file.go", 1),
		errctx.SourceLocation("external/package.Func", "/tmp/file.go", 1),
		errctx.SourceLocation("github.com/baalimago/clai/pkg/diagnostic.Func", "", 1),
		errctx.SourceLocation("github.com/baalimago/clai/pkg/diagnostic.Func", "/tmp/file.go", 0),
	}
	for _, location := range unavailable {
		if location != (diagnostic.SourceLocation{}) {
			t.Fatalf("unavailable origin claimed as %#v", location)
		}
	}

	trimmed := errctx.SourceLocation("github.com/baalimago/clai/internal/jsonl.Encode", "/abs/path/internal/jsonl/encode.go", 7)
	if want := (diagnostic.SourceLocation{Function: "github.com/baalimago/clai/internal/jsonl.Encode", File: "internal/jsonl/encode.go", Line: 7}); trimmed != want {
		t.Fatalf("SourceLocation = %#v, want %#v", trimmed, want)
	}
	relative := errctx.SourceLocation("main.main", filepath.FromSlash("cmd/main.go"), 3)
	if relative.File != "cmd/main.go" {
		t.Fatalf("relative File = %q", relative.File)
	}
}
