package diag

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/pkg/diagnostic"
)

const (
	sgrReset = "\x1b[0m"
	sgrRed   = "1;31"
	sgrAmber = "33"
	sgrCyan  = "36"
	sgrDim   = "2"
	sgrGray  = "90"
)

type attachedViewport struct {
	viewport Viewport
}

type textPresenter struct {
	destination io.Writer
	color       Color
	level       diagnostic.Level
	redactor    redactor

	mu        sync.Mutex
	viewports []*attachedViewport
}

func (p *textPresenter) Attach(v Viewport) func() {
	entry := &attachedViewport{viewport: v}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.viewports = append(p.viewports, entry)
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.viewports = slices.DeleteFunc(p.viewports, func(attached *attachedViewport) bool {
			return attached == entry
		})
	}
}

func (p *textPresenter) Deliver(_ context.Context, event diagnostic.Event) error {
	var line strings.Builder
	line.WriteString(p.paint(levelColor(event.Level), levelLabel(event.Level)))
	line.WriteString(" [")
	line.WriteString(escapeControls(event.Subsystem))
	line.WriteString("] ")
	line.WriteString(p.redactor.line(event.Message))
	p.writeFacts(&line, "", event.Facts)
	return p.writeLines([]string{line.String()})
}

func (p *textPresenter) writeFacts(line *strings.Builder, prefix string, facts []slog.Attr) {
	for _, fact := range facts {
		key := prefix + fact.Key
		value := fact.Value.Resolve()
		if value.Kind() == slog.KindGroup {
			if fact.Key != "" {
				key += "."
			}
			p.writeFacts(line, key, value.Group())
			continue
		}
		text := value.String()
		if err, ok := value.Any().(error); ok && value.Kind() == slog.KindAny && err != nil {
			text = err.Error()
		}
		line.WriteByte(' ')
		line.WriteString(escapeControls(key))
		line.WriteByte('=')
		line.WriteString(quoted(p.redactor.line(text)))
	}
}

func quoted(text string) string {
	if text != "" && !strings.ContainsAny(text, " \"=") {
		return text
	}
	return `"` + strings.ReplaceAll(text, `"`, `\"`) + `"`
}

func (p *textPresenter) ReportError(_ context.Context, err error) error {
	if err == nil {
		return nil
	}
	causes := independentCauses(err)
	label := p.paint(sgrRed, "error") + ": "
	var lines []string
	head, body := label, "  "
	if len(causes) > 1 {
		lines = append(lines, label+strconv.Itoa(len(causes))+" independent errors")
		head, body = "  - ", "    "
	}
	for _, found := range causes {
		lines = p.causeLines(lines, found, head, body)
	}
	return p.writeLines(lines)
}

func (p *textPresenter) causeLines(lines []string, found cause, head, body string) []string {
	messageLines := strings.Split(p.redactor.text(found.message), "\n")
	lines = append(lines, head+escapeControls(messageLines[0]))
	for _, continued := range messageLines[1:] {
		lines = append(lines, body+escapeControls(continued))
	}
	if p.level >= diagnostic.LevelDebug {
		operation := p.redactor.line(found.operation)
		switch location := locationText(found.location); {
		case location != "" && operation != "":
			lines = append(lines, body+p.paint(sgrDim, "at "+location+" ("+operation+")"))
		case location != "":
			lines = append(lines, body+p.paint(sgrDim, "at "+location))
		case operation != "":
			lines = append(lines, body+p.paint(sgrDim, "in "+operation))
		}
	}
	if p.level >= diagnostic.LevelTrace && len(found.stack) > 0 {
		lines = append(lines, body+p.paint(sgrDim, "stack:"))
		for _, frame := range found.stack {
			lines = append(lines, body+"  "+p.paint(sgrDim, stackFrameText(frame)))
		}
	}
	for _, remedy := range found.remedies {
		lines = append(lines, body+p.paint(sgrCyan, "hint")+": "+escapeControls(remedy))
	}
	return lines
}

// writeLines places lines through the innermost attached viewport, or writes
// them to the destination in one write.
func (p *textPresenter) writeLines(lines []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.viewports) > 0 {
		viewport := p.viewports[len(p.viewports)-1].viewport
		for _, line := range lines {
			viewport.Log(line)
		}
		return nil
	}
	output := strings.Join(lines, "\n") + "\n"
	written, err := io.WriteString(p.destination, output)
	if err != nil {
		return Errorf("write diagnostic output: %w", err)
	}
	if written != len(output) {
		return Errorf("write diagnostic output: %d of %d bytes: %w", written, len(output), io.ErrShortWrite)
	}
	return nil
}

func (p *textPresenter) paint(code, text string) string {
	if p.color != Colored {
		return text
	}
	return "\x1b[" + code + "m" + text + sgrReset
}

func levelColor(level diagnostic.Level) string {
	switch level {
	case diagnostic.LevelError:
		return sgrRed
	case diagnostic.LevelWarn:
		return sgrAmber
	case diagnostic.LevelInfo:
		return sgrCyan
	default:
		return sgrGray
	}
}

type jsonPresenter struct {
	encoder  jsonl.Encoder
	level    diagnostic.Level
	redactor redactor
}

func (p *jsonPresenter) Attach(Viewport) func() {
	return func() {}
}

func (p *jsonPresenter) Deliver(_ context.Context, event diagnostic.Event) error {
	event.Message = p.redactor.text(event.Message)
	event.Facts = p.redactor.attrs(event.Facts)
	if err := p.encoder.Encode(jsonl.EventRecord(event)); err != nil {
		return Errorf("deliver diagnostic record: %w", err)
	}
	return nil
}

func (p *jsonPresenter) ReportError(_ context.Context, err error) error {
	if err == nil {
		return nil
	}
	causes := independentCauses(err)
	values := make([]slog.Value, len(causes))
	for i, found := range causes {
		values[i] = p.causeValue(found)
	}
	record := jsonl.Record{Type: "error", Fields: []slog.Attr{
		slog.String("message", p.redactor.text(causeMessages(causes))),
		slog.Any("causes", values),
	}}
	if err := p.encoder.Encode(record); err != nil {
		return Errorf("report error record: %w", err)
	}
	return nil
}

func (p *jsonPresenter) causeValue(found cause) slog.Value {
	fields := []slog.Attr{slog.String("message", p.redactor.text(found.message))}
	if p.level >= diagnostic.LevelDebug {
		if found.operation != "" {
			fields = append(fields, slog.String("operation", p.redactor.text(found.operation)))
		}
		if location := locationText(found.location); location != "" {
			fields = append(fields, slog.String("location", location))
		}
	}
	if p.level >= diagnostic.LevelTrace && len(found.stack) > 0 {
		frames := make([]string, len(found.stack))
		for i, frame := range found.stack {
			frames[i] = stackFrameText(frame)
		}
		fields = append(fields, slog.Any("stack", frames))
	}
	if len(found.remedies) > 0 {
		fields = append(fields, slog.Any("remedies", slices.Clone(found.remedies)))
	}
	if found.api != nil {
		if found.api.StatusCode != 0 {
			fields = append(fields, slog.Int("status", found.api.StatusCode))
		}
		if found.api.ProviderCode != "" {
			fields = append(fields, slog.String("provider_code", p.redactor.text(found.api.ProviderCode)))
		}
	}
	return slog.GroupValue(fields...)
}
