// Package version prints clai's build version and dependency versions.
package version

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/runenv"
)

// Set with buildflag if built in pipeline and not using go install
var (
	BuildVersion  = ""
	BuildChecksum = ""
)

// Command builds the version command.
func Command() *internal.Command {
	c := &internal.Command{
		Name: "version",
		Desc: "Print the version of clai and its dependencies",
		HelpText: `version. Prints build version, checksum and dependency versions.

Examples:
  clai version`,
	}
	c.OnRun = func(ctx context.Context, _ *internal.Command) error {
		return Print(ctx)
	}
	return c
}

// Resolve is the build version: ldflags-stamped when present, the module
// version otherwise.
func Resolve() (string, error) {
	if BuildVersion != "" {
		return BuildVersion, nil
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("failed to read build info")
	}
	return bi.Main.Version, nil
}

// IsRelease reports whether v is a tagged release, vMAJOR.MINOR.PATCH, as
// opposed to a pseudo-version, a prerelease, a dirty or a devel build.
func IsRelease(v string) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" || len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

// Print writes the build version (ldflags-stamped when present, module
// version otherwise) and every dependency version to the run's stdout.
func Print(ctx context.Context) error {
	out := runenv.From(ctx).Stdout()
	v, err := Resolve()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "version: "+v)
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("failed to read build info")
	}
	for _, dep := range bi.Deps {
		fmt.Fprintf(out, "%s %s\n", dep.Path, dep.Version)
	}
	return nil
}
