// Package static holds the source-level checks: they parse the module instead
// of running clai, so they get their own test binary and -timeout budget
// beside e2e's CLI runs.
package static

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

const boilerplate = "github.com/baalimago/go_away_boilerplate/pkg/"

// runtimeFile is one parsed non-test Go file and the local names of its
// imports.
type runtimeFile struct {
	rel     string
	file    *ast.File
	imports map[string]string // local name -> import path
}

// sourceWalkDir reports a directory the module's own source is not under: a
// dot directory (.git, and a .claude worktree that holds a whole checkout), a
// test fixture tree, or a report tree.
func sourceWalkDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "testdata" || name == "worklogs"
}

// parsedFiles memoizes runtimeFiles by root: four static checks parse the same
// module, and -count parses it again per repetition. The files are read-only.
var parsedFiles sync.Map // strings.Join(roots, ",") -> []runtimeFile

// runtimeFiles parses every non-test Go file under roots of the module,
// skipping sourceWalkDir.
func runtimeFiles(t *testing.T, roots ...string) []runtimeFile {
	t.Helper()
	key := strings.Join(roots, ",")
	if cached, ok := parsedFiles.Load(key); ok {
		return cached.([]runtimeFile)
	}
	module := e2etest.ModuleRoot(t)
	fset := token.NewFileSet()
	var files []runtimeFile
	for _, root := range roots {
		start := filepath.Join(module, root)
		info, err := os.Stat(start)
		if err != nil {
			t.Fatalf("stat %s: %v", start, err)
		}
		walk := func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if sourceWalkDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(module, path)
			imports := map[string]string{}
			for _, spec := range f.Imports {
				p, _ := strconv.Unquote(spec.Path.Value)
				name := p[strings.LastIndex(p, "/")+1:]
				if spec.Name != nil {
					name = spec.Name.Name
				}
				imports[name] = p
			}
			files = append(files, runtimeFile{rel: filepath.ToSlash(rel), file: f, imports: imports})
			return nil
		}
		if !info.IsDir() {
			if err := walk(start, fs.FileInfoToDirEntry(info), nil); err != nil {
				t.Fatalf("parse %s: %v", start, err)
			}
			continue
		}
		if err := filepath.WalkDir(start, walk); err != nil {
			t.Fatalf("walk %s: %v", start, err)
		}
	}
	parsedFiles.Store(key, files)
	return files
}

// selectors calls fn for every pkg.Name selector whose pkg is an import.
func (f runtimeFile) selectors(fn func(importPath, name string, pos token.Pos)) {
	ast.Inspect(f.file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if p, ok := f.imports[id.Name]; ok {
				fn(p, sel.Sel.Name, sel.Pos())
			}
		}
		return true
	})
}

// TestDiagnosticRuntimeAncli proves criterion 8 from the parsed sources of
// main.go and every runtime package: nothing imports ancli, and no clai code
// reaches a go_away_boilerplate path that calls ancli. Those paths are
// cmd.Run (its help and error printing), the shutdown package, the
// cmd/version package, and a table without an input reader (its clear
// failures); clai dispatches through cmd.RunSilent and gives every table
// its input.
func TestDiagnosticRuntimeAncli(t *testing.T) {
	for _, f := range runtimeFiles(t, "main.go", "internal", "pkg") {
		for name, p := range f.imports {
			switch {
			case strings.HasSuffix(p, "/pkg/ancli"):
				t.Errorf("%s imports %s", f.rel, p)
			case p == boilerplate+"shutdown", p == boilerplate+"cmd/version":
				t.Errorf("%s imports %s (as %s), whose runtime paths call ancli", f.rel, p, name)
			}
		}
		f.selectors(func(p, name string, _ token.Pos) {
			if p == boilerplate+"cmd" && name == "Run" {
				t.Errorf("%s calls cmd.Run, whose help and error paths call ancli; use cmd.RunSilent", f.rel)
			}
		})
		// Every table chain must set its input: Run and selectNumbers call
		// ancli only for a table without one.
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" {
				return true
			}
			if chainCalls(call, "New", boilerplate+"table", f) && !chainHas(call, "WithInput") {
				t.Errorf("%s runs a table without WithInput", f.rel)
			}
			return true
		})
	}
}

// chainCalls reports whether a method chain starts with pkg.fn of importPath.
func chainCalls(expr ast.Expr, fn, importPath string, f runtimeFile) bool {
	for {
		switch e := expr.(type) {
		case *ast.CallExpr:
			expr = e.Fun
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.SelectorExpr:
			if id, ok := e.X.(*ast.Ident); ok {
				return e.Sel.Name == fn && f.imports[id.Name] == importPath
			}
			expr = e.X
		default:
			return false
		}
	}
}

func chainHas(expr ast.Expr, method string) bool {
	for {
		switch e := expr.(type) {
		case *ast.CallExpr:
			expr = e.Fun
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.SelectorExpr:
			if e.Sel.Name == method {
				return true
			}
			expr = e.X
		default:
			return false
		}
	}
}

// directWriteAllowed is the composition root and the allowlist of
// criterion 17: the CLI, the stream provider, and the test servers, plus the
// named test-harness files that only test binaries link (testHarnessFiles).
func directWriteAllowed(rel string) bool {
	for _, prefix := range []string{"internal/cli/", "internal/stdio/", "internal/tools/mcp/testserver/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return slices.Contains(testHarnessFiles, rel)
}

// testHarnessFiles are the files of the test harness that touch the process
// streams: TestMain's own failure output, Quiet, and procenv's process Env.
// TestTestHarnessIsTestOnly proves no runtime file links them.
var testHarnessFiles = []string{
	"internal/testenv/testenv.go",
	"internal/testenv/quiet.go",
	"internal/testenv/procenv/procenv.go",
}

// TestTestHarnessIsTestOnly proves the harness exemption of criterion 17
// stays out of clai: no non-test file outside internal/testenv imports it.
func TestTestHarnessIsTestOnly(t *testing.T) {
	for _, f := range runtimeFiles(t, "main.go", "internal", "pkg") {
		if strings.HasPrefix(f.rel, "internal/testenv/") {
			continue
		}
		for _, p := range f.imports {
			if strings.HasPrefix(p, "github.com/baalimago/clai/internal/testenv") {
				t.Errorf("%s imports the test harness %s", f.rel, p)
			}
		}
	}
}

// TestJSONNoDirectWrites proves criterion 17 from a parse of every non-test
// Go file under internal/ and pkg/: no reference to os.Stdout, os.Stderr,
// os.Stdin, a fmt.Print function, or the print builtins outside the
// allowlist.
func TestJSONNoDirectWrites(t *testing.T) {
	files := runtimeFiles(t, "internal", "pkg")
	if len(files) < 100 {
		t.Fatalf("parsed %d files, want the whole tree", len(files))
	}
	for _, f := range files {
		if directWriteAllowed(f.rel) {
			continue
		}
		f.selectors(func(p, name string, _ token.Pos) {
			switch {
			case p == "os" && (name == "Stdout" || name == "Stderr" || name == "Stdin"):
				t.Errorf("%s references os.%s", f.rel, name)
			case p == "fmt" && (name == "Print" || name == "Printf" || name == "Println"):
				t.Errorf("%s calls fmt.%s", f.rel, name)
			}
		})
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "println" || id.Name == "print") {
				t.Errorf("%s calls the %s builtin", f.rel, id.Name)
			}
			return true
		})
	}
}

// TestNoWriterBypassesTheRunStreams guards the other ways a byte could reach
// the process streams outside the run's format: Go's standard logger and the
// default slog logger, which only the composition root may route, a file
// built over a raw descriptor, and a child process handed a process stream.
// A child's Stdout and Stderr left unset are the null device, never
// inherited, so naming no process stream keeps every child off fd 1 and 2
// (TestJSONNoDirectWrites).
func TestNoWriterBypassesTheRunStreams(t *testing.T) {
	defaultLogger := []string{"Debug", "Info", "Warn", "Error", "Log", "LogAttrs", "DebugContext", "InfoContext", "WarnContext", "ErrorContext", "Default", "SetDefault"}
	for _, f := range runtimeFiles(t, "main.go", "internal", "pkg") {
		if directWriteAllowed(f.rel) {
			continue
		}
		f.selectors(func(p, name string, _ token.Pos) {
			switch {
			case p == "log":
				t.Errorf("%s uses the standard logger: log.%s", f.rel, name)
			case p == "log/slog" && slices.Contains(defaultLogger, name):
				t.Errorf("%s uses the default slog logger: slog.%s", f.rel, name)
			case p == "os" && name == "NewFile":
				t.Errorf("%s builds a file over a raw descriptor: os.NewFile", f.rel)
			}
		})
	}
}

// TestPrunedSkillsDiagnostics proves phase 10 criterion 1 from every file of
// the module, production, tests, generated source and documents alike: the
// removed skills logger, its parser and its level type have no reference
// outside the allowed hits, which are the worklogs (the phase's Removes table
// and the historical worklogs). The names are built from parts so this file
// is no hit of its own.
func TestPrunedSkillsDiagnostics(t *testing.T) {
	join := func(parts ...string) string { return strings.Join(parts, "") }
	removed := regexp.MustCompile(strings.Join([]string{
		`\b(` + strings.Join([]string{
			join("new", "Logger"), join("parse", "LogLevel"), join("Parse", "LogLevel", "FromEnv"),
			join("LogLevel", "Info"), join("LogLevel", "Warn"), join("LogLevel", "Error"),
		}, "|") + `)\b`,
		regexp.QuoteMeta(join("skills.", "LogLevel")),
		regexp.QuoteMeta(join("skills.", "logger")),
		regexp.QuoteMeta(join("Options.", "LogLevel")),
		regexp.QuoteMeta(join("internal/skills/", "logger.go")),
	}, "|"))
	hints := []string{join("Log", "Level"), join("new", "Logger"), join("skills.", "logger"), join("skills/", "logger.go")}
	module := e2etest.ModuleRoot(t)
	scanned := 0
	err := filepath.WalkDir(module, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(module, path)
		if d.IsDir() {
			if sourceWalkDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		// Every removed name holds one of these literals; a file without
		// them cannot match, so the regexp runs only where one occurs.
		if !slices.ContainsFunc(hints, func(hint string) bool { return bytes.Contains(content, []byte(hint)) }) {
			return nil
		}
		for i, line := range strings.Split(string(content), "\n") {
			if removed.MatchString(line) {
				t.Errorf("%s:%d references a removed skills diagnostic name: %q", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned %d files, want the whole module", scanned)
	}
}

// TestNoInvisibleRunesInSource keeps runes that read as a space or as
// nothing out of Go source, where a reviewer cannot see them: the line and
// paragraph separators and the replacement character are written as
// escapes (R4-07).
func TestNoInvisibleRunesInSource(t *testing.T) {
	module := e2etest.ModuleRoot(t)
	invisible := []rune{'\u2028', '\u2029', '\uFFFD', '\u200B', '\uFEFF'}
	var found []string
	err := filepath.WalkDir(module, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if sourceWalkDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, r := range invisible {
			if bytes.ContainsRune(source, r) {
				rel, _ := filepath.Rel(module, path)
				found = append(found, rel+": "+strconv.QuoteRuneToASCII(r))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	if len(found) > 0 {
		t.Fatalf("invisible runes in Go source; write them as escapes:\n%s", strings.Join(found, "\n"))
	}
}
