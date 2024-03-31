package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Prompt returns the prompt by checking all the arguments and stdin.
// If there is no arguments, but data in stdin, stdin will become the prompt.
// If there are arguments and data in stdin, all stdinReplace tokens will be substituted
// with the data in stdin
func Prompt(stdinReplace string, args []string) (string, error) {
	debug := misc.Truthy(os.Getenv("DEBUG"))
	if debug {
		ancli.PrintOK(fmt.Sprintf("stdinReplace: %v\n", stdinReplace))
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		panic(err)
	}
	var hasPipe bool
	if fi.Mode()&os.ModeNamedPipe == 0 {
		hasPipe = false
	} else {
		hasPipe = true
	}

	if len(args) == 1 && !hasPipe {
		return "", errors.New("found no prompt, set args or pipe in some string")
	}
	// First argument is the command, so we skip it
	args = args[1:]
	// If no data is in stdin, simply return args
	if !hasPipe {
		return strings.Join(args, " "), nil
	}

	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read stdin: %v", err)
	}
	pipeIn := string(inputData)
	if len(args) == 1 {
		args = append(args, strings.Split(pipeIn, " ")...)
	}

	// Replace all occurrence of stdinReplaceSignal with pipeIn
	if stdinReplace != "" {
		if debug {
			ancli.PrintOK(fmt.Sprintf("attempting to replace: '%v' with stdin\n", stdinReplace))
		}
		for i, arg := range args {
			if strings.Contains(arg, stdinReplace) {
				args[i] = strings.ReplaceAll(arg, stdinReplace, pipeIn)
			}
		}
	}

	if debug {
		ancli.PrintOK(fmt.Sprintf("args: %v\n", args))
	}
	return strings.Join(args, " "), nil
}
