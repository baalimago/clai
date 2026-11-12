package remote

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/utils"
)

// syncBuffer is a destination a test reads while the run writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// firstReadSnapshot records what a destination held when the run first
// read its input.
type firstReadSnapshot struct {
	in      io.Reader
	watch   *syncBuffer
	once    sync.Once
	atFirst string
}

func (r *firstReadSnapshot) Read(p []byte) (int, error) {
	r.once.Do(func() { r.atFirst = r.watch.String() })
	return r.in.Read(p)
}

type inputRun struct {
	status         int
	stdout, stderr string
	// seenAtFirstRead is the watched destination at the first read.
	seenAtFirstRead string
}

func runWithInput(t *testing.T, args string, stdin io.Reader, stdinTerminal bool, watchStderr bool) inputRun {
	t.Helper()
	// A run may set the process-wide config modes, and an earlier test may
	// have left them set; Harness.RunOne resets them the same way.
	// setup.Input, the trust input, outlives a `clai setup` run in the process.
	input := setup.Input
	reset := func() {
		setup.Input = input
		utils.Live = true
		utils.ReadonlyConfig = false
		utils.NoCreateConfig = false
	}
	setup.Input = nil
	utils.Live, utils.ReadonlyConfig, utils.NoCreateConfig = true, false, false
	t.Cleanup(reset)
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	watch := stdout
	if watchStderr {
		watch = stderr
	}
	reader := &firstReadSnapshot{in: stdin, watch: watch}
	status := cli.RunWithStreams(strings.Fields(args), harness.Deps, cli.Streams{Stdin: reader, Stdout: stdout, Stderr: stderr, StdinTerminal: stdinTerminal})
	return inputRun{status: status, stdout: stdout.String(), stderr: stderr.String(), seenAtFirstRead: reader.atFirst}
}

func records(t *testing.T, out string) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil || !strings.HasPrefix(line, `{"type":"`) {
			t.Fatalf("line is not a record: %q (%v)", line, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

// assertTableSelectPrompt checks a table run under -json: its rows are
// output records, and its selection prompt is a select prompt record, the
// last record on stdout before the first read (F13, F14).
func assertTableSelectPrompt(t *testing.T, run inputRun) {
	t.Helper()
	before := records(t, run.seenAtFirstRead)
	if len(before) < 2 {
		t.Fatalf("no table records before the read; stdout %q", run.stdout)
	}
	if last := before[len(before)-1]; last["type"] != "prompt" || last["kind"] != "select" || !strings.HasPrefix(last["text"].(string), "(select, ") {
		t.Fatalf("the last record before the read is %v, want the table's select prompt", last)
	}
	for _, rec := range records(t, run.stdout) {
		switch rec["type"] {
		case "output":
			if strings.Contains(rec["text"].(string), "(select, ") {
				t.Fatalf("the selection prompt is an output record: %v", rec)
			}
		case "prompt":
		default:
			t.Fatalf("table record %v is neither output nor prompt", rec)
		}
	}
}

// TestJSONModeInput proves criteria 14 and 15: under -json a question is a
// record that reaches its destination before the read, the same input
// answers it, a prompt skipped without a terminal stays skipped, and
// $EDITOR is refused before it starts.
func TestJSONModeInput(t *testing.T) {
	t.Run("trust prompt on a terminal", func(t *testing.T) {
		env := setupRemoteE2E(t)
		tty := filepath.Join(t.TempDir(), "tty")
		if err := os.WriteFile(tty, []byte("y\n"), 0o600); err != nil {
			t.Fatalf("write tty stand-in: %v", err)
		}
		t.Setenv("TTY", tty)
		// The trust answer is read from the terminal ($TTY), as without -json.
		got := runWithInput(t, "-json -prp cchub:ada/bot q hello", strings.NewReader(""), true, true)
		if got.status != 0 {
			t.Fatalf("status %d, want 0; stderr %q", got.status, got.stderr)
		}
		var prompt map[string]any
		recs := records(t, got.stderr)
		if len(recs) > 0 && recs[0]["type"] == "prompt" {
			prompt = recs[0]
		}
		if prompt == nil || prompt["kind"] != "trust" || !strings.Contains(prompt["text"].(string), "ada/bot") {
			t.Fatalf("the first stderr record is not the trust prompt: %q", got.stderr)
		}
		// R3-04: the question is plain text; the record's choices carry y/N.
		text := prompt["text"].(string)
		for _, decoration := range []string{"▸", "·", "│", "[y/N]", "   "} {
			if strings.Contains(text, decoration) {
				t.Fatalf("trust prompt text carries the terminal look %q: %q", decoration, text)
			}
		}
		if !strings.Contains(text, "sha256: ") || !strings.HasSuffix(text, "Trust cchub:ada/bot?") {
			t.Fatalf("trust prompt text = %q, want the sha256 and the question", text)
		}
		if choices, _ := prompt["choices"].([]any); len(choices) != 2 || choices[0] != "y" || choices[1] != "N" {
			t.Fatalf("trust prompt choices = %v, want [y N]", prompt["choices"])
		}
		if env.trusted(t) == "" {
			t.Fatalf("the answer y did not record trust; stdout %q", got.stdout)
		}
	})
	t.Run("prompt skipped without a terminal", func(t *testing.T) {
		setupRemoteE2E(t)
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("open %s: %v", os.DevNull, err)
		}
		t.Cleanup(func() { _ = devNull.Close() })
		text := runWithInput(t, "-prp cchub:ada/bot q hello", devNull, false, true)
		asJSON := runWithInput(t, "-json -prp cchub:ada/bot q hello", devNull, false, true)
		if text.status != remote.ExitRefused || asJSON.status != text.status {
			t.Fatalf("statuses text %d json %d, want both %d", text.status, asJSON.status, remote.ExitRefused)
		}
		for _, rec := range records(t, asJSON.stderr) {
			if rec["type"] == "prompt" {
				t.Fatalf("a prompt record was written without a terminal: %v", rec)
			}
		}
	})
	t.Run("table selection given a line", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(confDir)
		e2etest.SeedConv(t, filepath.Join(confDir, "conversations"), e2etest.MakeConv("conv-a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), e2etest.MsgUser("a prompt"), e2etest.MsgAsst("a reply")))
		text := runWithInput(t, "-r chat list", strings.NewReader("q\n"), false, false)
		asJSON := runWithInput(t, "-json -r chat list", strings.NewReader("q\n"), false, false)
		if asJSON.status != text.status {
			t.Fatalf("statuses text %d json %d", text.status, asJSON.status)
		}
		assertTableSelectPrompt(t, asJSON)
		if !strings.Contains(asJSON.stdout, "2026-01-01 00:00:00") {
			t.Fatalf("the table does not list the chat: %q", asJSON.stdout)
		}
	})
	t.Run("setup table selection given a line", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(confDir)
		text := runWithInput(t, "setup", strings.NewReader("q\n"), false, false)
		asJSON := runWithInput(t, "-json setup", strings.NewReader("q\n"), false, false)
		if asJSON.status != text.status {
			t.Fatalf("statuses text %d json %d", text.status, asJSON.status)
		}
		assertTableSelectPrompt(t, asJSON)
		if !strings.Contains(asJSON.stdout, "general config") {
			t.Fatalf("the table does not list the setup categories: %q", asJSON.stdout)
		}
	})
	t.Run("$EDITOR refused", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(confDir)
		e2etest.SeedConv(t, filepath.Join(confDir, "conversations"), e2etest.MakeConv("conv-a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), e2etest.MsgUser("a prompt"), e2etest.MsgAsst("a reply")))
		marker := filepath.Join(t.TempDir(), "editor-started")
		editor := filepath.Join(t.TempDir(), "editor.sh")
		if err := os.WriteFile(editor, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
			t.Fatalf("write editor: %v", err)
		}
		t.Setenv("EDITOR", editor)
		got := runWithInput(t, "-json -r chat list 0 e 1", strings.NewReader(""), false, false)
		if got.status != 1 {
			t.Fatalf("status %d, want 1; stderr %q", got.status, got.stderr)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("$EDITOR was started under -json")
		}
		var refused bool
		for _, rec := range records(t, got.stderr) {
			if rec["type"] == "error" && strings.Contains(rec["message"].(string), "$EDITOR") {
				refused = true
			}
		}
		if !refused {
			t.Fatalf("no error record naming $EDITOR: %q", got.stderr)
		}
	})
	// R1-03, F11: /dev/null keeps its identity under -json, so it reads as
	// no input in both formats.
	t.Run("stdin /dev/null is no input in both formats", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(confDir)
		run := func(args string) (int, string, string) {
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatalf("open %s: %v", os.DevNull, err)
			}
			defer devNull.Close()
			var stdout, stderr syncBuffer
			status := cli.RunWithStreams(strings.Fields(args), harness.Deps, cli.Streams{Stdin: devNull, Stdout: &stdout, Stderr: &stderr})
			return status, stdout.String(), stderr.String()
		}
		textStatus, _, textErr := run("-r -cm test q")
		jsonStatus, jsonOut, jsonErr := run("-json -r -cm test q")
		if textStatus != 1 || jsonStatus != textStatus {
			t.Fatalf("statuses text %d json %d, want both 1; text stderr %q json stdout %q stderr %q", textStatus, jsonStatus, textErr, jsonOut, jsonErr)
		}
		var refused bool
		for _, rec := range records(t, jsonErr) {
			refused = refused || rec["type"] == "error"
		}
		if !refused || strings.Contains(jsonOut, "reply") {
			t.Fatalf("want an error record and no reply; stdout %q stderr %q", jsonOut, jsonErr)
		}

		textStatus, textOut, _ := run("-r -cm test q hi")
		jsonStatus, jsonOut, jsonErr = run("-json -r -cm test q hi")
		if textStatus != 0 || jsonStatus != 0 {
			t.Fatalf("statuses text %d json %d, want both 0; json stderr %q", textStatus, jsonStatus, jsonErr)
		}
		var reply string
		for _, rec := range records(t, jsonOut) {
			if rec["type"] == "reply" {
				reply, _ = rec["text"].(string)
			}
		}
		if reply != strings.TrimSuffix(textOut, "\n") {
			t.Fatalf("json reply %q, want the text reply %q", reply, textOut)
		}
	})
}
