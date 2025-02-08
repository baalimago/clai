package text

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

var errFormat = "code: %v, stderr: '%v'\n"

func (q *Querier[C]) handleCmdMode() error {
	// Tokens stream end without endline
	fmt.Println()

	if q.execErr != nil {
		return nil
	}

	for {
		fmt.Print("Do you want to [e]xecute cmd, [q]uit?: ")
		input, err := utils.ReadUserInput()
		if err != nil {
			return err
		}
		switch strings.ToLower(input) {
		case "q":
			return nil
		case "e":
			err := q.executeLlmCmd()
			if err == nil {
				return nil
			} else {
				return fmt.Errorf("failed to execute cmd: %v", err)
			}
		default:
			ancli.PrintWarn(fmt.Sprintf("unrecognized command: %v, please try again\n", input))
		}
	}
}

func (q *Querier[C]) executeLlmCmd() error {
	fullMsg, err := utils.ReplaceTildeWithHome(q.fullMsg)
	if err != nil {
		return fmt.Errorf("parseGlob, ReplaceTildeWithHome: %w", err)
	}
	// Quotes are, in 99% of the time, expanded by the shell in
	// different ways and then passed into the shell. So when LLM
	// suggests a command, executeAiCmd needs to act the same (meaning)
	// remove/expand the quotes
	fullMsg = strings.ReplaceAll(fullMsg, "\"", "")
	split := strings.Split(fullMsg, " ")
	if len(split) < 1 {
		return errors.New("Querier.executeAiCmd: too few tokens in q.fullMsg")
	}
	cmd := split[0]
	args := split[1:]

	if len(cmd) == 0 {
		return errors.New("Querier.executeAiCmd: command is empty")
	}

	command := exec.Command(cmd, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err = command.Run()
	if err != nil {
		cast := &exec.ExitError{}
		if errors.As(err, &cast) {
			return fmt.Errorf(errFormat, cast.ExitCode())
		} else {
			return fmt.Errorf("Querier.executeAiCmd - run error: %w", err)
		}
	}

	return nil
}
