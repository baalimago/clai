package testenv

import (
	"fmt"
	"os"
	"testing"
)

func TestQuiet_parksTheProcessStreamsOnTheNullDeviceUntilRestored(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr

	restore := Quiet(t)

	if os.Stdout.Name() != os.DevNull || os.Stderr.Name() != os.DevNull {
		t.Fatalf("streams = %q, %q, want both on %s", os.Stdout.Name(), os.Stderr.Name(), os.DevNull)
	}
	if _, err := fmt.Fprintln(os.Stdout, "swallowed"); err != nil {
		t.Fatalf("write to the quiet stdout: %v", err)
	}
	if _, err := fmt.Fprintln(os.Stderr, "swallowed"); err != nil {
		t.Fatalf("write to the quiet stderr: %v", err)
	}
	restore()
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("restore did not put the process streams back")
	}
	restore()
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("a second restore moved the process streams")
	}
}

func TestQuiet_restoresWhenTheTestEndsWithoutRestoring(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr
	t.Run("forgets to restore", func(t *testing.T) {
		_ = Quiet(t)
		if os.Stdout == stdout {
			t.Fatal("Quiet left stdout in place")
		}
	})
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("the subtest's cleanup did not restore the process streams")
	}
}
