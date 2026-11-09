package diagnostic_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPublicAPISurface proves F10 by rejecting exports outside its allowlist.
func TestPublicAPISurface(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	assertExportsAllowed(t, filepath.Join(root, "pkg", "diagnostic"), []string{
		"SourceLocation", "Locator", "Remedy", "Remedier", "Level", "OutputMode", "Type", "Event", "Deliverer", "SlogDeliverer", "Reporter",
	})
	assertExportsAllowed(t, filepath.Join(root, "pkg", "agent"), []string{
		"Agent", "CallUsageRecorder", "CompletedModelCall", "Metadata", "New", "NewTyped", "NewTypedMetadata", "Option", "Stoploss", "ToolCall", "ToolCallRecorder",
		"TypedMetadataQuerier", "TypedMetadataResponse", "TypedQuerier", "TypedResponse", "WithCmdBanList", "WithConfigDir", "WithLogger", "WithMaxToolCalls",
		"WithMcpServers", "WithModel", "WithOutputIsTerminal", "WithPrompt", "WithResponseFormat", "WithSlogLevel", "WithSlogRuneLimit", "WithStoploss",
		"WithToolCallRecorder", "WithToolGlobs", "WithTools", "WithUsageRecorder", "SetupQuerier", "DiagnosticTypes", "WithDiagnosticReporter", "WithDiagnosticDeliverer",
	})
}

func assertExportsAllowed(t *testing.T, directory string, allowedNames []string) {
	t.Helper()
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, name := range allowedNames {
		allowed[name] = struct{}{}
	}

	var unexpected []string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if declaration.Recv == nil {
					checkAllowed(declaration.Name.Name, allowed, false, &unexpected)
				}
			case *ast.GenDecl:
				allowedConstantType := false
				for _, specification := range declaration.Specs {
					switch specification := specification.(type) {
					case *ast.TypeSpec:
						checkAllowed(specification.Name.Name, allowed, false, &unexpected)
					case *ast.ValueSpec:
						if declaration.Tok == token.CONST && specification.Type != nil {
							identifier, ok := specification.Type.(*ast.Ident)
							allowedConstantType = ok && (identifier.Name == "Level" || identifier.Name == "OutputMode")
						}
						for _, name := range specification.Names {
							checkAllowed(name.Name, allowed, declaration.Tok == token.CONST && allowedConstantType, &unexpected)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect %s: %v", directory, err)
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Fatalf("F10: %s exports identifiers outside the allowlist: %s", directory, strings.Join(unexpected, ", "))
	}
}

func checkAllowed(name string, allowed map[string]struct{}, allowedConstant bool, unexpected *[]string) {
	if !ast.IsExported(name) || allowedConstant {
		return
	}
	if _, ok := allowed[name]; !ok {
		*unexpected = append(*unexpected, name)
	}
}
