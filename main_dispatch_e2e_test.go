package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_e2e_flags_after_command proves the cmd.Run dispatch parses flags
// placed after the command identically to flags placed before it.
func Test_e2e_flags_after_command(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeNotificationTestPriceFiles(t, confDir)

	for _, args := range []string{
		"-r -cm test q hello there",
		"q -r -cm test hello there",
	} {
		t.Run(args, func(t *testing.T) {
			stdout, status := runOne(t, confDir, args)
			testboil.FailTestIfDiff(t, status, 0)
			testboil.FailTestIfDiff(t, stdout, "hello there\n")
		})
	}
}

// Test_e2e_dash_leading_prompt_escape pins the '--' escape for prompts whose
// first word starts with '-': the parser reads those as flags (accepted
// regression R-b), so the escape is the documented way through and must be
// discoverable from both the usage and the query help.
func Test_e2e_dash_leading_prompt_escape(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeNotificationTestPriceFiles(t, confDir)

	t.Run("-- passes a dash-leading prompt through", func(t *testing.T) {
		stdout, status := runOne(t, confDir, "-r -cm test q -- -what is this")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.FailTestIfDiff(t, stdout, "-what is this\n")
	})

	t.Run("usage documents the escape", func(t *testing.T) {
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			run(nil)
		})
		testboil.AssertStringContains(t, stdout, "clai q -- -")
	})

	t.Run("query help documents the escape", func(t *testing.T) {
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			run([]string{"q", "-h"})
		})
		testboil.AssertStringContains(t, stdout, "clai q -- -")
	})
}

// Test_e2e_other_command_flag_hints_owner pins the hint for a flag owned by
// a different top-level command: without it the error blames the flag's
// value ("'/tmp' is not a valid argument"), which reads as nonsense.
func Test_e2e_other_command_flag_hints_owner(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	var status int
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		_ = testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split("q -pd /tmp hello", " "))
		})
	})

	testboil.FailTestIfDiff(t, status, 1)
	for _, want := range []string{"-pd", "photo"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("expected %q in the error, got: %q", want, stderr)
		}
	}
}

func Test_e2e_unknown_command_errors_with_usage(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = run([]string{"bogus"})
		})
	})

	testboil.FailTestIfDiff(t, status, 1)
	if !strings.Contains(stderr, "bogus") {
		t.Fatalf("expected error naming the unknown command, got stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "query|q") {
		t.Fatalf("expected usage with command table, got stdout: %q", stdout)
	}
}

// Test_e2e_glob_command_removed pins that the deprecated glob command (and
// its g alias) is gone: both are unknown commands now. Globbing lives on
// the -g/-glob flag only.
func Test_e2e_glob_command_removed(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	for _, name := range []string{"glob", "g"} {
		t.Run(name, func(t *testing.T) {
			var status int
			var stdout string
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				stdout = testboil.CaptureStdout(t, func(t *testing.T) {
					status = run([]string{name, "*.go", "hi"})
				})
			})

			testboil.FailTestIfDiff(t, status, 1)
			if !strings.Contains(stderr, name) {
				t.Fatalf("expected error naming %q, got stderr: %q", name, stderr)
			}
			if !strings.Contains(stdout, "query|q") {
				t.Fatalf("expected usage with command table, got stdout: %q", stdout)
			}
		})
	}
}

// Test_e2e_hidden_completion_is_side_effect_free pins that the __complete
// path never runs config migrations, keeping shell completion fast and
// write-free.
func Test_e2e_hidden_completion_is_side_effect_free(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "__complete clai q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
	if err != nil {
		t.Fatalf("ReadFile(textConfig.json): %v", err)
	}
	if strings.Contains(string(regenerated), `"stoploss"`) {
		t.Fatalf("__complete must not migrate configs:\n%s", regenerated)
	}
}

// Test_e2e_flag_scoping pins the per-command flag surfaces: a command
// accepts its own flags (before or after the command token), rejects flags
// it doesn't own, and both alias forms share one value (last one wins).
func Test_e2e_flag_scoping(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	writeNotificationTestPriceFiles(t, confDir)

	t.Run("both aliases, last one wins", func(t *testing.T) {
		stdout, status := runOne(t, confDir, "-r -cm bogus_model -chat-model test q hello")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.FailTestIfDiff(t, stdout, "hello\n")
	})

	for _, tc := range []struct {
		name    string
		args    string
		wantErr string
	}{
		{"unowned flag rejected", "photo -cm x hi", "flag provided but not defined: -cm"},
		{"unowned flag pre-command rejected", "-pm x q hi", "flag provided but not defined: -pm"},
		{"invalid int value names the flag", "q -mt abc hi", "invalid value \"abc\" for flag -mt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var status int
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				_ = testboil.CaptureStdout(t, func(t *testing.T) {
					status = run(strings.Split(tc.args, " "))
				})
			})
			testboil.FailTestIfDiff(t, status, 1)
			if !strings.Contains(stderr, tc.wantErr) {
				t.Fatalf("expected stderr containing %q, got: %q", tc.wantErr, stderr)
			}
			if strings.Contains(stderr, "Usage of") {
				t.Fatalf("stdlib usage dump must not print, got: %q", stderr)
			}
		})
	}

	t.Run("chat help lists chat-scoped flags only, no stdlib dump", func(t *testing.T) {
		var status int
		var stdout string
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			stdout = testboil.CaptureStdout(t, func(t *testing.T) {
				status = run([]string{"chat", "-h"})
			})
		})
		testboil.FailTestIfDiff(t, status, 0)
		for _, want := range []string{"-r", "-n", "-p"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("expected chat help to list %q, got: %q", want, stdout)
			}
		}
		// The chat tree runs no model, so the agent group's flags would be
		// inert here — listing them only advertises no-ops.
		for _, forbidden := range []string{"-cm", "-lb", "-cmd-ban", "-am", "-af", "-reply", "-skills", "-asc", "-response-format", "Usage of"} {
			if strings.Contains(stdout+stderr, forbidden) {
				t.Fatalf("chat help must not contain %q, got: %q", forbidden, stdout+stderr)
			}
		}
	})

	t.Run("chat sub lists and parses its own flags at its level", func(t *testing.T) {
		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run([]string{"chat", "list", "-h"})
		})
		testboil.FailTestIfDiff(t, status, 0)
		for _, want := range []string{"-r", "-n"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("expected list help to list %q, got: %q", want, stdout)
			}
		}
		if strings.Contains(stdout, "-cm") {
			t.Fatalf("list help must not list -cm, got: %q", stdout)
		}

		listStdout, listStatus := runOne(t, confDir, "-n chat list -r q")
		if listStatus != 0 {
			t.Fatalf("sub-level -r should parse, got status %d, stdout=%q", listStatus, listStdout)
		}
	})

	t.Run("per-command help lists own flags only", func(t *testing.T) {
		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run([]string{"photo", "-h"})
		})
		testboil.FailTestIfDiff(t, status, 0)
		for _, want := range []string{"-pm", "-photo-dir", "-re", "-raw"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("expected photo help to list %q, got: %q", want, stdout)
			}
		}
		for _, forbidden := range []string{"-cm", "-chat-model", "-vm", "-am"} {
			if strings.Contains(stdout, forbidden) {
				t.Fatalf("photo help must not list %q, got: %q", forbidden, stdout)
			}
		}
	})
}

// Test_e2e_usage_examples_parse proves every flag/command shape shown in
// the usage examples still parses under the scoped flag system: none may
// die with a flag-definition or unknown-command error (later failures such
// as missing API keys are fine — the examples use real models).
func Test_e2e_usage_examples_parse(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	writeNotificationTestPriceFiles(t, confDir)

	for _, args := range []string{
		"-r -cm test q hi",
		"confdir",
		"-t website_text -cm test -r query hi",
		"-glob *.go -cm test -r query hi",
		"-asc minimal -cm test -r q hi",
		"-pm test photo A cat in space",
		"-I LOG -cm test -r q find errors in LOG",
		"a t meeting.wav",
		"-n c l q",
		"-r c dirv2",
		"c help",
	} {
		t.Run(args, func(t *testing.T) {
			var stdout string
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				stdout, _ = runOne(t, confDir, args)
			})
			combined := stdout + stderr
			for _, fatal := range []string{"flag provided but not defined", "is not a valid argument"} {
				if strings.Contains(combined, fatal) {
					t.Fatalf("example no longer parses (%q), got: %q", fatal, combined)
				}
			}
		})
	}
}

// Test_e2e_command_help proves -h on any command prints that command's
// Help() and exits 0.
func Test_e2e_command_help(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	for _, tc := range []struct {
		args     string
		wantHelp string
	}{
		{"q -h", "query <text>"},
		{"chat -h", "clai chat help"},
		{"audio -h", "t|transcribe <file>"},
		{"photo -h", "photo <text>"},
		{"video -h", "video <text>"},
		{"setup -h", "configuration wizard"},
		{"version -h", "dependency versions"},
		{"replay -h", "previous reply"},
		{"dre -h", "conversation bound to the"},
		{"tools -h", "mcp and built-in tools"},
		{"profiles -h", "profiles under"},
		{"confdir -h", "registered subpath"},
		{"completion -h", "source <(clai completion bash)"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			var status int
			stdout := testboil.CaptureStdout(t, func(t *testing.T) {
				status = run(strings.Split(tc.args, " "))
			})
			testboil.FailTestIfDiff(t, status, 0)
			if !strings.Contains(stdout, tc.wantHelp) {
				t.Fatalf("expected help containing %q, got: %q", tc.wantHelp, stdout)
			}
		})
	}
}
