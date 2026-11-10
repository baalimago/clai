package capture

import (
	"bufio"
	"go/build/constraint"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestGroupFileByPlatform pins which stop implementation each platform
// builds. The wasix port answers to the linux tag as well as its own, as
// android does, and its ffmpeg is a wrapper script whose child cannot take a
// signal: a signal to the wrapper's group ends that child before it wrote the
// file and left the wrapper waiting on it forever (measured 2026-11-10).
func TestGroupFileByPlatform(t *testing.T) {
	rows := []struct {
		platform string
		tags     []string
		want     string
	}{
		{"linux", []string{"linux", "unix", "amd64"}, "group_unix.go"},
		{"darwin", []string{"darwin", "unix", "arm64"}, "group_unix.go"},
		{"wasix", []string{"wasix", "linux", "wasm"}, "group_other.go"},
		{"windows", []string{"windows", "amd64"}, "group_other.go"},
	}
	for _, row := range rows {
		t.Run(row.platform, func(t *testing.T) {
			var built []string
			for _, file := range []string{"group_unix.go", "group_other.go"} {
				expr := buildConstraint(t, file)
				if expr.Eval(func(tag string) bool { return slices.Contains(row.tags, tag) }) {
					built = append(built, file)
				}
			}
			if !slices.Equal(built, []string{row.want}) {
				t.Fatalf("%s builds %v, want only %s", row.platform, built, row.want)
			}
		})
	}
}

func buildConstraint(t *testing.T, file string) constraint.Expr {
	t.Helper()
	source, err := os.Open(file)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer source.Close()
	scanner := bufio.NewScanner(source)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			t.Fatalf("parse the build constraint of %s: %v", file, err)
		}
		return expr
	}
	t.Fatalf("%s has no //go:build line", file)
	return nil
}
