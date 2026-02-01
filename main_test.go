package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type goldenFileTestCase struct {
	expect          string
	givenArgs       string
	givenEnvs       map[string]string
	wantOutExactly  string
	wantOutContains string
	wantErr         string
	wantStatusCode  int
}

// Test_goldenFile_calibration of the golden file tests to ensure they work
func Test_goldenFile_calibration(t *testing.T) {
	tcs := []goldenFileTestCase{
		{
			expect: "base-test",
			// These tests work by using the `test` chat model which is an
			// echo text querier. It will respond with whatever the input is
			givenArgs:      "-r -cm test q test",
			givenEnvs:      make(map[string]string),
			wantOutExactly: "test\n",
			wantErr:        "",
			wantStatusCode: 0,
		},
		{
			// This is a bit meta to ensure the goldenfile tests work
			expect:         "Multiple tests-test",
			givenArgs:      "-r -cm test q another test",
			givenEnvs:      make(map[string]string),
			wantOutExactly: "another test\n",
			wantErr:        "",
			wantStatusCode: 0,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.expect, func(t *testing.T) {
			oldArgs := os.Args
			t.Cleanup(func() {
				os.Args = oldArgs
			})

			for k, v := range tc.givenEnvs {
				t.Setenv(k, v)
			}
			var gotStatusCode int
			gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
				gotStatusCode = run(strings.Split(tc.givenArgs, " "))
			})

			testboil.FailTestIfDiff(t, gotStatusCode, tc.wantStatusCode)
			if tc.wantOutContains != "" {
				testboil.AssertStringContains(t, gotStdout, tc.wantOutContains)
			}
			if tc.wantOutExactly != "" {
				testboil.FailTestIfDiff(t, gotStdout, tc.wantOutExactly)
			}
		})
	}
}

func Test_goldenFile_CHAT_DIRSCOPED(t *testing.T) {
	// This test defines the desired top-level CLI contract for global replay (re)
	// and directory-scoped replay (dre). It is intentionally sequential and avoids
	// t.Parallel(), since it changes the process working directory (CWD).
	//
	// API expectations (TDD):
	// - `clai re` prints the most recent message from the *global* previous query (prevQuery.json)
	// - `clai dre` prints the most recent message from the *directory binding* for CWD
	// - `clai dre` errors (non-zero status code) if no binding exists for CWD
	//
	// Also validates the on-disk contract for directory-scoped chats:
	// - a conversation file exists for the scoped directory
	// - the directory binding file exists at conversations/dirs/<sha256(canonicalDir)>.json
	// - the binding points at the correct conversation (chat_id matches the conversation filename)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	confDir := t.TempDir()
	projRoot := t.TempDir()
	bar := filepath.Join(projRoot, "bar")
	baz := filepath.Join(bar, "baz")
	if err := os.MkdirAll(baz, 0o755); err != nil {
		t.Fatalf("MkdirAll(baz): %v", err)
	}

	// Helper to run a single CLI invocation with isolated env.
	runOne := func(t *testing.T, cwd string, args string) (string, int) {
		t.Helper()
		oldArgs := os.Args
		t.Cleanup(func() {
			os.Args = oldArgs
		})

		t.Setenv("CLAI_CONFIG_DIR", confDir)

		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir(%q): %v", cwd, err)
		}

		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split(args, " "))
		})
		return stdout, status
	}

	// 1) (/bar) query hello
	out, status := runOne(t, bar, "-r -cm test q hello")
	testboil.FailTestIfDiff(t, status, 0)
	// config init may print "created directory ..." before the model output
	testboil.AssertStringContains(t, out, "hello\n")

	// 2) (/bar) global replay matches last message from global prevQuery
	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	// 3) (/baz) dir replay should error because no binding exists yet
	_, status = runOne(t, baz, "-r dre")
	if status == 0 {
		t.Fatalf("expected non-zero status for 'dre' without binding")
	}

	// 4) (/baz) query baz
	out, status = runOne(t, baz, "-r -cm test q baz")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz\n")

	// 5) (/baz) dir replay matches baz latest
	out, status = runOne(t, baz, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	// 6) (/bar) dir replay matches bar latest
	out, status = runOne(t, bar, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	// 7) (/bar) global replay matches baz (global last query)
	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	// 8) (/baz) append new reply to existing dirscoped conv in /baz.
	// Use -dre + -re to ensure the reply uses the directory-scoped binding.
	out, status = runOne(t, baz, "-r -cm test -re -dre q hello3")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello3\n")

	// Validate conversation in <clai conf dir>/conversations/<chatid>.json exists
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
		if strings.HasSuffix(name, ".json") && name != "prevQuery.json" {
			convFiles = append(convFiles, filepath.Join(convDir, name))
		}
	}
	if len(convFiles) == 0 {
		t.Fatalf("expected at least one conversation json in %q", convDir)
	}

	// Find the (baz) conversation by looking for a file containing both "baz" and later "hello3"
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

	// Validate content in baz conversation; it should at this point have 2 assistant replies
	type msg struct {
		Role string `json:"role"`
	}
	type conv struct {
		Messages []msg `json:"messages"`
	}
	bazBytes, err := os.ReadFile(bazConvPath)
	if err != nil {
		t.Fatalf("ReadFile(baz conversation): %v", err)
	}
	// Be tolerant to schema changes by decoding only known fields.
	var c conv
	if err := json.Unmarshal(bazBytes, &c); err != nil {
		t.Fatalf("Unmarshal(baz conversation): %v", err)
	}
	var assistantCount int
	for _, m := range c.Messages {
		if m.Role == "assistant" {
			assistantCount++
		}
	}
	if assistantCount != 2 {
		t.Fatalf("expected 2 assistant messages in baz conversation, got %d", assistantCount)
	}

	// Calculate sha256 checksum for directory (same as implementation: hash of canonical abs path)
	bazAbs, err := filepath.Abs(baz)
	if err != nil {
		t.Fatalf("Abs(baz): %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(bazAbs)))
	hash := hex.EncodeToString(sum[:])

	// Validate dirscoped pointer in <clai conf dir>/conversations/dirs/<hash>.json exists
	bindingPath := filepath.Join(confDir, "conversations", "dirs", hash+".json")
	bindingBytes, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatalf("ReadFile(bindingPath %q): %v", bindingPath, err)
	}

	// Validate file points to correct file (chat_id matches conversation filename)
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
