package jsonl

import "log/slog"

// ChildOutputRecord is the "child_output" record of one line a child process
// wrote: the fields source, stream ("stdout" or "stderr") and text, the line
// without its terminator. The text is the child's own: nothing is removed
// from it, and the Encoder escapes its control characters. (F14)
func ChildOutputRecord(source, stream, text string) Record {
	return Record{Type: "child_output", Fields: []slog.Attr{
		slog.String("source", source),
		slog.String("stream", stream),
		slog.String("text", text),
	}}
}
