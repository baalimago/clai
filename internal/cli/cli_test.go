package cli

import (
	"os"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestDefaultDeps_setsBothFactories(t *testing.T) {
	deps := DefaultDeps()
	if deps.NewSummarizer == nil {
		t.Fatal("NewSummarizer is nil, want the real summarizer constructor")
	}
	if deps.ForeignCache == nil {
		t.Fatal("ForeignCache is nil, want the real foreign-cache factory")
	}
}

func TestCommands_buildsEveryDocumentedKey(t *testing.T) {
	want := []string{
		"query|q", "chat|c", "photo|p", "video|v", "audio|a", "setup|s",
		"version", "replay|re", "dir-replay|dre", "tools|t", "mcp",
		"profiles", "confdir",
	}
	got := Commands(DefaultDeps())
	if len(got) != len(want) {
		t.Fatalf("Commands() has %d entries, want %d", len(got), len(want))
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("Commands() misses %q", key)
		}
	}
}

func TestStartCPUProfile_reportsUnusablePath(t *testing.T) {
	dir := t.TempDir()
	if _, err := startCPUProfile(dir); err == nil {
		t.Fatalf("startCPUProfile(%q) = nil, want an error naming the directory", dir)
	}
}

func TestStartCPUProfile_writesProfileBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	stop, err := startCPUProfile("unit.prof")
	if err != nil {
		t.Fatalf("startCPUProfile: %v", err)
	}
	stop()
	info, err := os.Stat("unit.prof")
	if err != nil {
		t.Fatalf("Stat(unit.prof): %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("profile file is empty, want the encoded profile")
	}
}

// isolateEnv keeps a dispatch off the developer's real config and cache dirs.
func isolateEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
}

func TestRun_returnsStatusForKnownAndUnknownCommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{name: "version", args: []string{"version"}, want: 0},
		{name: "unknown command", args: []string{"bogus"}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			var status int
			_ = testboil.CaptureStderr(t, func(t *testing.T) {
				_ = testboil.CaptureStdout(t, func(t *testing.T) {
					status = Run(tc.args, DefaultDeps())
				})
			})
			if status != tc.want {
				t.Fatalf("Run(%v) = %d, want %d", tc.args, status, tc.want)
			}
		})
	}
}

// TestRun_versionFlagMatchesVersionCommand pins that the special
// first-argument version flag is dispatched through the same command: both
// spellings produce the same status and stdout.
func TestRun_versionFlagMatchesVersionCommand(t *testing.T) {
	isolateEnv(t)

	flagStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if status := Run([]string{"--version"}, DefaultDeps()); status != 0 {
			t.Fatalf("Run(--version) = %d, want 0", status)
		}
	})
	commandStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if status := Run([]string{"version"}, DefaultDeps()); status != 0 {
			t.Fatalf("Run(version) = %d, want 0", status)
		}
	})

	if flagStdout == "" {
		t.Fatal("Run(--version) printed nothing, want the version output")
	}
	if flagStdout != commandStdout {
		t.Fatalf("Run(--version) = %q, want the version command's %q", flagStdout, commandStdout)
	}
}

// TestRun_versionFlagSpellings pins the accepted spellings: Go flag syntax
// treats one and two dashes alike, so both must work.
func TestRun_versionFlagSpellings(t *testing.T) {
	isolateEnv(t)
	for _, arg := range []string{"--version", "-version"} {
		t.Run(arg, func(t *testing.T) {
			var status int
			stdout := testboil.CaptureStdout(t, func(t *testing.T) {
				status = Run([]string{arg}, DefaultDeps())
			})
			if status != 0 {
				t.Fatalf("Run(%q) = %d, want 0", arg, status)
			}
			if stdout == "" {
				t.Fatalf("Run(%q) printed nothing, want the version output", arg)
			}
		})
	}
}

func TestRunProfiled_withCPUDebugWritesProfile(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DEBUG", "")
	t.Setenv("DEBUG_CPU", "1")
	t.Chdir(t.TempDir())

	var status int
	_ = testboil.CaptureStdout(t, func(t *testing.T) {
		status = RunProfiled([]string{"version"}, DefaultDeps())
	})
	if status != 0 {
		t.Fatalf("RunProfiled(version) = %d, want 0", status)
	}
	info, err := os.Stat(CPUProfileFileName)
	if err != nil {
		t.Fatalf("Stat(%q): %v, want a written profile", CPUProfileFileName, err)
	}
	if info.Size() == 0 {
		t.Fatalf("%q is empty, want the encoded profile", CPUProfileFileName)
	}
}

func TestRunProfiled_withoutCPUDebugWritesNoProfile(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DEBUG", "")
	t.Setenv("DEBUG_CPU", "")
	t.Chdir(t.TempDir())

	var status int
	_ = testboil.CaptureStdout(t, func(t *testing.T) {
		status = RunProfiled([]string{"version"}, DefaultDeps())
	})
	if status != 0 {
		t.Fatalf("RunProfiled(version) = %d, want 0", status)
	}
	if _, err := os.Stat(CPUProfileFileName); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) = %v, want no profile without the CPU debug flag", CPUProfileFileName, err)
	}
}
