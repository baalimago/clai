package cli

import (
	"log"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// TestStandardLogIsARunDiagnostic pins that a line written through Go's
// standard logger during a run, as net/http writes its own, is a diagnostic
// of the run in both formats and never a raw line, and that the logger is
// restored when the run ends.
func TestStandardLogIsARunDiagnostic(t *testing.T) {
	const line = "Unsolicited response received on idle HTTP channel starting with \"HTTP/1.1 401\""
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	for format, want := range map[string]string{
		"-r":    "warn [clai] " + line,
		"-json": `"level":"warn","message":"Unsolicited response received on idle HTTP channel starting with \"HTTP/1.1 401\""`,
	} {
		t.Run(format, func(t *testing.T) {
			useConfigDir(t)
			scriptedProvider(t, "logs", func(w http.ResponseWriter, _ *http.Request, _ int32) {
				log.Print(line)
				slog.Warn("from the default slog logger")
				sse(w, chatDelta("hi"), chatFinishStop, chatDone)
			})
			got := runProcess(t, format+" -cm gpt-logs q hi", nil)
			if got.status != 0 || strings.Count(got.stderr, want) != 1 || !strings.Contains(got.stderr, "from the default slog logger") {
				t.Fatalf("got %+v, want the logged lines once as diagnostics", got)
			}
			if format == "-json" {
				recordTypes(t, got.stderr)
			} else if strings.Count(got.stderr, "\n") != 2 {
				t.Fatalf("stderr %q, want exactly the two diagnostics", got.stderr)
			}
			if log.Writer() != writer || log.Flags() != flags || log.Prefix() != prefix {
				t.Fatal("the standard logger is not restored after the run")
			}
		})
	}
}
