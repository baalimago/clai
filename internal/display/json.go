package display

import (
	"log/slog"
	"strings"

	"github.com/baalimago/clai/internal/jsonl"
)

const toolErrorPrefix = "ERROR: "

type jsonDisplayer struct {
	enc jsonl.Encoder
}

func (d *jsonDisplayer) ReplyDelta(text string) error {
	return d.text("reply_delta", text)
}

func (d *jsonDisplayer) ReasoningDelta(text string) error {
	return d.text("reasoning_delta", text)
}

func (d *jsonDisplayer) Reply(text string) error {
	return d.text("reply", text)
}

func (d *jsonDisplayer) ReportToolCall(call ToolCall) error {
	inputs := call.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	return d.enc.Encode(jsonl.Record{Type: "tool_call", Fields: []slog.Attr{
		slog.String("name", call.Name),
		slog.Any("inputs", inputs),
	}})
}

func (d *jsonDisplayer) ReportToolResult(result ToolResult) error {
	rest, failed := strings.CutPrefix(result.Output, toolErrorPrefix)
	fields := []slog.Attr{
		slog.String("name", result.Name),
		slog.Bool("ok", !failed),
		slog.String("output", result.Output),
	}
	if failed {
		firstLine, _, _ := strings.Cut(rest, "\n")
		firstLine = strings.TrimSuffix(firstLine, "\r")
		fields = appendOptional(fields, "error", firstLine)
	}
	return d.enc.Encode(jsonl.Record{Type: "tool_result", Fields: fields})
}

func (d *jsonDisplayer) ReportProgress(p Progress) error {
	fields := []slog.Attr{slog.String("title", p.Title)}
	fields = appendOptional(fields, "row", p.Row)
	fields = append(fields, slog.String("state", p.State))
	fields = appendOptional(fields, "detail", p.Detail)
	return d.enc.Encode(jsonl.Record{Type: "progress", Fields: fields})
}

func (d *jsonDisplayer) ShowPrompt(p Prompt) error {
	fields := []slog.Attr{
		slog.String("kind", p.Kind),
		slog.String("text", p.Text),
	}
	if len(p.Choices) > 0 {
		fields = append(fields, slog.Any("choices", p.Choices))
	}
	return d.enc.Encode(jsonl.Record{Type: "prompt", Fields: fields})
}

func (d *jsonDisplayer) text(recordType, text string) error {
	return d.enc.Encode(jsonl.Record{Type: recordType, Fields: []slog.Attr{slog.String("text", text)}})
}

func appendOptional(fields []slog.Attr, key, value string) []slog.Attr {
	if value == "" {
		return fields
	}
	return append(fields, slog.String(key, value))
}
