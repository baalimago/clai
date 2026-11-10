package setup

import (
	"context"
	"fmt"
	"io"

	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

// termIn is the wizard's input: Input when a test or a macro set it, else
// the run's stdin.
func termIn(ctx context.Context) io.Reader {
	if Input != nil {
		return Input
	}
	return runenv.From(ctx).Stdin()
}

// selectPrompt is how a wizard table shows its selection prompt: under
// -json a select prompt record on stdout, else the table's own line.
func selectPrompt(ctx context.Context) func(string) error {
	return runenv.From(ctx).TablePrompter(stdio.Stdout)
}

// ask shows a question before its answer is read from termIn: under -json
// a prompt record of kind on stdout with the answers as choices, and the
// text form unchanged otherwise (F13, F14). Free-text entries are kind
// select without choices. A failed write is ignored, as the text form
// ignores it.
func ask(ctx context.Context, kind, question string, choices []string, textForm string) {
	env := runenv.From(ctx)
	if env.JSON() {
		_ = env.ShowPrompt(stdio.Stdout, display.Prompt{Kind: kind, Text: question, Choices: choices})
		return
	}
	fmt.Fprint(env.Stdout(), textForm)
}
