package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SourceReader reads Pi coding agent session logs from disk.
//
// Storage: ~/.pi/agent/sessions/<escaped-cwd>/<timestamp>_<uuid>.jsonl
// Each line is JSON with top-level "type":
//
//	"session"             – metadata (id, cwd, timestamp, version)
//	"model_change"        – provider/model change (skipped)
//	"thinking_level_change" – thinking level change (skipped)
//	"message"             – chat message with message.role: user, assistant, toolResult
//
// Content blocks within message.content array:
//
//	{"type":"text","text":"..."}
//	{"type":"thinking","thinking":"..."}
//	{"type":"toolCall","id":"...","name":"...","arguments":{...}}
//
// clai never writes back to these sources.
type SourceReader struct {
	FS fs.FS
	// Root is the absolute directory that contains the pi sessions.
	// If empty, defaults to $HOME/.pi/agent/sessions.
	Root string
}

func (r SourceReader) Source() string {
	return "pi"
}

const (
	discoverMaxLines = 200
)

func (r SourceReader) Discover(ctx context.Context) ([]vendors.SourceRow, error) {
	root := r.sessionsRoot()
	if root == "" {
		return []vendors.SourceRow{}, nil
	}
	st, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []vendors.SourceRow{}, nil
		}
		return nil, fmt.Errorf("stat pi sessions dir %q: %w", root, err)
	}
	if !st.IsDir() {
		return []vendors.SourceRow{}, nil
	}

	var rows []vendors.SourceRow
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		row, ok := r.discoverOne(ctx, p)
		if !ok {
			return nil
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk pi sessions dir %q: %w", root, err)
	}
	return rows, nil
}

func (r SourceReader) discoverOne(ctx context.Context, absPath string) (vendors.SourceRow, bool) {
	f, err := r.open(absPath)
	if err != nil {
		return vendors.SourceRow{}, false
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20)

	row := vendors.SourceRow{Source: r.Source(), RawPath: absPath}
	lines := 0
	for s.Scan() {
		select {
		case <-ctx.Done():
			return vendors.SourceRow{}, false
		default:
		}
		lines++
		if lines > discoverMaxLines {
			break
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}

		topType, _ := env["type"].(string)
		switch topType {
		case "session":
			if row.SourceID == "" {
				if sid, _ := env["id"].(string); sid != "" {
					row.SourceID = sid
				}
			}
			if row.Created.IsZero() {
				if ts, _ := env["timestamp"].(string); ts != "" {
					if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						row.Created = t
					}
				}
			}
		case "message":
			msg, _ := env["message"].(map[string]any)
			if msg == nil {
				continue
			}
			role, _ := msg["role"].(string)
			switch role {
			case "user":
				row.MessageCount++
				if row.FirstUserMessage == "" {
					row.FirstUserMessage, row.FullFirstUserMessage = extractPiUserContent(msg)
				}
			case "assistant":
				row.MessageCount++
				if row.Model == "" {
					if m, _ := msg["model"].(string); m != "" {
						row.Model = m
					}
				}
			case "toolResult":
				row.MessageCount++
			}
		case "model_change", "thinking_level_change":
			// skip, not relevant for chat history
		}
	}

	if row.SourceID == "" {
		return vendors.SourceRow{}, false
	}
	if row.Created.IsZero() {
		if st, err := os.Stat(absPath); err == nil {
			row.Created = st.ModTime()
		}
	}
	if row.FirstUserMessage == "" {
		row.FirstUserMessage = "(no preview)"
	}
	return row, true
}

func extractPiUserContent(msg map[string]any) (string, string) {
	content, _ := msg["content"].([]any)
	if len(content) == 0 {
		return "", ""
	}
	var full strings.Builder
	for _, v := range content {
		block, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := block["type"].(string); typ == "text" {
			if t, _ := block["text"].(string); t != "" {
				if full.Len() > 0 {
					full.WriteString("\n")
				}
				full.WriteString(t)
			}
		}
	}
	s := full.String()
	return vendors.TruncateOneLine(s, 100), s
}

func (r SourceReader) Read(ctx context.Context, sourceID string) (pub_models.Chat, error) {
	absPath, err := r.findSessionFile(sourceID)
	if err != nil {
		return pub_models.Chat{}, err
	}
	f, err := r.open(absPath)
	if err != nil {
		return pub_models.Chat{}, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 10<<20)
	msgs := make([]pub_models.Message, 0, 128)
	created := time.Time{}
	cwd := ""
	sessionFound := false

	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		topType, _ := env["type"].(string)
		switch topType {
		case "session":
			sessionFound = true
			if created.IsZero() {
				if ts, _ := env["timestamp"].(string); ts != "" {
					if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						created = t
					}
				}
			}
			if cwd == "" {
				if v, _ := env["cwd"].(string); v != "" {
					cwd = v
				}
			}
		case "message":
			if !sessionFound {
				continue
			}
			msg, _ := env["message"].(map[string]any)
			if msg == nil {
				continue
			}
			role, _ := msg["role"].(string)
			switch role {
			case "user":
				msgs = append(msgs, mapPiUserMessage(msg)...)
			case "assistant":
				msgs = append(msgs, mapPiAssistantMessage(msg)...)
			case "toolResult":
				msgs = append(msgs, mapPiToolResultMessage(msg))
			}
		case "model_change", "thinking_level_change":
			// skip
		}
	}
	if err := s.Err(); err != nil {
		return pub_models.Chat{}, fmt.Errorf("scan jsonl %q: %w", absPath, err)
	}

	// Post-process: normalize pi's parallel/interleaved tool call pattern
	msgs = vendors.NormalizeToolCallSequence(msgs)

	sys := pub_models.Message{Role: "system", Content: fmt.Sprintf("Continued from Pi session %s", sourceID)}
	if cwd != "" {
		sys.Content = fmt.Sprintf("Continued from Pi session %s (originally at %s).", sourceID, cwd)
	}

	chat := pub_models.Chat{
		Created:  created,
		ID:       "",
		Source:   r.Source(),
		SourceID: sourceID,
		Messages: append([]pub_models.Message{sys}, msgs...),
	}
	if chat.Created.IsZero() {
		if st, err := os.Stat(absPath); err == nil {
			chat.Created = st.ModTime()
		}
	}
	return chat, nil
}

func (r SourceReader) findSessionFile(sourceID string) (string, error) {
	root := r.sessionsRoot()
	if root == "" {
		return "", fmt.Errorf("pi sessions root not configured")
	}
	var found string
	errFound := errors.New("found")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		ok, err := fileHasPiSessionID(r, p, sourceID)
		if err != nil {
			return nil
		}
		if ok {
			found = p
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return "", fmt.Errorf("walk pi sessions dir %q: %w", root, err)
	}
	if found == "" {
		return "", fmt.Errorf("pi session %q not found", sourceID)
	}
	return found, nil
}

func (r SourceReader) sessionsRoot() string {
	if r.Root != "" {
		return r.Root
	}
	h := os.Getenv("HOME")
	if h == "" {
		return ""
	}
	return filepath.Join(h, ".pi", "agent", "sessions")
}

func fileHasPiSessionID(r SourceReader, absPath, want string) (bool, error) {
	f, err := r.open(absPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lines := 0
	for s.Scan() {
		lines++
		if lines > discoverMaxLines {
			break
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if typ, _ := env["type"].(string); typ == "session" {
			if sid, _ := env["id"].(string); sid == want {
				return true, nil
			}
			// Session ID is only on the "session" line. If it doesn't match,
			// this file is not the one we want.
			return false, nil
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func mapPiUserMessage(msg map[string]any) []pub_models.Message {
	content, _ := msg["content"].([]any)
	var texts []string
	for _, v := range content {
		block, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := block["type"].(string); typ == "text" {
			if t, _ := block["text"].(string); t != "" {
				texts = append(texts, t)
			}
		}
	}
	if len(texts) == 0 {
		return nil
	}
	return []pub_models.Message{{Role: "user", Content: strings.Join(texts, "\n")}}
}

func mapPiAssistantMessage(msg map[string]any) []pub_models.Message {
	content, _ := msg["content"].([]any)
	out := pub_models.Message{Role: "assistant"}
	texts := make([]string, 0, 2)
	for _, v := range content {
		block, ok := v.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := block["type"].(string)
		switch typ {
		case "text":
			if t, _ := block["text"].(string); t != "" {
				texts = append(texts, t)
			}
		case "thinking":
			if th, _ := block["thinking"].(string); th != "" {
				out.ReasoningContent = vendors.JoinNonEmpty(out.ReasoningContent, th)
			}
		case "toolCall":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			args := block["arguments"]
			argsStr := "{}"
			if args != nil {
				if b, err := json.Marshal(args); err == nil {
					argsStr = string(b)
				}
			}
			call := pub_models.Call{
				ID: id,
				Function: pub_models.Specification{
					Name:      name,
					Arguments: argsStr,
				},
			}
			call.Patch()
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	out.Content = strings.Join(texts, "\n")
	if out.Content == "" && len(out.ToolCalls) == 0 {
		if out.ReasoningContent != "" {
			out.Content = "[thinking] " + out.ReasoningContent
		} else {
			return nil
		}
	}
	return []pub_models.Message{out}
}

func mapPiToolResultMessage(msg map[string]any) pub_models.Message {
	toolCallID, _ := msg["toolCallId"].(string)
	content, _ := msg["content"].([]any)
	var texts []string
	for _, v := range content {
		block, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := block["type"].(string); typ == "text" {
			if t, _ := block["text"].(string); t != "" {
				texts = append(texts, t)
			}
		}
	}
	return pub_models.Message{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    strings.Join(texts, "\n"),
	}
}

func (r SourceReader) open(absPath string) (io.ReadCloser, error) {
	fsys := r.FS
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	p := strings.TrimPrefix(absPath, string(filepath.Separator))
	f, err := fsys.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", absPath, err)
	}
	rc, ok := f.(io.ReadCloser)
	if ok {
		return rc, nil
	}
	return io.NopCloser(f), nil
}
