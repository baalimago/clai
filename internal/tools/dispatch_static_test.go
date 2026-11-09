package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const toolsImportPath = "github.com/baalimago/clai/internal/tools"

// registryReaders are the files outside this package that may read the
// process registry. Each builds a catalog or validates a selection at setup;
// none of them turns a model's call into an execution.
var registryReaders = []string{
	"internal/text/querier_setup_tools.go",
	"internal/text/setup_querier.go",
}

// toolBoxImplementers are the directories whose RegisterTool calls are a
// model forwarding a registration to the completer it wraps.
var toolBoxImplementers = []string{"internal/vendors/"}

// A model's call runs only what the run offered. Three properties keep it
// so: nothing outside this package reads the registry except the setup files,
// the dispatch file never names the registry, and every registration on a
// model's tool box goes through Offered.Offer, so the offered tools and the
// callable tools are one set.
func TestModelCallDispatchCannotReachTheRegistry(t *testing.T) {
	module, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	fset := token.NewFileSet()
	parsed := 0
	err = filepath.WalkDir(module, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "testdata" || name == "worklogs" || (strings.HasPrefix(name, ".") && path != module) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(module, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		parsed++
		checkDispatchFile(t, fset, rel, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", module, err)
	}
	if parsed == 0 {
		t.Fatal("no Go file was checked")
	}
}

func checkDispatchFile(t *testing.T, fset *token.FileSet, rel string, file *ast.File) {
	t.Helper()
	toolsName := ""
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == toolsImportPath {
			toolsName = "tools"
			if imp.Name != nil {
				toolsName = imp.Name.Name
			}
		}
	}
	forwardsToCompleter := slices.ContainsFunc(toolBoxImplementers, func(dir string) bool { return strings.HasPrefix(rel, dir) })
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			if rel == "internal/tools/offered.go" && node.Name == "Registry" {
				t.Errorf("%s: the dispatch file names the process registry", fset.Position(node.Pos()))
			}
		case *ast.SelectorExpr:
			pkg, isIdent := node.X.(*ast.Ident)
			if isIdent && toolsName != "" && pkg.Name == toolsName && node.Sel.Name == "Registry" && !slices.Contains(registryReaders, rel) {
				t.Errorf("%s: reads the process tool registry; only setup files may (%v)", fset.Position(node.Pos()), registryReaders)
			}
		case *ast.CallExpr:
			sel, isSel := node.Fun.(*ast.SelectorExpr)
			if isSel && sel.Sel.Name == "RegisterTool" && rel != "internal/tools/offered.go" && !forwardsToCompleter {
				t.Errorf("%s: registers a tool on a model outside Offered.Offer, so it would be offered but not callable", fset.Position(node.Pos()))
			}
		}
		return true
	})
}
