package tools

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
)

// captureSubCmdOut runs fn with os.Stdout redirected to a pipe and returns the
// captured output (mirrors internal/profiles/cmd_test.go).
func captureSubCmdOut(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		var out bytes.Buffer
		_, _ = out.ReadFrom(r)
		buf.Write(out.Bytes())
		close(done)
	}()

	fn()
	w.Close()
	os.Stdout = origStdout
	<-done
	return buf.String()
}

func TestSubCmd_ListsAliasUnderCanonicalTool(t *testing.T) {
	WithTestRegistry(t, func() {
		spec := models.Specification{Name: "canon", Description: "canonical tool description"}
		Registry.Set("canon", &mockLLMTool{spec: spec})
		Registry.SetAlias("old-name", "canon", &mockLLMTool{spec: spec})

		var err error
		got := captureSubCmdOut(t, func() {
			err = SubCmd(context.Background(), []string{"tools"})
		})
		if err == nil {
			t.Fatal("expected ErrUserInitiatedExit from SubCmd")
		}
		if !strings.Contains(got, "- canon (alias: old-name): ") {
			t.Fatalf("expected canonical entry with alias annotation, got:\n%s", got)
		}
		if !strings.Contains(got, "canonical tool description") {
			t.Fatalf("expected canonical description present, got:\n%s", got)
		}
		if strings.Contains(got, "- old-name:") {
			t.Fatalf("expected no standalone entry for the alias, got:\n%s", got)
		}
	})
}

func TestSubCmd_DetailResolvesAlias(t *testing.T) {
	WithTestRegistry(t, func() {
		spec := models.Specification{Name: "canon", Description: "desc"}
		Registry.Set("canon", &mockLLMTool{spec: spec})
		Registry.SetAlias("old-name", "canon", &mockLLMTool{spec: spec})

		var err error
		got := captureSubCmdOut(t, func() {
			err = SubCmd(context.Background(), []string{"tools", "old-name"})
		})
		if err == nil {
			t.Fatal("expected ErrUserInitiatedExit from SubCmd")
		}
		if !strings.Contains(got, `"name": "canon"`) {
			t.Fatalf("expected detail to resolve alias to canonical spec, got:\n%s", got)
		}
	})
}
