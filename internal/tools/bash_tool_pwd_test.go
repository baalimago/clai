package tools

import (
	"os"
	"strings"
	"testing"
)

func TestPwd_ReturnsCurrentWorkingDirectory(t *testing.T) {
	// Arrange: get the Go process' current working directory
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd failed: %v", err)
	}

	// Act: call the tool
	out, err := Pwd.Call(map[string]any{})
	if err != nil {
		t.Fatalf("Pwd.Call returned error: %v", err)
	}

	// pwd usually prints a trailing newline; trim whitespace
	got := strings.TrimSpace(out)

	// Assert
	if got != want {
		t.Fatalf("pwd output mismatch.\nwant: %q\ngot:  %q", want, got)
	}
}
