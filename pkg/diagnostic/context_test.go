package diagnostic_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/pkg/diagnostic"
)

type typedCause struct{ message string }

func (e *typedCause) Error() string { return e.message }

type fakeLocator struct {
	cause     error
	operation string
	location  diagnostic.SourceLocation
	stack     []diagnostic.SourceLocation
}

func (e fakeLocator) Error() string                       { return e.operation + ": " + e.cause.Error() }
func (e fakeLocator) Unwrap() error                       { return e.cause }
func (e fakeLocator) Operation() string                   { return e.operation }
func (e fakeLocator) Location() diagnostic.SourceLocation { return e.location }
func (e fakeLocator) Stack() []diagnostic.SourceLocation  { return e.stack }

type locatorFactory struct {
	name string
	new  func(error, string, bool) error
}

func locatorFactories() []locatorFactory {
	return []locatorFactory{
		{
			name: "production",
			new: func(cause error, operation string, stack bool) error {
				if stack {
					return diag.ErrorfWithStack(operation+": %w", cause)
				}
				return diag.Errorf(operation+": %w", cause)
			},
		},
		{
			name: "fake",
			new: func(cause error, operation string, stack bool) error {
				location := diagnostic.SourceLocation{Function: "fake.attach", File: "pkg/diagnostic/context_test.go", Line: 1}
				var frames []diagnostic.SourceLocation
				if stack {
					frames = []diagnostic.SourceLocation{location}
				}
				return fakeLocator{cause: cause, operation: operation, location: location, stack: frames}
			},
		},
	}
}

// TestLocatorContract proves Unwrap.1, Operation.1, Location.1, and Stack.1.
func TestLocatorContract(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sentinel")
	typed := &typedCause{message: "typed"}
	joined := errors.Join(sentinel, typed)

	for _, factory := range locatorFactories() {
		t.Run(factory.name, func(t *testing.T) {
			t.Parallel()

			for _, captureStack := range []bool{false, true} {
				wrapped := factory.new(joined, "connect MCP server", captureStack)
				var locator diagnostic.Locator
				if !errors.As(wrapped, &locator) {
					t.Fatalf("Location.1: %T does not implement diagnostic.Locator", wrapped)
				}
				if locator.Operation() != "connect MCP server" {
					t.Fatalf("Operation.1: operation = %q", locator.Operation())
				}
				if locator.Unwrap() != joined {
					t.Fatalf("Unwrap.1: Unwrap() did not return original cause")
				}
				if !errors.Is(wrapped, sentinel) {
					t.Fatal("Unwrap.1: errors.Is cannot reach sentinel")
				}
				var gotTyped *typedCause
				if !errors.As(wrapped, &gotTyped) || gotTyped != typed {
					t.Fatal("Unwrap.1: errors.As cannot reach typed cause")
				}
				if got, want := wrapped.Error(), "connect MCP server: sentinel\ntyped"; got != want {
					t.Fatalf("Unwrap.1: Error() = %q, want %q", got, want)
				}

				assertLocation(t, "Location.1", locator.Location())
				frames := locator.Stack()
				if !captureStack && len(frames) != 0 {
					t.Fatalf("Stack.1: disabled capture returned %d frames", len(frames))
				}
				if captureStack && len(frames) == 0 {
					t.Fatal("Stack.1: enabled capture returned no frames")
				}
				if captureStack && frames[0] != locator.Location() {
					t.Fatalf("Stack.1: first frame %#v is not attachment location %#v", frames[0], locator.Location())
				}
				if len(frames) > 32 {
					t.Fatalf("Stack.1: stack is not bounded: %d frames", len(frames))
				}
				for _, frame := range frames {
					assertLocation(t, "Stack.1", frame)
				}
			}
		})
	}
}

func assertLocation(t *testing.T, clause string, location diagnostic.SourceLocation) {
	t.Helper()
	if location.Function == "" || location.File == "" || location.Line <= 0 {
		t.Fatalf("%s: incomplete location: %#v", clause, location)
	}
	if filepath.IsAbs(location.File) {
		t.Fatalf("%s: absolute path exposed: %q", clause, location.File)
	}
}

// TestLocatorInventory proves that clai-owned formatting wrappers use Locator.
func TestLocatorInventory(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	var legacy []string
	for _, directory := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			// The implementation must use fmt.Errorf internally to preserve Go's
			// formatting rules; it is not an unmigrated context attachment.
			if filepath.ToSlash(relative) == "internal/diag/context.go" {
				return nil
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 || !isFmtErrorf(call.Fun) {
					return true
				}
				format, ok := constantString(call.Args[0])
				if !ok {
					return true
				}
				if strings.Contains(format, "%w") {
					position := fileSet.Position(call.Pos())
					legacy = append(legacy, fmt.Sprintf("%s:%d", filepath.ToSlash(relative), position.Line))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("inventory %s: %v", directory, err)
		}
	}
	if len(legacy) > 0 {
		sort.Strings(legacy)
		t.Fatalf("Location.1: fmt.Errorf context wrappers remain:\n%s", strings.Join(legacy, "\n"))
	}
}

func constantString(expression ast.Expr) (string, bool) {
	switch expression := expression.(type) {
	case *ast.BasicLit:
		if expression.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(expression.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if expression.Op != token.ADD {
			return "", false
		}
		left, leftOK := constantString(expression.X)
		right, rightOK := constantString(expression.Y)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func isFmtErrorf(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Errorf" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "fmt"
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
