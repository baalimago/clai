// Package debugflags implements the uniform clai debug-flag scheme.
//
// Every feature-scoped debug switch follows the same rules: plain DEBUG=1
// enables all subsystems, DEBUG_<SUBSYSTEM>=1 enables a single subsystem.
package debugflags

import (
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Enabled reports whether verbose diagnostics are active for a subsystem. It
// returns true when DEBUG is truthy, or when DEBUG_<SUBSYSTEM> is truthy
// (case-insensitive).
func Enabled(subsystem string) bool {
	if misc.Truthy(os.Getenv("DEBUG")) {
		return true
	}
	return misc.Truthy(os.Getenv("DEBUG_" + strings.ToUpper(subsystem)))
}

// EnabledEnv applies the global DEBUG switch and a caller-provided
// compatibility environment variable, such as OPENAI_DEBUG.
func EnabledEnv(env string) bool {
	return misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv(env))
}

// OutputFile returns the optional path used for debug output.
func OutputFile() string {
	return os.Getenv("DEBUG_OUTPUT_FILE")
}
