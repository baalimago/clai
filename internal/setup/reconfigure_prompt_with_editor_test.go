package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func makeProfile(t *testing.T, dir, prompt string) config {
	t.Helper()
	p := text.Profile{
		Model:           "m",
		UseTools:        true,
		Tools:           []string{"t1"},
		Prompt:          prompt,
		SaveReplyAsConv: new(true),
	}
	b, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o755); err != nil {
		t.Fatalf("write profile %q: %v", fp, err)
	}
	return config{name: "p", filePath: fp}
}

func readPrompt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile %q: %v", path, err)
	}
	var p text.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("unmarshal profile %q: %v", path, err)
	}
	return p.Prompt
}

func makeEditorScript(t *testing.T, dir, body string, code int) string {
	t.Helper()
	sp := filepath.Join(dir, "ed.sh")
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	if body != "" {
		sb.WriteString("cat > \"$1\" <<'EOF'\n")
		sb.WriteString(body)
		sb.WriteString("\nEOF\n")
	}
	sb.WriteString("exit ")
	sb.WriteString(strconv.Itoa(code))
	sb.WriteString("\n")
	if err := os.WriteFile(sp, []byte(sb.String()), 0o755); err != nil {
		t.Fatalf("write editor script %q: %v", sp, err)
	}
	return sp
}

func TestPromptEdit_Success(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "hello\\nworld\\tX")
	body := "L1\nL2\tY"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		if err := actionReconfigurePromptWithEditor(cfg); err != nil {
			t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
		}
	})
	got := readPrompt(t, cfg.filePath)
	want := "L1\\nL2\\tY"
	testboil.FailTestIfDiff(t, got, want)
	if !strings.Contains(out, "updated field \"prompt\"") {
		t.Fatalf("stdout missing update marker: %q", out)
	}
}

func TestPromptEdit_Unicode(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "hi 🌟")
	body := "bye 🌈"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	got := readPrompt(t, cfg.filePath)
	testboil.FailTestIfDiff(t, got, body)
}

func TestPromptEdit_NoEditor(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "p")
	t.Setenv("EDITOR", "")
	err := actionReconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "EDITOR") {
		t.Fatalf("expected EDITOR error, got: %v", err)
	}
}

func TestPromptEdit_EditorFail(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "p")
	ed := makeEditorScript(t, dir, "", 1)
	t.Setenv("EDITOR", ed)
	err := actionReconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "failed to edit prompt with editor") {
		t.Fatalf("expected wrapped prompt-edit error, got: %v", err)
	}
}

func TestPromptEdit_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(fp, []byte("{"), 0o644); err != nil {
		t.Fatalf("write invalid json %q: %v", fp, err)
	}
	cfg := config{name: "p", filePath: fp}
	t.Setenv("EDITOR", "/bin/true")
	err := actionReconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected unmarshal error, got: %v", err)
	}
}

func TestPromptEdit_FileMode(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "x")
	body := "z\n"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	fi, err := os.Stat(cfg.filePath)
	if err != nil {
		t.Fatalf("stat profile %q: %v", cfg.filePath, err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("permissions = %v, want 0755", fi.Mode().Perm())
	}
}

func TestPromptEdit_StdoutMsg(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "x")
	ed := makeEditorScript(t, dir, "ok\n", 0)
	t.Setenv("EDITOR", ed)
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		if err := actionReconfigurePromptWithEditor(cfg); err != nil {
			t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
		}
	})
	if !strings.Contains(out, "updated field \"prompt\" at path") {
		t.Fatalf("stdout missing path update message: %q", out)
	}
}

func TestPromptEdit_LargePrompt(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := range 20000 {
		if i%3 == 0 {
			sb.WriteString("line\\n")
		}
		if i%5 == 0 {
			sb.WriteString("tab\\t")
		}
		sb.WriteString("X")
	}
	cfg := makeProfile(t, dir, sb.String())
	body := "A\nB\tC\nD\tE"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	got := readPrompt(t, cfg.filePath)
	want := "A\\nB\\tC\\nD\\tE"
	testboil.FailTestIfDiff(t, got, want)
}

func TestPromptEdit_NoOpEditor(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "orig\\nval")
	ed := makeEditorScript(t, dir, "", 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	got := readPrompt(t, cfg.filePath)
	if got != "orig\\nval" {
		t.Fatalf("expected unchanged prompt, got %q", got)
	}
}

func TestPromptEdit_JSONStability(t *testing.T) {
	dir := t.TempDir()
	p := map[string]any{
		"model":              "m1",
		"use_tools":          true,
		"tools":              []string{"a", "b"},
		"prompt":             "P\\nQ",
		"save-reply-as-conv": false,
		"extra":              map[string]any{"k": "v"},
	}
	b, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		t.Fatalf("marshal test profile: %v", err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o755); err != nil {
		t.Fatalf("write test profile %q: %v", fp, err)
	}
	cfg := config{name: "p", filePath: fp}
	ed := makeEditorScript(t, dir, "R\nS", 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	b2, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read updated profile %q: %v", fp, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b2, &m); err != nil {
		t.Fatalf("unmarshal updated profile %q: %v", fp, err)
	}
	want := "R\\nS"
	got, ok := m["prompt"].(string)
	if !ok {
		t.Fatalf("prompt type = %T, want string", m["prompt"])
	}
	testboil.FailTestIfDiff(t, got, want)
}

func TestPromptEdit_EmptyBoundary(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "")
	ed := makeEditorScript(t, dir, "", 0)
	t.Setenv("EDITOR", ed)
	if err := actionReconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("actionReconfigurePromptWithEditor(%q): %v", cfg.filePath, err)
	}
	got := readPrompt(t, cfg.filePath)
	if got != "" {
		t.Fatalf("expected empty prompt, got %q", got)
	}
}
