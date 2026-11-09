package cli

import (
	"strings"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// globalJSONHint says where the global -json goes (phase 9 synopsis).
const globalJSONHint = "-json selects the output format of the whole run: place it before the command (clai -json <command> ...) or among the command's flags before its first argument (clai <command> -json ...)."

// misplacedFlagError is a dispatcher's misplaced-flag error shown with its
// hint as a remedy, "hint:" in text and remedies under -json, instead of
// inside its message. A misplaced global -json gets the global flag's hint.
type misplacedFlagError struct {
	misplaced cmd.MisplacedFlagError
}

func (e misplacedFlagError) Error() string { return e.misplaced.Reason() }

func (e misplacedFlagError) Unwrap() error { return e.misplaced }

func (e misplacedFlagError) Remedies() []diagnostic.Remedy {
	if isJSONFlag(e.misplaced.Flag) {
		return []diagnostic.Remedy{{Instruction: globalJSONHint}}
	}
	return []diagnostic.Remedy{{Instruction: e.misplaced.Hint()}}
}

// isJSONFlag reports whether written, a flag as the dispatcher reports it,
// is the global -json in any spelling.
func isJSONFlag(written string) bool {
	name, _, _ := strings.Cut(strings.TrimLeft(written, "-"), "=")
	return name == "json"
}

// withHintAsRemedy returns err, or, when it is a misplaced-flag error, the
// same error with its hint lifted out of the message into a remedy.
func withHintAsRemedy(err error) error {
	if misplaced, ok := err.(cmd.MisplacedFlagError); ok {
		return misplacedFlagError{misplaced: misplaced}
	}
	return err
}
