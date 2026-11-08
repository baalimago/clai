package remote

import (
	"fmt"
	"strings"
)

// MaxBodyBytes caps a remote profile. The hub refuses a larger body too.
const MaxBodyBytes = 64 * 1024

// InvalidProfileError lists every problem found in a remote profile.
type InvalidProfileError struct {
	Target   string
	Problems []string
}

func (e *InvalidProfileError) Error() string {
	return fmt.Sprintf("%s is not a valid profile:\n  %s", e.Target, strings.Join(e.Problems, "\n  "))
}

// ExitCode marks a refused remote profile apart from a failed run.
func (e *InvalidProfileError) ExitCode() int { return ExitRefused }

// Checked is what a profile check learned about a profile that passed.
type Checked struct {
	// Ignored are keys clai does not read and that look like no key it
	// does; they are left alone, as a local profile's would be.
	Ignored []string
}
