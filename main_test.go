package main

import (
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
	// Pre-implementation reality (today):
	// - `clai re` currently calls os.Exit(0) inside internal.Setup (will panic in tests)
	// - `clai dre` does not exist yet
	//
	// This test is therefore expected to FAIL until the implementation changes the
	// command execution flow to return status codes for `re`/`dre` instead of calling
	// os.Exit, and until `dre` is implemented.

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

	// 1) (/bar) query hello (using test model, with -r to avoid non-deterministic pretty output)
	out, status := runOne(t, bar, "-r -cm test q hello")
	testboil.FailTestIfDiff(t, status, 0)
	// config init may print "created directory ..." before the model output
	testboil.AssertStringContains(t, out, "hello\n")

	// 2) (/bar) global replay matches last message from global prevQuery
	// Expected (once implemented): status=0 and output contains "hello".
	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	// 3) (/baz) dir replay should error because no binding exists yet
	_, status = runOne(t, baz, "-r dre")
	if status == 0 {
		t.Fatalf("expected non-zero status for 'dre' without binding")
	}

	// 4) (/baz) query hello2
	out, status = runOne(t, baz, "-r -cm test q hello2")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello2\n")

	// 5) (/baz) dir replay matches baz latest
	out, status = runOne(t, baz, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello2")

	// 6) (/bar) dir replay matches bar latest
	out, status = runOne(t, bar, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	// 7) (/bar) global replay matches baz (global last query)
	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello2")
}
