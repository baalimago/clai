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

// helper: create a profile file with given prompt
func makeProfile(t *testing.T, dir, prompt string) config {
	t.Helper()
	p := text.Profile{
		Model:           "m",
		UseTools:        true,
		Tools:           []string{"t1"},
		Prompt:          prompt,
		SaveReplyAsConv: true,
	}
	b, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return config{name: "p", filePath: fp}
}

// helper: read prompt from profile file
func readPrompt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p text.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p.Prompt
}

// helper: create editor script that writes body and exits code
func makeEditorScript(t *testing.T, dir, body string, code int) string {
	t.Helper()
	sp := filepath.Join(dir, "ed.sh")
	sb := strings.Builder{}
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
		t.Fatal(err)
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
		if err := reconfigurePromptWithEditor(cfg); err != nil {
			t.Fatalf("err: %v", err)
		}
	})
	got := readPrompt(t, cfg.filePath)
	want := "L1\\nL2\\tY\\n"
	testboil.FailTestIfDiff(t, got, want)
	if !strings.Contains(out, "updated profile") {
		t.Fatalf("stdout miss update")
	}
}

func TestPromptEdit_Unicode(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "hi 🌟")
	body := "bye 🌈"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatalf("err: %v", err)
	}
	got := readPrompt(t, cfg.filePath)
	testboil.FailTestIfDiff(t, got, body+"\\n")
}

func TestPromptEdit_NoEditor(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "p")
	t.Setenv("EDITOR", "")
	err := reconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "EDITOR") {
		t.Fatalf("want EDITOR err, got %v", err)
	}
}

func TestPromptEdit_EditorFail(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "p")
	ed := makeEditorScript(t, dir, "", 1)
	t.Setenv("EDITOR", ed)
	err := reconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "unescapeEditWithEditor") {
		t.Fatalf("want unescape err, got %v", err)
	}
}

func TestPromptEdit_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(fp, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{name: "p", filePath: fp}
	t.Setenv("EDITOR", "/bin/true")
	err := reconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("want unmarshal err, got %v", err)
	}
}

func TestPromptEdit_WriteFail(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "x")
	body := "y\n"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := os.Chmod(cfg.filePath, 0o444); err != nil {
		t.Fatal(err)
	}
	err := reconfigurePromptWithEditor(cfg)
	if err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("want write err, got %v", err)
	}
}

func TestPromptEdit_FileMode(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "x")
	body := "z\n"
	ed := makeEditorScript(t, dir, body, 0)
	t.Setenv("EDITOR", ed)
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfg.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
}

func TestPromptEdit_StdoutMsg(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "x")
	ed := makeEditorScript(t, dir, "ok\n", 0)
	t.Setenv("EDITOR", ed)
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		if err := reconfigurePromptWithEditor(cfg); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "updated profile at path") {
		t.Fatalf("stdout msg missing")
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
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatal(err)
	}
	got := readPrompt(t, cfg.filePath)
	want := "A\\nB\\tC\\nD\\tE\\n"
	if got != want {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
}

func TestPromptEdit_NoOpEditor(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "orig\\nval")
	ed := makeEditorScript(t, dir, "", 0)
	t.Setenv("EDITOR", ed)
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatal(err)
	}
	got := readPrompt(t, cfg.filePath)
	if got != "orig\\nval" {
		t.Fatalf("unchanged expected, got %q", got)
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
	b, _ := json.MarshalIndent(p, "", "\t")
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config{name: "p", filePath: fp}
	ed := makeEditorScript(t, dir, "R\nS", 0)
	t.Setenv("EDITOR", ed)
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(fp)
	var m map[string]any
	if err := json.Unmarshal(b2, &m); err != nil {
		t.Fatal(err)
	}
	want := "R\\nS\\n"
	got := m["prompt"].(string)
	testboil.FailTestIfDiff(t, got, want)
}

func TestPromptEdit_EmptyBoundary(t *testing.T) {
	dir := t.TempDir()
	cfg := makeProfile(t, dir, "")
	ed := makeEditorScript(t, dir, "", 0)
	t.Setenv("EDITOR", ed)
	if err := reconfigurePromptWithEditor(cfg); err != nil {
		t.Fatal(err)
	}
	got := readPrompt(t, cfg.filePath)
	if got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
