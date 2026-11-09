package diag

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestErrorfFormattingAndCauses(t *testing.T) {
	t.Parallel()

	first := errors.New("first")
	second := errors.New("second")
	err := Errorf("combine %w and %w", first, second)
	if got, want := err.Error(), "combine first and second"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("multiple wrapped causes are not reachable")
	}
	var locator diagnostic.Locator
	if !errors.As(err, &locator) {
		t.Fatal("Errorf with wrapped causes did not return a Locator")
	}
	if locator.Location().File != "internal/diag/context_test.go" {
		t.Fatalf("location file = %q", locator.Location().File)
	}
	if filepath.IsAbs(locator.Location().File) {
		t.Fatalf("absolute location = %q", locator.Location().File)
	}
}

func TestErrorfWithoutCauseRetainsFormatting(t *testing.T) {
	t.Parallel()

	err := Errorf("plain %s", "failure")
	if got, want := err.Error(), "plain failure"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if _, ok := errors.AsType[diagnostic.Locator](err); ok {
		t.Fatal("format without %w unexpectedly returned a Locator")
	}
}

func TestErrorfStackIsDefensiveCopy(t *testing.T) {
	t.Parallel()

	err := ErrorfWithStack("operation: %w", errors.New("cause"))
	var locator diagnostic.Locator
	if !errors.As(err, &locator) {
		t.Fatal("ErrorfWithStack did not return a Locator")
	}
	first := locator.Stack()
	if len(first) == 0 {
		t.Fatal("captured stack is empty")
	}
	first[0] = diagnostic.SourceLocation{}
	if locator.Stack()[0] == (diagnostic.SourceLocation{}) {
		t.Fatal("Stack exposed mutable internal storage")
	}
}

func TestSourceLocationRejectsUnavailableOrigins(t *testing.T) {
	t.Parallel()

	cases := []diagnostic.SourceLocation{
		sourceLocation("", "/tmp/file.go", 1),
		sourceLocation("external/package.Func", "/tmp/file.go", 1),
		sourceLocation("github.com/baalimago/clai/pkg/diagnostic.Func", "", 1),
		sourceLocation("github.com/baalimago/clai/pkg/diagnostic.Func", "/tmp/file.go", 0),
	}
	for _, location := range cases {
		if location != (diagnostic.SourceLocation{}) {
			t.Fatalf("unavailable origin claimed as %#v", location)
		}
	}
}
