package jsonltest

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(shape Shape) CorpusOptions {
	return CorpusOptions{Shape: shape, Sessions: MinSessions + 2, LinesPerSession: 12}
}

func allShapes() []Shape { return []Shape{ShapeClaude, ShapePi} }

func TestWriteCorpus_deterministicForSeed(t *testing.T) {
	for _, shape := range allShapes() {
		t.Run(shape.String(), func(t *testing.T) {
			opts := testOptions(shape)
			a := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), opts)
			b := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), opts)

			relA, relB := relPaths(t, a), relPaths(t, b)
			if len(relA) == 0 {
				t.Fatal("corpus wrote no files")
			}
			if strings.Join(relA, "\n") != strings.Join(relB, "\n") {
				t.Fatalf("file lists differ:\n%v\n%v", relA, relB)
			}
			for i, rel := range relA {
				bytesA := readFile(t, a.Files[i])
				bytesB := readFile(t, b.Files[i])
				if string(bytesA) != string(bytesB) {
					t.Fatalf("%q differs between two runs of the same seed", rel)
				}
			}
			if factsKey(t, a) != factsKey(t, b) {
				t.Fatalf("facts differ between two runs of the same seed:\n%s\n%s", factsKey(t, a), factsKey(t, b))
			}
			// Timestamps derive from the seed, never from wall-clock time.
			for _, f := range a.Sessions {
				if !f.Created.IsZero() && f.Created.After(time.Now()) {
					t.Fatalf("%q created %v is in the future; timestamps must derive from the seed", f.Path, f.Created)
				}
			}
			// A different seed must change content, or the seed is decorative.
			other := opts
			other.Seed = DefaultSeed + 1
			c := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), other)
			if string(readFile(t, a.Files[0])) == string(readFile(t, c.Files[0])) {
				t.Fatal("a different seed produced identical bytes")
			}
		})
	}
}

func TestWriteCorpus_factsMatchWrittenFiles(t *testing.T) {
	for _, shape := range allShapes() {
		t.Run(shape.String(), func(t *testing.T) {
			c := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), testOptions(shape))
			if len(c.Sessions) != len(c.Files) {
				t.Fatalf("facts = %d, files = %d; want one fact per written file", len(c.Sessions), len(c.Files))
			}
			for i, f := range c.Sessions {
				if f.Path != c.Files[i] {
					t.Fatalf("fact %d path = %q, want %q (facts follow the walk order)", i, f.Path, c.Files[i])
				}
				want := verifyFile(t, shape, f.Path)
				want.Path, want.Kind = f.Path, f.Kind
				if want != f {
					t.Fatalf("fact for %q (%v):\n got  %+v\n want %+v", f.Path, f.Kind, f, want)
				}
			}
		})
	}
}

func TestWriteCorpus_containsRequiredFixtureKinds(t *testing.T) {
	for _, shape := range allShapes() {
		t.Run(shape.String(), func(t *testing.T) {
			opts := testOptions(shape)
			opts.LinesPerSession = DefaultLinesPerSession
			c := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), opts)
			for _, kind := range RequiredKinds {
				facts := c.WithKind(kind)
				if len(facts) == 0 {
					t.Fatalf("no file of kind %v in the %v corpus", kind, shape)
				}
			}
			assertKindProperties(t, shape, c, opts)
		})
	}
}

// assertKindProperties checks the property each awkward kind exists for,
// so a generator change that keeps the label but loses the trap fails here.
func assertKindProperties(t *testing.T, shape Shape, c Corpus, opts CorpusOptions) {
	t.Helper()
	only := func(kind Kind) SessionFact {
		t.Helper()
		facts := c.WithKind(kind)
		if len(facts) != 1 {
			t.Fatalf("kind %v: %d files, want exactly one", kind, len(facts))
		}
		return facts[0]
	}

	if got := only(KindLongSession); got.Messages <= opts.LinesPerSession {
		t.Fatalf("long session carries %d messages, want more than %d", got.Messages, opts.LinesPerSession)
	}
	for _, kind := range []Kind{KindOrdinary, KindLongSession} {
		for _, f := range c.WithKind(kind) {
			if f.Messages <= 200 {
				t.Fatalf("%v %q carries %d messages; the corpus must exceed the discovery line cap", kind, f.Path, f.Messages)
			}
		}
	}
	if got := readFile(t, only(KindQuotedRoleMarker).Path); !strings.Contains(string(got), `\"type\":\"user\"`) {
		t.Fatal("quoted-role-marker file does not quote a role marker in a message body")
	}
	if got := only(KindEmptyFile); len(readFile(t, got.Path)) != 0 {
		t.Fatal("empty-file fixture is not empty")
	}
	if got := readFile(t, only(KindNoTrailingNewline).Path); strings.HasSuffix(string(got), "\n") {
		t.Fatal("no-trailing-newline fixture ends with a newline")
	}
	if got := only(KindNoIdentity); got.SessionID != "" {
		t.Fatalf("no-identity fixture carries session id %q", got.SessionID)
	}
	if got := only(KindTwoIdentities); got.SessionID != firstIdentity(t, shape, got.Path) {
		t.Fatalf("two-identity fixture: fact id %q is not the first identity in the file", got.SessionID)
	}
	if got := identityCount(t, shape, only(KindTwoIdentities).Path); got != 2 {
		t.Fatalf("two-identity fixture carries %d identities, want two", got)
	}
	if got := only(KindLateIdentity); firstIdentityLine(t, shape, got.Path) == 0 {
		t.Fatal("late-identity fixture establishes identity on its first line")
	}
	// The count/preview divergence: the tool-result-first line is counted as
	// a message but contributes no preview.
	got := only(KindToolResultFirstUser)
	if got.FirstUserText == "" {
		t.Fatal("tool-result-first fixture has no later user text to preview")
	}
	if strings.Contains(got.FirstUserText, "tool-result") {
		t.Fatalf("preview %q came from the tool-result line", got.FirstUserText)
	}
	switch shape {
	case ShapeClaude:
		if only(KindSidechainFile).SessionID != "" {
			t.Fatal("an all-sidechain claude file must yield no identity")
		}
		lines := onlyLines(t, only(KindSidechainLines).Path)
		sidechains := 0
		for _, line := range lines {
			env := decode(t, line)
			if sc, _ := env["isSidechain"].(bool); sc {
				sidechains++
			}
		}
		if sidechains == 0 {
			t.Fatal("sidechain-lines fixture carries no sidechain line")
		}
		if got := only(KindSidechainLines); got.Messages >= len(lines) {
			t.Fatalf("sidechain lines were counted: %d messages over %d lines", got.Messages, len(lines))
		}
	case ShapePi:
		if only(KindToolResult).Messages == 0 {
			t.Fatal("pi tool-result fixture counts no messages")
		}
	case ShapeUnset:
		t.Fatalf("unset shape reached the corpus")
	}
}

func TestWriteCorpusErr_rootNotADirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "corpus")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c, err := WriteCorpusErr(root, testOptions(ShapeClaude))
	if err == nil {
		t.Fatal("WriteCorpusErr over a file root returned no error")
	}
	if !strings.Contains(err.Error(), root) {
		t.Fatalf("error %q does not name %q", err, root)
	}
	if len(c.Files) != 0 || len(c.Sessions) != 0 {
		t.Fatalf("failed write returned %d files and %d facts, want none", len(c.Files), len(c.Sessions))
	}
	if got := readFile(t, root); string(got) != "not a directory" {
		t.Fatalf("the root file was modified: %q", got)
	}
}

func TestWriteCorpusErr_unwritableRootFails(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	root := filepath.Join(parent, "corpus")
	c, err := WriteCorpusErr(root, testOptions(ShapePi))
	if err == nil {
		t.Fatal("WriteCorpusErr under a file parent returned no error")
	}
	if len(c.Files) != 0 {
		t.Fatalf("failed write returned %d files, want none", len(c.Files))
	}
	// The parent is a file, so the root cannot exist; ENOTDIR and ENOENT are
	// both "nothing was written".
	if _, statErr := os.Stat(root); statErr == nil {
		t.Fatalf("Stat(%q) succeeded, want no partial corpus", root)
	}
	if got := readFile(t, parent); string(got) != "file" {
		t.Fatalf("the parent file was modified: %q", got)
	}
}

func TestWriteCorpusErr_zeroSessionsIsEmpty(t *testing.T) {
	for _, shape := range allShapes() {
		t.Run(shape.String(), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "corpus")
			c, err := WriteCorpusErr(root, CorpusOptions{Shape: shape, Sessions: 0})
			if err != nil {
				t.Fatalf("WriteCorpusErr with zero sessions: %v", err)
			}
			if len(c.Files) != 0 || len(c.Sessions) != 0 {
				t.Fatalf("zero sessions produced %d files and %d facts, want an empty corpus", len(c.Files), len(c.Sessions))
			}
			if c.Root != root {
				t.Fatalf("Root = %q, want %q", c.Root, root)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatalf("ReadDir(%q): %v", root, err)
			}
			if len(entries) != 0 {
				t.Fatalf("zero sessions left %v under the root", entries)
			}
		})
	}
}

func TestWriteCorpusErr_invalidShape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "corpus")
	if _, err := WriteCorpusErr(root, CorpusOptions{Sessions: 1}); err == nil {
		t.Fatal("an unset shape must be an error: CorpusOptions.Shape has no default")
	}
}

func TestCountingFS_statDoesNotOpen(t *testing.T) {
	c := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), testOptions(ShapeClaude))
	fsys, counts := CountingFS("/")
	name := strings.TrimPrefix(c.Files[0], "/")

	info, err := fs.Stat(fsys, name)
	if err != nil {
		t.Fatalf("fs.Stat(%q): %v", name, err)
	}
	if info.Size() == 0 {
		t.Fatalf("Stat(%q) reported an empty file", name)
	}
	if got := counts.Stats(name); got != 1 {
		t.Fatalf("stats(%q) = %d, want 1", name, got)
	}
	if got := counts.TotalOpens(); got != 0 {
		t.Fatalf("total opens = %d, want 0: a stat must not open the file", got)
	}
}

func TestCountingFS_recordsOpensPerPath(t *testing.T) {
	c := WriteCorpus(t, filepath.Join(t.TempDir(), "corpus"), testOptions(ShapePi))
	fsys, counts := CountingFS("/")
	first := strings.TrimPrefix(c.Files[0], "/")
	second := strings.TrimPrefix(c.Files[1], "/")

	for range 2 {
		f, err := fsys.Open(first)
		if err != nil {
			t.Fatalf("Open(%q): %v", first, err)
		}
		if _, err := io.ReadAll(f); err != nil {
			t.Fatalf("ReadAll(%q): %v", first, err)
		}
		f.Close()
	}
	f, err := fsys.Open(second)
	if err != nil {
		t.Fatalf("Open(%q): %v", second, err)
	}
	f.Close()

	if got := counts.Opens(first); got != 2 {
		t.Fatalf("opens(%q) = %d, want 2", first, got)
	}
	if got := counts.Opens(second); got != 1 {
		t.Fatalf("opens(%q) = %d, want 1", second, got)
	}
	if got := counts.TotalOpens(); got != 3 {
		t.Fatalf("total opens = %d, want 3", got)
	}
	if got := counts.OpenedPaths(); len(got) != 2 {
		t.Fatalf("opened paths = %v, want two distinct paths", got)
	}
}

func TestCountingFS_missingPathCounted(t *testing.T) {
	fsys, counts := CountingFS(t.TempDir())

	_, openErr := fsys.Open("absent.jsonl")
	if !errors.Is(openErr, fs.ErrNotExist) {
		t.Fatalf("Open(absent) = %v, want the underlying not-exist error unchanged", openErr)
	}
	_, statErr := fs.Stat(fsys, "absent.jsonl")
	if !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("Stat(absent) = %v, want the underlying not-exist error unchanged", statErr)
	}
	if got := counts.Opens("absent.jsonl"); got != 1 {
		t.Fatalf("opens(absent) = %d, want the miss counted", got)
	}
	if got := counts.Stats("absent.jsonl"); got != 1 {
		t.Fatalf("stats(absent) = %d, want the miss counted", got)
	}
}

// --- independent verification helpers -------------------------------------
//
// These re-derive a SessionFact from the bytes on disk with their own decode
// loop, so the oracle is checked against the files rather than against the
// bookkeeping that produced them.

func verifyFile(t *testing.T, shape Shape, path string) SessionFact {
	t.Helper()
	got := SessionFact{}
	for _, line := range onlyLines(t, path) {
		env := decode(t, line)
		if env == nil {
			continue
		}
		switch shape {
		case ShapeClaude:
			verifyClaudeLine(env, &got)
		case ShapePi:
			verifyPiLine(env, &got)
		case ShapeUnset:
			t.Fatal("unset shape")
		}
	}
	return got
}

func verifyClaudeLine(env map[string]any, got *SessionFact) {
	if sc, _ := env["isSidechain"].(bool); sc {
		return
	}
	if got.SessionID == "" {
		got.SessionID, _ = env["sessionId"].(string)
	}
	if got.Cwd == "" {
		got.Cwd, _ = env["cwd"].(string)
	}
	if got.Created.IsZero() {
		if ts, _ := env["timestamp"].(string); ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				got.Created = parsed
			}
		}
	}
	msg, _ := env["message"].(map[string]any)
	switch typ, _ := env["type"].(string); typ {
	case "user":
		got.Messages++
		if got.FirstUserText == "" && msg != nil {
			got.FirstUserText = textBlocks(msg["content"])
		}
	case "assistant":
		got.Messages++
		if got.Model == "" && msg != nil {
			got.Model, _ = msg["model"].(string)
		}
	}
}

func verifyPiLine(env map[string]any, got *SessionFact) {
	switch typ, _ := env["type"].(string); typ {
	case "session":
		if got.SessionID == "" {
			got.SessionID, _ = env["id"].(string)
		}
		if got.Cwd == "" {
			got.Cwd, _ = env["cwd"].(string)
		}
		if got.Created.IsZero() {
			if ts, _ := env["timestamp"].(string); ts != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					got.Created = parsed
				}
			}
		}
	case "message":
		msg, _ := env["message"].(map[string]any)
		if msg == nil {
			return
		}
		switch role, _ := msg["role"].(string); role {
		case "user":
			got.Messages++
			if got.FirstUserText == "" {
				got.FirstUserText = textBlocks(msg["content"])
			}
		case "assistant":
			got.Messages++
			if got.Model == "" {
				got.Model, _ = msg["model"].(string)
			}
		case "toolResult":
			got.Messages++
		}
	}
}

func textBlocks(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	arr, ok := content.([]any)
	if !ok {
		return ""
	}
	texts := []string{}
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "text" {
			continue
		}
		if s, _ := m["text"].(string); s != "" {
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, "\n")
}

func identityOf(shape Shape, env map[string]any) string {
	if shape == ShapePi {
		if typ, _ := env["type"].(string); typ != "session" {
			return ""
		}
		id, _ := env["id"].(string)
		return id
	}
	if sc, _ := env["isSidechain"].(bool); sc {
		return ""
	}
	id, _ := env["sessionId"].(string)
	return id
}

func identityCount(t *testing.T, shape Shape, path string) int {
	t.Helper()
	seen := map[string]struct{}{}
	for _, line := range onlyLines(t, path) {
		if id := identityOf(shape, decode(t, line)); id != "" {
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func firstIdentity(t *testing.T, shape Shape, path string) string {
	t.Helper()
	for _, line := range onlyLines(t, path) {
		if id := identityOf(shape, decode(t, line)); id != "" {
			return id
		}
	}
	return ""
}

func firstIdentityLine(t *testing.T, shape Shape, path string) int {
	t.Helper()
	for i, line := range onlyLines(t, path) {
		if id := identityOf(shape, decode(t, line)); id != "" {
			return i
		}
	}
	return -1
}

func onlyLines(t *testing.T, path string) [][]byte {
	t.Helper()
	raw := readFile(t, path)
	if len(raw) == 0 {
		return nil
	}
	out := [][]byte{}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, []byte(line))
	}
	return out
}

func decode(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatalf("corpus wrote an undecodable line %q: %v", line, err)
	}
	return env
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return b
}

func relPaths(t *testing.T, c Corpus) []string {
	t.Helper()
	out := make([]string, 0, len(c.Files))
	for _, p := range c.Files {
		rel, err := filepath.Rel(c.Root, p)
		if err != nil {
			t.Fatalf("Rel(%q, %q): %v", c.Root, p, err)
		}
		out = append(out, rel)
	}
	return out
}

func factsKey(t *testing.T, c Corpus) string {
	t.Helper()
	parts := make([]string, 0, len(c.Sessions))
	for _, f := range c.Sessions {
		rel, err := filepath.Rel(c.Root, f.Path)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		parts = append(parts, strings.Join([]string{
			rel, f.Kind.String(), f.SessionID, f.Cwd,
			f.Created.Format(time.RFC3339Nano), f.Model, f.FirstUserText,
		}, "|"))
	}
	return strings.Join(parts, "\n")
}
