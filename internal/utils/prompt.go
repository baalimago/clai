package utils

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
)

var ErrNoPrompt = errors.New("found no prompt, set args or pipe in some string")

// Prompt returns the prompt by checking all the arguments and stdin.
// If there is no arguments, but data in stdin, stdin will become the prompt.
// If there are arguments and data in stdin, all stdinReplace tokens will be substituted
// with the data in stdin
func Prompt(ctx context.Context, stdinReplace string, args []string) (string, error) {
	stdin, err := PipedStdin(ctx)
	if err != nil {
		return "", err
	}
	return PromptFrom(stdinReplace, args, stdin)
}

// PipedStdin returns the run's stdin when it is not a terminal (a pipe, a
// file redirect), and nil when it is one. A pipe's reads give up with the
// cause of ctx once it ends, so an open pipe nobody writes to cannot outlast
// the run.
func PipedStdin(ctx context.Context) (io.Reader, error) {
	env := runenv.From(ctx)
	if env.StdinIsTerminal() {
		return nil, nil
	}
	in := env.Stdin()
	if f, ok := in.(interface{ Stat() (os.FileInfo, error) }); ok {
		fi, err := f.Stat()
		if err != nil {
			return nil, diag.Errorf("stat stdin: %w", err)
		}
		if fi.Mode()&os.ModeCharDevice != 0 {
			return nil, nil
		}
		if fi.Mode().IsRegular() {
			return in, nil
		}
	}
	return contextReader{ctx: ctx, r: in}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

type readResult struct {
	n   int
	err error
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := context.Cause(c.ctx); err != nil {
		return 0, diag.Errorf("stopped waiting for stdin: %w", err)
	}
	// The read runs on its own buffer: an abandoned one must not write
	// into p after Read returned.
	buf := make([]byte, len(p))
	res := make(chan readResult, 1)
	go func() {
		n, err := c.r.Read(buf)
		res <- readResult{n: n, err: err}
	}()
	select {
	case <-c.ctx.Done():
		return 0, diag.Errorf("stopped waiting for stdin: %w", context.Cause(c.ctx))
	case r := <-res:
		return copy(p, buf[:r.n]), r.err
	}
}

// PromptFrom is Prompt with stdin given explicitly; nil means stdin is a terminal.
func PromptFrom(stdinReplace string, args []string, stdin io.Reader) (string, error) {
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
		return "", diag.Errorf("failed to read stdin: %w", err)
	}
	pipeIn := string(inputData)
	// Stdin alone is the prompt.
	if len(args) == 0 {
		return pipeIn, nil
	}
	if stdinReplace == "" {
		stdinReplace = "{}"
	}
	// Stdin replaces every token in the prompt, or is appended once to a
	// prompt without one; it is never sent twice nor dropped.
	replaced := false
	for i, arg := range args {
		if strings.Contains(arg, stdinReplace) {
			args[i] = strings.ReplaceAll(arg, stdinReplace, pipeIn)
			replaced = true
		}
	}
	if !replaced {
		args = append(args, pipeIn)
	}

	return strings.Join(args, " "), nil
}

func ReplaceTildeWithHome(s string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil && strings.Contains(s, "~/") { // only fail if glob contains ~/ and home dir is not found
		return "", diag.Errorf("failed to get home dir: %w", err)
	}
	return strings.ReplaceAll(s, "~", home), nil
}
