// Package version prints clai's build version and dependency versions.
package version

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

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

// Print writes the build version (ldflags-stamped when present, module
// version otherwise) and every dependency version to the run's stdout.
func Print(ctx context.Context) error {
	out := runenv.From(ctx).Stdout()
	hasPrintedVersion := false
	if BuildVersion != "" {
		hasPrintedVersion = true
		fmt.Fprintln(out, "version: "+BuildVersion)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("failed to read build info")
	}
	if !hasPrintedVersion {
		fmt.Fprintln(out, "version: "+bi.Main.Version)
	}
	for _, dep := range bi.Deps {
		fmt.Fprintf(out, "%s %s\n", dep.Path, dep.Version)
	}
	return nil
}
