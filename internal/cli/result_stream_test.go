package cli

import (
	"strings"
	"testing"
)

// TestDiagnosticLevelLeavesTheResultStream pins that raising the diagnostic
// level, by LOG_LEVEL or a legacy control, changes only what reaches
// stderr: stdout carries the same reply, streamed as reply_delta records
// under -json, as at the default level (F1, F9, F11).
func TestDiagnosticLevelLeavesTheResultStream(t *testing.T) {
	controls := []struct{ name, value string }{
		{"LOG_LEVEL", "debug"},
		{"LOG_LEVEL", "trace"},
		{"LOG_LEVEL", "info,text_querier=debug"},
		{"DEBUG", "1"},
		{"DEBUG_TEXT_QUERIER", "1"},
	}
	for _, format := range []string{"", "-json "} {
		useConfigDir(t)
		args := format + "-cm test q hello world"
		want := runCLI(t, args, cliOpts{})
		if want.status != 0 {
			t.Fatalf("%q at the default level: status %d stderr %q", args, want.status, want.stderr)
		}
		if format != "" && !strings.Contains(want.stdout, `{"type":"reply_delta","text":"hello world"}`) {
			t.Fatalf("%q at the default level streams no reply_delta: %q", args, want.stdout)
		}
		for _, control := range controls {
			t.Run(format+control.name+"="+control.value, func(t *testing.T) {
				t.Setenv(control.name, control.value)
				got := runCLI(t, args, cliOpts{})
				if got.status != want.status || got.stdout != want.stdout {
					t.Fatalf("%s=%s changed the result: status %d stdout %q, want status %d stdout %q", control.name, control.value, got.status, got.stdout, want.status, want.stdout)
				}
			})
		}
	}
}
