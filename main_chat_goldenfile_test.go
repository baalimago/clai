package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_CHAT_DIRSCOPED(t *testing.T) {
	type chatDirInfo struct {
		Scope         string         `json:"scope"`
		ChatID        string         `json:"chat_id"`
		RepliesByRole map[string]int `json:"replies_by_role"`
		TokensTotal   int            `json:"tokens_total"`
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	projRoot := t.TempDir()
	bar := filepath.Join(projRoot, "bar")
	baz := filepath.Join(bar, "baz")
	if mkdirErr := os.MkdirAll(baz, 0o755); mkdirErr != nil {
		t.Fatalf("MkdirAll(baz): %v", mkdirErr)
	}

	runOne := func(t *testing.T, cwd string, args string) (string, int) {
		t.Helper()
		oldArgs := os.Args
		t.Cleanup(func() {
			os.Args = oldArgs
		})

		if chDirErr := os.Chdir(cwd); chDirErr != nil {
			t.Fatalf("Chdir(%q): %v", cwd, chDirErr)
		}

		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split(args, " "))
		})
		return stdout, status
	}

	parseChatDir := func(t *testing.T, stdout string) chatDirInfo {
		t.Helper()
		trimmed := strings.TrimSpace(strings.TrimSuffix(stdout, "\a"))
		if trimmed == "" {
			t.Fatalf("expected non-empty stdout")
		}
		if trimmed == "{}" {
			return chatDirInfo{}
		}
		var got chatDirInfo
		if err := json.Unmarshal([]byte(trimmed), &got); err != nil {
			t.Fatalf("Unmarshal(chat dir json): %v\nstdout=%q", err, stdout)
		}
		return got
	}

	_, status := runOne(t, bar, "-r -cm test chat dir")
	if status != 0 {
		t.Fatalf("expected zero status for 'chat dir' when empty, got %v", status)
	}

	out, status := runOne(t, bar, "-r -cm test q hello")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "hello\n\a")

	out, status = runOne(t, bar, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	barInfo := parseChatDir(t, out)
	if barInfo.Scope != "dir" {
		t.Fatalf("expected scope=dir, got %q", barInfo.Scope)
	}
	if barInfo.ChatID == "" {
		t.Fatalf("expected non-empty chat_id for dir scope, got %q", barInfo.ChatID)
	}
	if barInfo.RepliesByRole == nil {
		t.Fatalf("expected replies_by_role to be present")
	}
	if barInfo.RepliesByRole["user"] < 1 {
		t.Fatalf("expected at least 1 user message, got %v", barInfo.RepliesByRole)
	}
	if barInfo.TokensTotal < 0 {
		t.Fatalf("expected non-negative tokens_total, got %d", barInfo.TokensTotal)
	}

	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	_, status = runOne(t, baz, "-r dre")
	if status == 0 {
		t.Fatalf("expected non-zero status for 'dre' without binding")
	}

	out, status = runOne(t, baz, "-r -cm test q baz")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "baz\n\a")

	out, status = runOne(t, baz, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	bazInfo := parseChatDir(t, out)
	if bazInfo.Scope != "dir" {
		t.Fatalf("expected scope=dir, got %q", bazInfo.Scope)
	}
	if bazInfo.ChatID == "" {
		t.Fatalf("expected non-empty chat_id for dir scope")
	}
	if bazInfo.RepliesByRole == nil {
		t.Fatalf("expected replies_by_role to be present")
	}
	if bazInfo.RepliesByRole["user"] < 1 {
		t.Fatalf("expected at least 1 user message, got %v", bazInfo.RepliesByRole)
	}

	out, status = runOne(t, baz, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	out, status = runOne(t, bar, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	out, status = runOne(t, baz, "-r -cm test -re -dre q hello3")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "hello3\n\a")

	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var convFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") && name != "globalScope.json" {
			convFiles = append(convFiles, filepath.Join(convDir, name))
		}
	}
	if len(convFiles) == 0 {
		t.Fatalf("expected at least one conversation json in %q", convDir)
	}

	var bazConvPath string
	for _, p := range convFiles {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("ReadFile(%q): %v", p, readErr)
		}
		s := string(b)
		if strings.Contains(s, "baz") && strings.Contains(s, "hello3") {
			bazConvPath = p
			break
		}
	}
	if bazConvPath == "" {
		t.Fatalf("could not find baz conversation file in %q", convDir)
	}

	var bazChat models.Chat
	bazBytes, err := os.ReadFile(bazConvPath)
	if err != nil {
		t.Fatalf("ReadFile(baz conversation): %v", err)
	}
	if unmarshalErr := json.Unmarshal(bazBytes, &bazChat); unmarshalErr != nil {
		t.Fatalf("Unmarshal(baz conversation): %v", unmarshalErr)
	}
	var gotSysMsgs []string
	for _, m := range bazChat.Messages {
		if m.Role == "system" {
			gotSysMsgs = append(gotSysMsgs, m.Content)
		}
	}

	if !slices.Contains(gotSysMsgs, "baz") || !slices.Contains(gotSysMsgs, "hello3") {
		t.Fatalf("expected systemMessages: '%v' to contain: '%v'", gotSysMsgs, []string{"baz", "hello3"})
	}

	bazAbs, err := filepath.Abs(baz)
	if err != nil {
		t.Fatalf("Abs(baz): %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(bazAbs)))
	hash := hex.EncodeToString(sum[:])

	bindingPath := filepath.Join(confDir, "conversations", "dirs", hash+".json")
	bindingBytes, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatalf("ReadFile(bindingPath %q): %v", bindingPath, err)
	}

	type binding struct {
		ChatID string `json:"chat_id"`
	}
	var b binding
	if err := json.Unmarshal(bindingBytes, &b); err != nil {
		t.Fatalf("Unmarshal(binding): %v", err)
	}
	if b.ChatID == "" {
		t.Fatalf("expected non-empty chat_id in binding file %q", bindingPath)
	}
	wantChatFile := filepath.Join(confDir, "conversations", b.ChatID+".json")
	if filepath.Clean(wantChatFile) != filepath.Clean(bazConvPath) {
		t.Fatalf("binding chat_id points to %q, but baz conversation is %q", wantChatFile, bazConvPath)
	}
}
