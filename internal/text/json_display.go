package text

import (
	"context"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// jsonDisplay is the query path's JSON Displayer pair: the reply records go
// to stdout and the transcript records to the destination the text form
// prints the transcript on (F1, F11, F14). Both are nil under the text
// format, where the querier draws as before. A failed write of the reply,
// its deltas included, fails the run in both formats; a failed write of a
// transcript record is ignored where the text form ignores the write it
// replaces (F6, F11).
type jsonDisplay struct {
	stdout display.Displayer
	stderr display.Displayer
}

func newJSONDisplay(ctx context.Context) jsonDisplay {
	env := runenv.From(ctx)
	if !env.JSON() {
		return jsonDisplay{}
	}
	stdout, outErr := env.Displayer(stdio.Stdout)
	stderr, errErr := env.Displayer(stdio.Stderr)
	if outErr != nil || errErr != nil {
		return jsonDisplay{}
	}
	return jsonDisplay{stdout: stdout, stderr: stderr}
}

func (j jsonDisplay) active() bool { return j.stdout != nil }

// jsonTranscript is the Displayer for what transcriptOut would print.
func (q *Querier[C]) jsonTranscript() display.Displayer {
	if q.transcriptToStderr {
		return q.json.stderr
	}
	return q.json.stdout
}

// closeStepReply writes the reply record that closes the reply_delta records
// of a step that goes on to call tools, on the destination those deltas
// went to, so no delta sequence is left open before a tool_call record.
func (q *Querier[C]) closeStepReply(text string) error {
	if !q.json.active() || q.debug || q.structuredOutput {
		return nil
	}
	text = stripThinkingBlocks(text)
	if text == "" {
		return nil
	}
	displayer := q.json.stdout
	if q.holdsStepText() {
		displayer = q.jsonTranscript()
	}
	if err := displayer.Reply(text); err != nil {
		return diag.Errorf("write the reply record before a tool call: %w", err)
	}
	return nil
}

func jsonToolCall(call pub_models.Call) display.ToolCall {
	var inputs map[string]any
	if call.Inputs != nil {
		inputs = *call.Inputs
	}
	return display.ToolCall{Name: call.Name, Inputs: inputs}
}
