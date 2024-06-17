package text

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func (q *Querier[C]) handleCmdMode() error {
	// Tokens stream end without endline
	fmt.Println()
	var input string

	for {
		fmt.Print("Do you want to [e]xecute cmd, [q]uit?: ")
		fmt.Scanln(&input)
		switch strings.ToLower(input) {
		case "q":
			return nil
		case "e":
			return q.executeAiCmd()
		default:
			ancli.PrintWarn(fmt.Sprintf("unrecognized command: %v, please try again\n", input))
		}
	}
}

func (q *Querier[C]) executeAiCmd() error {
	fullMsg, err := utils.ReplaceTildeWithHome(q.fullMsg)
	if err != nil {
		return fmt.Errorf("parseGlob, ReplaceTildeWithHome: %w", err)
	}
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	outStr := stdout.String()
	errStr := stderr.String()

	if err != nil {
		cast := &exec.ExitError{}
		if errors.As(err, &cast) {
			ancli.PrintErr(fmt.Sprintf("code: %v, stderr: '%v', stdout: '%v'\n", cast.ExitCode(), errStr, outStr))
			return nil
		} else {
			return fmt.Errorf("Querier.executeAiCmd - run error: %w", err)
		}
	}

	ancli.PrintOK(fmt.Sprintf("stdout on new line:\n%v\n", outStr))
	return nil
}
