package vendors

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
	"slices"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// jsonl.go is the filesystem/scanner skeleton shared by every JSONL-backed
// session source (Claude Code, pi, ...): tolerant directory walking, bounded
// line scanning, FS-injectable file opening, and the vendor-agnostic parts of
// message-block mapping. Vendors keep only their schema-specific logic.

const (
	// DiscoverMaxLines bounds how many lines discovery-time scans read per file.
	DiscoverMaxLines = 200
	// ReadMaxToken bounds scanner tokens when reading full sessions; JSONL
	// lines can be huge (pasted-image user messages).
	ReadMaxToken = 10 << 20
)

// OpenAbs opens absPath through fsys, defaulting to the host root filesystem.
// The FS indirection exists for tests; production readers leave it nil.
func OpenAbs(fsys fs.FS, absPath string) (io.ReadCloser, error) {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	// os.DirFS("/") expects paths without the leading separator.
	p := strings.TrimPrefix(absPath, string(filepath.Separator))
	f, err := fsys.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", absPath, err)
	}
	if rc, ok := f.(io.ReadCloser); ok {
		return rc, nil
	}
	return io.NopCloser(f), nil
}

// errStopWalk terminates a walk early without surfacing an error.
var errStopWalk = errors.New("stop walk")

// WalkJSONLFiles calls visit for every *.jsonl file under root, skipping
// directories named in skipDirs and tolerating unreadable entries (one bad
// file must not hide the whole source). visit returns true to stop early.
// A missing, empty, or non-directory root yields no visits and no error.
func WalkJSONLFiles(ctx context.Context, root string, skipDirs []string, visit func(absPath string) (stop bool)) error {
	if root == "" {
		return nil
	}
	st, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %q: %w", root, err)
	}
	if !st.IsDir() {
		return nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if visit(p) {
			return errStopWalk
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return fmt.Errorf("walk %q: %w", root, err)
	}
	return nil
}

// ScanJSONLLines feeds each non-empty, JSON-object line of r to fn, skipping
// unparsable lines. fn returns false to stop. maxLines bounds the scan when
// positive; maxToken bounds the scanner buffer. The scanner error (e.g. a
// line exceeding maxToken) is returned so callers can decide whether a
// truncated scan matters.
func ScanJSONLLines(r io.Reader, maxToken, maxLines int, fn func(env map[string]any) bool) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), maxToken)
	lines := 0
	for s.Scan() {
		lines++
		if maxLines > 0 && lines > maxLines {
			return nil
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if !fn(env) {
			return nil
		}
	}
	return s.Err()
}

// HomeRelativeRoot returns override when set, else $HOME joined with parts,
// else "" (no HOME, no root).
func HomeRelativeRoot(override string, parts ...string) string {
	if override != "" {
		return override
	}
	h := os.Getenv("HOME")
	if h == "" {
		return ""
	}
	return filepath.Join(append([]string{h}, parts...)...)
}

// TextBlocksContent flattens content that vendors store as either a plain
// string or an array of {"type":"text"} blocks, joining texts with newlines.
func TextBlocksContent(c any) string {
	if s, ok := c.(string); ok {
		return s
	}
	arr, ok := c.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(arr))
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "text" {
			continue
		}
		if t, _ := m["text"].(string); t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n")
}

// ToolCallBlockKeys names the vendor-specific JSON keys of an assistant
// tool-call block; "id" and "name" are shared by all known vendors.
type ToolCallBlockKeys struct {
	// Type is the block-type marker, e.g. "tool_use" (Claude) / "toolCall" (pi).
	Type string
	// Args is the arguments key, e.g. "input" (Claude) / "arguments" (pi).
	Args string
}

// MapAssistantBlocks converts an assistant message's content (a plain string
// or an array of text/thinking/tool-call blocks) into at most one assistant
// chat message. Empty assistant messages (stream "start" markers, etc) are
// dropped: DeepSeek and similar APIs require content or tool_calls to be set;
// reasoning-only messages surface their thinking as content instead.
func MapAssistantBlocks(content any, toolKeys ToolCallBlockKeys) []pub_models.Message {
	arr, ok := content.([]any)
	if !ok {
		if s, _ := content.(string); s != "" {
			return []pub_models.Message{{Role: "assistant", Content: s}}
		}
		return nil
	}
	out := pub_models.Message{Role: "assistant"}
	texts := make([]string, 0, 2)
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch typ, _ := m["type"].(string); typ {
		case "text":
			if t, _ := m["text"].(string); t != "" {
				texts = append(texts, t)
			}
		case "thinking":
			if th, _ := m["thinking"].(string); th != "" {
				out.ReasoningContent = JoinNonEmpty(out.ReasoningContent, th)
			}
		case toolKeys.Type:
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			args := "{}"
			if input := m[toolKeys.Args]; input != nil {
				if b, err := json.Marshal(input); err == nil {
					args = string(b)
				}
			}
			call := pub_models.Call{
				ID: id,
				Function: pub_models.Specification{
					Name:      name,
					Arguments: args,
				},
			}
			call.Patch()
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	out.Content = strings.Join(texts, "\n")
	if out.Content == "" && len(out.ToolCalls) == 0 {
		if out.ReasoningContent == "" {
			return nil
		}
		out.Content = "[thinking] " + out.ReasoningContent
	}
	return []pub_models.Message{out}
}
