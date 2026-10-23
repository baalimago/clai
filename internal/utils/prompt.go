package utils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var ErrNoPrompt = errors.New("found no prompt, set args or pipe in some string")

// Prompt returns the prompt by checking all the arguments and stdin.
// If there is no arguments, but data in stdin, stdin will become the prompt.
// If there are arguments and data in stdin, all stdinReplace tokens will be substituted
// with the data in stdin
func Prompt(stdinReplace string, args []string) (string, error) {
	stdin, err := PipedStdin()
	if err != nil {
		return "", err
	}
	return PromptFrom(stdinReplace, args, stdin)
}

// PipedStdin returns os.Stdin when it is not a terminal (a pipe, a file
// redirect), and nil when it is one.
func PipedStdin() (io.Reader, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat stdin: %w", err)
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return nil, nil
	}
	return os.Stdin, nil
}

// PromptFrom is Prompt with stdin given explicitly; nil means stdin is a terminal.
func PromptFrom(stdinReplace string, args []string, stdin io.Reader) (string, error) {
	debug := misc.Truthy(os.Getenv("DEBUG"))
	if debug {
		ancli.PrintOK(fmt.Sprintf("stdinReplace: %v\n", stdinReplace))
	}
	hasStdin := stdin != nil

	if len(args) == 0 || (len(args) == 1 && !hasStdin) {
		return "", ErrNoPrompt
	}
	// First argument is the command, so we skip it
	args = args[1:]
	// If no data is in stdin, simply return args
	if !hasStdin {
		return strings.Join(args, " "), nil
	}

	inputData, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read stdin: %w", err)
	}
	pipeIn := string(inputData)
	// Add the pipeIn to the args if there are no args
	if len(args) == 0 {
		args = append(args, strings.Split(pipeIn, " ")...)
	} else if stdinReplace == "" {
		stdinReplace = "{}"
		args = append(args, "{}")
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

func ReplaceTildeWithHome(s string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil && strings.Contains(s, "~/") { // only fail if glob contains ~/ and home dir is not found
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}
	return strings.ReplaceAll(s, "~", home), nil
}
