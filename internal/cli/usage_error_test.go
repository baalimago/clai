package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// errorRecord is the first error record of a JSON run's stderr.
func errorRecord(t *testing.T, stderr string) (message string, remedies []string) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSuffix(stderr, "\n"), "\n") {
		var rec struct {
			Type    string
			Message string
			Causes  []struct {
				Message  string
				Remedies []string
			}
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stderr line %q is not a record: %v", line, err)
		}
		if rec.Type != "error" {
			continue
		}
		for _, cause := range rec.Causes {
			remedies = append(remedies, cause.Remedies...)
		}
		return rec.Message, remedies
	}
	t.Fatalf("no error record on stderr: %q", stderr)
	return "", nil
}

// TestUsageErrorHints pins the shape of a usage error (F14, ReportError.1,
// ReportError.3): its message carries no trailing newline and no hint; a
// misplaced flag's hint is a remedy, "hint:" in text and remedies under
// -json; a misplaced global -json says where the global flag goes.
func TestUsageErrorHints(t *testing.T) {
	useConfigDir(t)
	const cmHint = "'-cm' belongs to 'query' — place it there, after that command."

	text := runCLI(t, "chat list -cm x", cliOpts{})
	wantText := "error: failed to parse flagset: flag provided but not defined: -cm\n  hint: " + cmHint + "\n"
	if text.status != 1 || text.stderr != wantText {
		t.Fatalf("text: status %d stderr %q, want 1 and %q", text.status, text.stderr, wantText)
	}

	asJSON := runCLI(t, "-json chat list -cm x", cliOpts{})
	message, remedies := errorRecord(t, asJSON.stderr)
	if asJSON.status != 1 || message != "failed to parse flagset: flag provided but not defined: -cm" || len(remedies) != 1 || remedies[0] != cmHint {
		t.Fatalf("json: status %d message %q remedies %q", asJSON.status, message, remedies)
	}

	for _, args := range []string{"chat list -json", "-json chat list -json"} {
		misplaced := runCLI(t, args, cliOpts{})
		var hint string
		if strings.HasPrefix(args, "-json") {
			_, remedies := errorRecord(t, misplaced.stderr)
			hint = strings.Join(remedies, "\n")
		} else {
			_, hint, _ = strings.Cut(misplaced.stderr, "hint: ")
		}
		if misplaced.status != 1 || !strings.Contains(hint, "before the command") || !strings.Contains(hint, "before its first argument") || strings.Contains(hint, "profiles validate") {
			t.Fatalf("%q: status %d, hint %q; want where the global -json goes", args, misplaced.status, hint)
		}
	}

	unknown := runCLI(t, "-json nosuchcmd", cliOpts{})
	if message, _ := errorRecord(t, unknown.stderr); message != "'nosuchcmd' is not a valid argument" {
		t.Fatalf("unknown argument message = %q", message)
	}
}

// TestMissingAPIKeyRemedy pins the remedy of a vendor whose API key
// variable is unset: it names the variable, never a value, in text and
// under -json, for each way a vendor checks its key.
func TestMissingAPIKeyRemedy(t *testing.T) {
	useConfigDir(t)
	for _, tc := range []struct{ model, env string }{
		{"gpt-5", "OPENAI_API_KEY"},
		{"claude-sonnet-4", "ANTHROPIC_API_KEY"},
		{"mistral-large", "MISTRAL_API_KEY"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Setenv(tc.env, "")
			asJSON := runCLI(t, "-json -cm "+tc.model+" q hi", cliOpts{})
			message, remedies := errorRecord(t, asJSON.stderr)
			if asJSON.status != 1 || !strings.Contains(message, tc.env) || len(remedies) != 1 || !strings.Contains(remedies[0], "Set "+tc.env) {
				t.Fatalf("json: status %d message %q remedies %q; want a remedy naming %s", asJSON.status, message, remedies, tc.env)
			}
			text := runCLI(t, "-cm "+tc.model+" q hi", cliOpts{})
			if text.status != 1 || !strings.Contains(text.stderr, "\n  hint: Set "+tc.env) {
				t.Fatalf("text: status %d stderr %q; want a hint naming %s", text.status, text.stderr, tc.env)
			}
		})
	}
}

// TestJSONFlagValues pins the -json value spellings: a boolean value as Go's
// bool flags read it selects the format (-json=false is text), in both
// places the synopsis allows; any other value is a usage error reported in
// text, with the usage, exit status 1.
func TestJSONFlagValues(t *testing.T) {
	useConfigDir(t)
	text := runCLI(t, "-cm test q hello", cliOpts{})
	asJSON := runCLI(t, "-json -cm test q hello", cliOpts{})
	for _, args := range []string{"-json=false -cm test q hello", "--json=false -cm test q hello", "q -json=false -cm test hello", "-json=0 -cm test q hello"} {
		if got := runCLI(t, args, cliOpts{}); got.status != 0 || got.stdout != text.stdout {
			t.Fatalf("%q: status %d stdout %q, want the text run's %q", args, got.status, got.stdout, text.stdout)
		}
	}
	for _, args := range []string{"-json=true -cm test q hello", "--json=true -cm test q hello", "q --json=1 -cm test hello"} {
		if got := runCLI(t, args, cliOpts{}); got.status != 0 || got.stdout != asJSON.stdout {
			t.Fatalf("%q: status %d stdout %q, want the JSON run's %q", args, got.status, got.stdout, asJSON.stdout)
		}
	}
	for _, args := range []string{"-json=maybe -cm test q hello", "q --json= hello"} {
		got := runCLI(t, args, cliOpts{})
		if got.status != 1 || !strings.HasPrefix(got.stderr, "error: invalid boolean value ") || !strings.Contains(got.stderr, "for -json") || !strings.Contains(got.stdout, "Usage: clai") {
			t.Fatalf("%q: status %d stdout %q stderr %q, want a usage error naming -json and the usage", args, got.status, got.stdout, got.stderr)
		}
	}
}
