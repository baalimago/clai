package vendors_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
	"github.com/baalimago/clai/internal/vendors/pi"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// corpusOptions is the fixture scale most tests in this file use: every
// required kind plus a couple of ordinary sessions, short enough that a
// corpus is written and scanned many times over inside the race gate.
func corpusOptions(shape jsonltest.Shape) jsonltest.CorpusOptions {
	return jsonltest.CorpusOptions{
		Shape:           shape,
		Sessions:        jsonltest.MinSessions + 2,
		LinesPerSession: 12,
	}
}

type vendorCase struct {
	name   string
	shape  jsonltest.Shape
	reader func(root string) vendors.SourceReader
}

func vendorCases() []vendorCase {
	return []vendorCase{
		{"claude-code", jsonltest.ShapeClaude, func(root string) vendors.SourceReader {
			return anthropic.SourceReader{Root: root}
		}},
		{"pi", jsonltest.ShapePi, func(root string) vendors.SourceReader {
			return pi.SourceReader{Root: root}
		}},
	}
}

// TestDiscoverJSONL_matchesCorpusFacts drives the real vendor readers over a
// generated corpus and checks every row field against the corpus oracle. It is
// the phase's behaviour-preservation anchor: it passes identically before and
// after discovery moves into the generic layer.
func TestDiscoverJSONL_matchesCorpusFacts(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			corpus := jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			reader := tc.reader(root)

			rows, err := reader.Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := keptFacts(corpus)
			if len(rows) != len(want) {
				t.Fatalf("got %d rows, want %d (identity-less files must be dropped)", len(rows), len(want))
			}
			for i, row := range rows {
				fact := want[i]
				if row.RawPath != fact.Path {
					t.Fatalf("row %d: RawPath %q, want %q (walk order)", i, row.RawPath, fact.Path)
				}
				checkRowAgainstFact(t, reader.Source(), row, fact)
			}
		})
	}
}

func keptFacts(c jsonltest.Corpus) []jsonltest.SessionFact {
	out := []jsonltest.SessionFact{}
	for _, f := range c.Sessions {
		if f.SessionID != "" {
			out = append(out, f)
		}
	}
	return out
}

func checkRowAgainstFact(t *testing.T, source string, row vendors.SourceRow, fact jsonltest.SessionFact) {
	t.Helper()
	if row.Source != source {
		t.Errorf("%v: Source %q, want %q", fact.Kind, row.Source, source)
	}
	if row.SourceID != fact.SessionID {
		t.Errorf("%v: SourceID %q, want %q", fact.Kind, row.SourceID, fact.SessionID)
	}
	if row.Cwd != fact.Cwd {
		t.Errorf("%v: Cwd %q, want %q", fact.Kind, row.Cwd, fact.Cwd)
	}
	if !row.Created.Equal(fact.Created) {
		t.Errorf("%v: Created %v, want %v", fact.Kind, row.Created, fact.Created)
	}
	if row.Model != fact.Model {
		t.Errorf("%v: Model %q, want %q", fact.Kind, row.Model, fact.Model)
	}
	if row.MessageCount != fact.Messages {
		t.Errorf("%v: MessageCount %d, want %d", fact.Kind, row.MessageCount, fact.Messages)
	}
	if row.FullFirstUserMessage != fact.FirstUserText {
		t.Errorf("%v: FullFirstUserMessage %q, want %q", fact.Kind, row.FullFirstUserMessage, fact.FirstUserText)
	}
	wantPreview := vendors.TruncateOneLine(fact.FirstUserText, 100)
	if wantPreview == "" {
		wantPreview = "(no preview)"
	}
	if row.FirstUserMessage != wantPreview {
		t.Errorf("%v: FirstUserMessage %q, want %q", fact.Kind, row.FirstUserMessage, wantPreview)
	}
}

// --- generic-layer fixtures -------------------------------------------------

// stubLine is the line format the stub schema understands. It maps one to one
// onto LineFields, so a test states a line's contribution instead of
// re-encoding a vendor's JSON dialect.
type stubLine struct {
	SessionID string `json:"sid,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Timestamp string `json:"ts,omitempty"`
	Model     string `json:"model,omitempty"`
	Role      string `json:"role,omitempty"`
	Text      string `json:"text,omitempty"`
}

func (l stubLine) String() string {
	b, err := json.Marshal(l)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// stubSchema is a JSONLSchema whose line meaning a test dictates. It records
// every line it was asked about, which is how "read no further" is proven.
type stubSchema struct {
	root   string
	skip   []string
	roleOf func(string) vendors.LineRole
	// onLine runs before every line is interpreted, so a test can mutate the
	// file that is being scanned right now, or rendezvous with another worker.
	onLine func(line []byte)
	// probe, when set, brackets every Fields call so concurrency is observed.
	probe *concurrencyProbe
	mu    sync.Mutex
	seen  []string
}

func newStubSchema(root string) *stubSchema {
	return &stubSchema{root: root}
}

func (s *stubSchema) SourceName() string { return "stub" }
func (s *stubSchema) Root() string       { return s.root }
func (s *stubSchema) SkipDirs() []string { return s.skip }
func (s *stubSchema) lines() []string    { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.seen) }
func (s *stubSchema) lineCount() int     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.seen) }

func (s *stubSchema) Fields(line []byte) vendors.LineFields {
	if s.probe != nil {
		s.probe.enter()
		defer s.probe.leave()
	}
	if s.onLine != nil {
		s.onLine(line)
	}
	s.mu.Lock()
	s.seen = append(s.seen, string(line))
	s.mu.Unlock()

	var sl stubLine
	if err := json.Unmarshal(line, &sl); err != nil {
		return vendors.LineFields{}
	}
	f := vendors.LineFields{SessionID: sl.SessionID, Cwd: sl.Cwd, Model: sl.Model, UserText: sl.Text}
	if sl.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, sl.Timestamp); err == nil {
			f.Timestamp = t
		}
	}
	roleOf := s.roleOf
	if roleOf == nil {
		roleOf = defaultStubRole
	}
	f.Role = roleOf(sl.Role)
	return f
}

func defaultStubRole(role string) vendors.LineRole {
	switch role {
	case "user":
		return vendors.LineRoleUser
	case "assistant":
		return vendors.LineRoleAssistant
	case "tool":
		return vendors.LineRoleTool
	case "skip":
		return vendors.LineRoleSkip
	default:
		return vendors.LineRoleNone
	}
}

// writeStubSession writes one session file and returns its absolute path.
func writeStubSession(t *testing.T, root, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", root, err)
	}
	p := filepath.Join(root, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
	return p
}

func discoverStub(t *testing.T, s *stubSchema, fsys fs.FS) []vendors.SourceRow {
	t.Helper()
	return discoverStubCached(t, s, fsys, nil)
}

func discoverStubCached(t *testing.T, s *stubSchema, fsys fs.FS, cache vendors.SourceCache) []vendors.SourceRow {
	t.Helper()
	rows, err := vendors.DiscoverJSONL(context.Background(), s, fsys, cache)
	if err != nil {
		t.Fatalf("DiscoverJSONL: %v", err)
	}
	return rows
}

func onlyRow(t *testing.T, rows []vendors.SourceRow) vendors.SourceRow {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row, got %d: %+v", len(rows), rows)
	}
	return rows[0]
}

// --- aggregation ------------------------------------------------------------

// TestDiscoverJSONL_aggregationRules executes the phase's aggregation table
// row by row: every field's rule, and every fallback that has one.
func TestDiscoverJSONL_aggregationRules(t *testing.T) {
	root := t.TempDir()
	created := "2026-01-02T03:04:05Z"
	long := strings.Repeat("x", 250)
	path := writeStubSession(t, root, "s.jsonl",
		stubLine{Role: "assistant", Text: "ignored", Model: "first-model"}.String(),
		stubLine{SessionID: "first-id", Cwd: "/first/cwd", Timestamp: created, Model: "later-model"}.String(),
		stubLine{SessionID: "later-id", Cwd: "/later/cwd", Timestamp: "2026-02-02T03:04:05Z"}.String(),
		stubLine{Role: "user", Text: ""}.String(),
		stubLine{Role: "user", Text: "first user\ntext " + long}.String(),
		stubLine{Role: "user", Text: "second user text"}.String(),
		stubLine{Role: "tool"}.String(),
	)
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))

	wantFull := "first user\ntext " + long
	wantPreview := vendors.TruncateOneLine(wantFull, 100)
	for _, tc := range []struct{ field, got, want string }{
		{"Source", row.Source, "stub"},
		{"RawPath", row.RawPath, path},
		{"SourceID", row.SourceID, "first-id"},
		{"Cwd", row.Cwd, "/first/cwd"},
		{"Model", row.Model, "first-model"},
		{"FullFirstUserMessage", row.FullFirstUserMessage, wantFull},
		{"FirstUserMessage", row.FirstUserMessage, wantPreview},
		{"Created", row.Created.Format(time.RFC3339), created},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if utf8.RuneCountInString(row.FirstUserMessage) != 100 {
		t.Errorf("preview is %d runes, want the 100-rune bound", utf8.RuneCountInString(row.FirstUserMessage))
	}
	if strings.Contains(row.FirstUserMessage, "\n") {
		t.Errorf("preview kept a newline: %q", row.FirstUserMessage)
	}
	// assistant, user, user, user, tool — the two identity-only lines are
	// LineRoleNone and contribute fields without being messages.
	if row.MessageCount != 5 {
		t.Errorf("MessageCount = %d, want 5 (roles user/assistant/tool only)", row.MessageCount)
	}
}

// TestDiscoverJSONL_toolResultFirstUserLinePreview covers the corpus fixture
// whose first user line carries only tool-result content: it is counted as a
// message, but the preview comes from the first line with real text.
func TestDiscoverJSONL_toolResultFirstUserLinePreview(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			corpus := jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			facts := corpus.WithKind(jsonltest.KindToolResultFirstUser)
			if len(facts) != 1 {
				t.Fatalf("expected one tool-result-first-user fixture, got %d", len(facts))
			}
			fact := facts[0]

			rows, err := tc.reader(root).Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			row, ok := rowForPath(rows, fact.Path)
			if !ok {
				t.Fatalf("no row for %q", fact.Path)
			}
			if row.FirstUserMessage == "(no preview)" {
				t.Fatal("a tool-result-only first user line suppressed the whole preview")
			}
			if strings.Contains(row.FullFirstUserMessage, "tool-result output") {
				t.Fatalf("tool-result content leaked into the preview source: %q", row.FullFirstUserMessage)
			}
			if row.FullFirstUserMessage != fact.FirstUserText {
				t.Fatalf("FullFirstUserMessage = %q, want %q", row.FullFirstUserMessage, fact.FirstUserText)
			}
			// The uncounted alternative would be one lower.
			if row.MessageCount != fact.Messages {
				t.Fatalf("MessageCount = %d, want %d: the unpreviewable user line still counts", row.MessageCount, fact.Messages)
			}
		})
	}
}

func rowForPath(rows []vendors.SourceRow, path string) (vendors.SourceRow, bool) {
	for _, r := range rows {
		if r.RawPath == path {
			return r, true
		}
	}
	return vendors.SourceRow{}, false
}

// TestDiscoverJSONL_skipRoleContributesNothing checks that LineRoleSkip
// discards the whole line: no identity, no metadata, no count.
func TestDiscoverJSONL_skipRoleContributesNothing(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "a.jsonl",
		stubLine{SessionID: "skipped-id", Cwd: "/skipped", Timestamp: "2026-01-01T00:00:00Z", Model: "skipped-model", Role: "skip", Text: "skipped text"}.String(),
		stubLine{SessionID: "kept-id", Role: "user", Text: "kept text", Timestamp: "2026-03-03T00:00:00Z"}.String(),
	)
	writeStubSession(t, root, "b.jsonl",
		stubLine{SessionID: "all-skipped", Role: "skip"}.String(),
	)
	rows := discoverStub(t, newStubSchema(root), nil)
	row := onlyRow(t, rows)
	if row.SourceID != "kept-id" {
		t.Errorf("SourceID = %q, want kept-id: a skipped line contributed identity", row.SourceID)
	}
	if row.Cwd != "" || row.Model != "" {
		t.Errorf("a skipped line contributed metadata: cwd %q, model %q", row.Cwd, row.Model)
	}
	if row.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1: a skipped line was counted", row.MessageCount)
	}
	if got := row.Created.Format(time.RFC3339); got != "2026-03-03T00:00:00Z" {
		t.Errorf("Created = %v, want the first unskipped timestamp", got)
	}
}

// TestDiscoverJSONL_roleCountingIsSchemaDriven proves the counting difference
// between vendors lives in the schema alone: identical bytes, two role
// mappings, two counts, no branch in the generic layer.
func TestDiscoverJSONL_roleCountingIsSchemaDriven(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String(),
		stubLine{SessionID: "s1", Role: "assistant"}.String(),
		stubLine{SessionID: "s1", Role: "tool"}.String(),
	)
	counting := newStubSchema(root)
	if got := onlyRow(t, discoverStub(t, counting, nil)).MessageCount; got != 3 {
		t.Errorf("tool-counting schema: MessageCount = %d, want 3", got)
	}
	ignoring := newStubSchema(root)
	ignoring.roleOf = func(role string) vendors.LineRole {
		if role == "tool" {
			return vendors.LineRoleNone
		}
		return defaultStubRole(role)
	}
	if got := onlyRow(t, discoverStub(t, ignoring, nil)).MessageCount; got != 2 {
		t.Errorf("tool-ignoring schema: MessageCount = %d, want 2", got)
	}
}

// TestDiscoverJSONL_missingRootYieldsNoRows covers the contract row for a
// vendor that is not installed on this host.
func TestDiscoverJSONL_missingRootYieldsNoRows(t *testing.T) {
	for _, root := range []string{"", filepath.Join(t.TempDir(), "absent")} {
		rows, err := vendors.DiscoverJSONL(context.Background(), newStubSchema(root), nil, nil)
		if err != nil {
			t.Fatalf("root %q: %v", root, err)
		}
		if len(rows) != 0 {
			t.Fatalf("root %q: got %d rows, want none", root, len(rows))
		}
	}
}

// TestDiscoverJSONL_neverWritesToSourceRoot runs the real readers over a
// corpus whose files and directories are read-only, and checks byte for byte
// that nothing under the root moved.
func TestDiscoverJSONL_neverWritesToSourceRoot(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			makeReadOnly(t, root)
			before := treeSnapshot(t, root)

			rows, err := tc.reader(root).Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(rows) == 0 {
				t.Fatal("no rows; the corpus did not reach the reader")
			}
			if after := treeSnapshot(t, root); after != before {
				t.Fatalf("discovery changed the source root:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// makeReadOnly strips write permission from every file and directory under
// root, restoring it afterwards so the temp dir can be removed.
func makeReadOnly(t *testing.T, root string) {
	t.Helper()
	chmodTree(t, root, 0o555, 0o444)
	t.Cleanup(func() { chmodTree(t, root, 0o755, 0o644) })
}

func chmodTree(t *testing.T, root string, dirMode, fileMode os.FileMode) {
	t.Helper()
	paths := []string{}
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	// Deepest first when locking, so a read-only parent never blocks a child.
	slices.Reverse(paths)
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %q: %v", p, err)
		}
		mode := fileMode
		if st.IsDir() {
			mode = dirMode
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatalf("chmod %q: %v", p, err)
		}
	}
}

// treeSnapshot renders every path under root with its size, mod time and
// content hash, so any write at all shows up as a diff.
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	out := strings.Builder{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum := ""
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum = fmt.Sprintf("%x", sha256.Sum256(b))
		}
		fmt.Fprintf(&out, "%s %d %v %s\n", p, info.Size(), info.ModTime().UTC(), sum)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %q: %v", root, err)
	}
	return out.String()
}

// --- session lookup ---------------------------------------------------------

// TestFindJSONLSession_stopsAtFirstIdentityLine proves the lookup reads no
// further than the line that establishes a file's identity: the schema is
// asked about exactly one line, and a second identity later in the file is
// never reachable.
func TestFindJSONLSession_stopsAtFirstIdentityLine(t *testing.T) {
	root := t.TempDir()
	path := writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "first", Role: "user", Text: "hi"}.String(),
		stubLine{SessionID: "second", Role: "user", Text: "later"}.String(),
	)
	fsys, counts := jsonltest.CountingFS("/")
	rel := strings.TrimPrefix(path, "/")

	s := newStubSchema(root)
	got, err := vendors.FindJSONLSession(context.Background(), s, fsys, nil, "first")
	if err != nil {
		t.Fatalf("FindJSONLSession: %v", err)
	}
	if got != path {
		t.Fatalf("got %q, want %q", got, path)
	}
	if n := s.lineCount(); n != 1 {
		t.Fatalf("schema was asked about %d lines, want 1: %v", n, s.lines())
	}
	if n := counts.Opens(rel); n != 1 {
		t.Fatalf("file opened %d times, want 1", n)
	}

	// The second identity is unreachable, which is what "one identity per
	// file, decided by the first line that carries one" means.
	s2 := newStubSchema(root)
	if _, err := vendors.FindJSONLSession(context.Background(), s2, fsys, nil, "second"); err == nil {
		t.Fatal("expected the second identity to be unreachable")
	}
	if n := s2.lineCount(); n != 1 {
		t.Fatalf("schema was asked about %d lines while missing, want 1", n)
	}
}

// TestFindJSONLSession_agreesWithDiscover checks the two paths against each
// other for every session in the corpus, through the real readers: the file a
// lookup resolves is the file discovery reported, including for the fixture
// that carries two identities.
func TestFindJSONLSession_agreesWithDiscover(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			corpus := jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			rows, err := tc.reader(root).Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(rows) == 0 {
				t.Fatal("no rows; the corpus did not reach the reader")
			}
			for _, row := range rows {
				fsys, counts := jsonltest.CountingFS("/")
				reader := tc.reader(root)
				switch r := reader.(type) {
				case anthropic.SourceReader:
					r.FS = fsys
					reader = r
				case pi.SourceReader:
					r.FS = fsys
					reader = r
				}
				chat, err := reader.Read(context.Background(), nil, row.SourceID)
				if err != nil {
					t.Fatalf("Read(%q): %v", row.SourceID, err)
				}
				if chat.SourceID != row.SourceID {
					t.Fatalf("Read returned session %q, want %q", chat.SourceID, row.SourceID)
				}
				// The resolved file is opened twice: once by the identity
				// scan, once by the full read. Every other candidate is
				// opened at most once, so the double open names the file.
				rel := strings.TrimPrefix(row.RawPath, "/")
				if n := counts.Opens(rel); n != 2 {
					t.Fatalf("%q opened %d times, want 2 (lookup then read)", row.RawPath, n)
				}
				for _, p := range counts.OpenedPaths() {
					if p != rel && counts.Opens(p) > 1 {
						t.Fatalf("%q was read as well as scanned; lookup picked another file", p)
					}
				}
			}
			// The two-identity fixture's second identity belongs to no file.
			facts := corpus.WithKind(jsonltest.KindTwoIdentities)
			if len(facts) != 1 {
				t.Fatalf("expected one two-identity fixture, got %d", len(facts))
			}
			alt := facts[0].SessionID + "-b"
			if _, err := tc.reader(root).Read(context.Background(), nil, alt); err == nil {
				t.Fatalf("Read(%q) resolved a second identity discovery never reports", alt)
			}
		})
	}
}

// TestFindJSONLSession_notFound names the source and the identifier, so a
// failed continue says which tool was searched.
func TestFindJSONLSession_notFound(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl", stubLine{SessionID: "present", Role: "user", Text: "hi"}.String())
	for _, tc := range []struct {
		name, root string
		want       []string
	}{
		{"no such session", root, []string{"stub", "absent"}},
		// An unconfigured root is a failure too, not a silent empty answer.
		{"root not configured", "", []string{"stub", "not configured"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := vendors.FindJSONLSession(context.Background(), newStubSchema(tc.root), nil, nil, "absent")
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// TestFindJSONLSession_emptyIDRejected fails before the walk begins: an empty
// identifier must never match a file that has no identity either.
func TestFindJSONLSession_emptyIDRejected(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl", stubLine{Role: "user", Text: "no identity here"}.String())
	fsys, counts := jsonltest.CountingFS("/")
	s := newStubSchema(root)
	if _, err := vendors.FindJSONLSession(context.Background(), s, fsys, nil, ""); err == nil {
		t.Fatal("expected an error for an empty session id")
	}
	if counts.TotalOpens() != 0 || s.lineCount() != 0 {
		t.Fatalf("the walk started: %d opens, %d lines", counts.TotalOpens(), s.lineCount())
	}
}

// --- error coverage ---------------------------------------------------------

// failOpenFS fails to open one path and behaves normally for every other, so
// an unreadable file is simulated without depending on process privileges.
type failOpenFS struct {
	inner fs.FS
	fail  string
}

func (f failOpenFS) Open(name string) (fs.File, error) {
	if name == f.fail {
		return nil, fs.ErrPermission
	}
	return f.inner.Open(name)
}

func (f failOpenFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(f.inner, name) }

// TestDiscoverJSONL_unreadableFileSkipped keeps one bad file from hiding the
// whole source.
func TestDiscoverJSONL_unreadableFileSkipped(t *testing.T) {
	root := t.TempDir()
	line := func(id string) string { return stubLine{SessionID: id, Role: "user", Text: "hi " + id}.String() }
	writeStubSession(t, root, "a.jsonl", line("a"))
	bad := writeStubSession(t, root, "b.jsonl", line("b"))
	writeStubSession(t, root, "c.jsonl", line("c"))

	fsys := failOpenFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(bad, "/")}
	rows := discoverStub(t, newStubSchema(root), fsys)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (only the unreadable file skipped)", len(rows))
	}
	for _, row := range rows {
		if row.RawPath == bad {
			t.Fatalf("unreadable file yielded a row: %+v", row)
		}
	}
}

// TestDiscoverJSONL_malformedLineSkipped keeps scanning past a line that is
// not JSON at all.
func TestDiscoverJSONL_malformedLineSkipped(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl",
		`{"sid":"s1"`,
		"not json at all",
		"",
		stubLine{SessionID: "s1", Role: "user", Text: "after the rubble"}.String(),
	)
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))
	if row.SourceID != "s1" {
		t.Fatalf("SourceID = %q, want s1", row.SourceID)
	}
	if row.FullFirstUserMessage != "after the rubble" {
		t.Fatalf("FullFirstUserMessage = %q, want the line after the malformed ones", row.FullFirstUserMessage)
	}
	if row.MessageCount != 1 {
		t.Fatalf("MessageCount = %d, want 1: a malformed line was counted", row.MessageCount)
	}
}

// TestDiscoverJSONL_oversizedLineTruncatesScan stops at a line beyond the
// scanner token bound but still returns what the file already yielded.
func TestDiscoverJSONL_oversizedLineTruncatesScan(t *testing.T) {
	root := t.TempDir()
	huge := stubLine{SessionID: "s1", Role: "assistant", Text: strings.Repeat("x", vendors.ReadMaxToken+1)}.String()
	writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "before the giant"}.String(),
		huge,
		stubLine{SessionID: "s1", Role: "assistant", Model: "never-read"}.String(),
	)
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))
	if row.SourceID != "s1" || row.FullFirstUserMessage != "before the giant" {
		t.Fatalf("partial row lost its usable fields: %+v", row)
	}
	if row.MessageCount != 1 {
		t.Fatalf("MessageCount = %d, want 1: the scan continued past the oversized line", row.MessageCount)
	}
	if row.Model != "" {
		t.Fatalf("Model = %q, want empty: the line after the oversized one was read", row.Model)
	}
}

// TestDiscoverJSONL_noIdentityYieldsNoRow drops a file that names no session,
// without failing the walk.
func TestDiscoverJSONL_noIdentityYieldsNoRow(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "a.jsonl", stubLine{Role: "user", Text: "anonymous"}.String())
	writeStubSession(t, root, "b.jsonl", "")
	writeStubSession(t, root, "c.jsonl", stubLine{SessionID: "c", Role: "user", Text: "named"}.String())
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))
	if row.SourceID != "c" {
		t.Fatalf("SourceID = %q, want c", row.SourceID)
	}
}

// TestDiscoverJSONL_missingPreviewFallback uses the placeholder when no user
// line carried text.
func TestDiscoverJSONL_missingPreviewFallback(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user"}.String(),
		stubLine{SessionID: "s1", Role: "assistant", Text: "assistants never preview"}.String(),
	)
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))
	if row.FirstUserMessage != "(no preview)" {
		t.Fatalf("FirstUserMessage = %q, want the placeholder", row.FirstUserMessage)
	}
	if row.FullFirstUserMessage != "" {
		t.Fatalf("FullFirstUserMessage = %q, want empty", row.FullFirstUserMessage)
	}
	if row.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2: an unpreviewable user line still counts", row.MessageCount)
	}
}

// TestDiscoverJSONL_createdFallsBackToModTime uses the file's mod time when
// no line carried a usable timestamp.
func TestDiscoverJSONL_createdFallsBackToModTime(t *testing.T) {
	root := t.TempDir()
	path := writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "no timestamp"}.String(),
		stubLine{SessionID: "s1", Role: "assistant", Timestamp: "not a timestamp"}.String(),
	)
	want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	row := onlyRow(t, discoverStub(t, newStubSchema(root), nil))
	if !row.Created.Equal(want) {
		t.Fatalf("Created = %v, want the file mod time %v", row.Created, want)
	}
}

// TestDiscoverJSONL_contextCancelled stops the walk and surfaces the context
// error rather than a half-built list.
func TestDiscoverJSONL_contextCancelled(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl", stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newStubSchema(root)
	rows, err := vendors.DiscoverJSONL(ctx, s, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows = %+v, want nil on a cancelled walk", rows)
	}
	if s.lineCount() != 0 {
		t.Fatalf("the cancelled walk still scanned %d lines", s.lineCount())
	}
}

// --- cache seam -------------------------------------------------------------

// memCache is a SourceCache with the same validation rule the foreign index
// uses: a row is current while the (size, mtime) pair it was stored under
// still holds. It exists because internal/chat, which owns the real
// implementation, imports this package and so cannot be imported back.
type memCache struct {
	mu      sync.Mutex
	rows    map[string]memRow
	lookups int
	stores  int
}

type memRow struct {
	size    int64
	modTime time.Time
	row     vendors.SourceRow
}

func newMemCache() *memCache { return &memCache{rows: map[string]memRow{}} }

func (c *memCache) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookups++
	r, ok := c.rows[absPath]
	if !ok || info == nil || r.size != info.Size() || !r.modTime.Equal(info.ModTime()) {
		return vendors.SourceRow{}, false
	}
	return r.row, true
}

func (c *memCache) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	if info == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stores++
	c.rows[absPath] = memRow{size: info.Size(), modTime: info.ModTime(), row: row}
}

func (c *memCache) Locate(source, sourceID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, r := range c.rows {
		if r.row.Source == source && r.row.SourceID == sourceID {
			return p, true
		}
	}
	return "", false
}

func (c *memCache) storedSize(absPath string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rows[absPath].size
}

// TestDiscoverJSONL_cacheHitOpensNothing is the invariant the whole worklog
// exists for: a corpus whose every file still matches its cached (size,
// mtime) pair is stat'ed once per file and opened not at all.
func TestDiscoverJSONL_cacheHitOpensNothing(t *testing.T) {
	root := t.TempDir()
	corpus := jsonltest.WriteCorpus(t, root, corpusOptions(jsonltest.ShapeClaude))
	reader := anthropic.SourceReader{Root: root}
	cache := newMemCache()

	warm, err := reader.Discover(context.Background(), cache)
	if err != nil {
		t.Fatalf("warming Discover: %v", err)
	}
	if len(warm) == 0 {
		t.Fatal("no rows; the corpus did not reach the reader")
	}

	fsys, counts := jsonltest.CountingFS("/")
	cached := anthropic.SourceReader{Root: root, FS: fsys}
	got, err := cached.Discover(context.Background(), cache)
	if err != nil {
		t.Fatalf("cached Discover: %v", err)
	}
	if !reflect.DeepEqual(got, warm) {
		t.Fatalf("cached rows differ from scanned rows:\n got %+v\nwant %+v", got, warm)
	}
	if n := counts.TotalOpens(); n != 0 {
		t.Fatalf("cache hit opened %d files, want 0: %v", n, counts.OpenedPaths())
	}
	if n, want := counts.TotalStats(), len(corpus.Files); n != want {
		t.Fatalf("cache hit stat'ed %d times, want one per file (%d)", n, want)
	}
}

// TestDiscoverJSONL_cacheAgnosticResults proves the index is derived: the
// rows are the same with no cache, with a cold cache and with a warm one, so
// deleting foreign_index.cache can only cost time.
func TestDiscoverJSONL_cacheAgnosticResults(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			reader := tc.reader(root)

			nilCache, err := reader.Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("nil-cache Discover: %v", err)
			}
			cache := newMemCache()
			cold, err := reader.Discover(context.Background(), cache)
			if err != nil {
				t.Fatalf("cold-cache Discover: %v", err)
			}
			warm, err := reader.Discover(context.Background(), cache)
			if err != nil {
				t.Fatalf("warm-cache Discover: %v", err)
			}
			if !reflect.DeepEqual(cold, nilCache) {
				t.Fatalf("cold cache changed the rows:\n got %+v\nwant %+v", cold, nilCache)
			}
			if !reflect.DeepEqual(warm, nilCache) {
				t.Fatalf("warm cache changed the rows:\n got %+v\nwant %+v", warm, nilCache)
			}
		})
	}
}

// TestDiscoverJSONL_unusableFileCachedNegatively stores the zero row for a
// file that names no session, so it is not reopened on every later listing.
func TestDiscoverJSONL_unusableFileCachedNegatively(t *testing.T) {
	root := t.TempDir()
	anon := writeStubSession(t, root, "anon.jsonl", stubLine{Role: "user", Text: "anonymous"}.String())
	named := writeStubSession(t, root, "named.jsonl", stubLine{SessionID: "n", Role: "user", Text: "named"}.String())
	cache := newMemCache()

	first := discoverStubCached(t, newStubSchema(root), nil, cache)
	if len(first) != 1 || first[0].RawPath != named {
		t.Fatalf("first pass rows = %+v, want only %q", first, named)
	}
	if _, ok := cache.rows[anon]; !ok {
		t.Fatalf("the identity-less file was not cached; it would be rescanned forever")
	}

	fsys, counts := jsonltest.CountingFS("/")
	second := discoverStubCached(t, newStubSchema(root), fsys, cache)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second pass rows = %+v, want %+v", second, first)
	}
	if n := counts.Opens(strings.TrimPrefix(anon, "/")); n != 0 {
		t.Fatalf("the negatively cached file was opened %d times, want 0", n)
	}
}

// TestDiscoverJSONL_fileGrowingDuringScanIsNotCachedAsCurrent keys the row on
// the stat taken before the read (D15). A file appended to while it is being
// scanned is therefore cached under the state that was actually read, and the
// next run rescans it instead of trusting a count for bytes it never saw.
func TestDiscoverJSONL_fileGrowingDuringScanIsNotCachedAsCurrent(t *testing.T) {
	root := t.TempDir()
	path := writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "first"}.String(),
	)
	appended := stubLine{SessionID: "s1", Role: "assistant", Text: "grew"}.String()

	s := newStubSchema(root)
	var once sync.Once
	s.onLine = func([]byte) {
		once.Do(func() {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Errorf("append to %q: %v", path, err)
				return
			}
			defer f.Close()
			if _, err := f.WriteString(appended + "\n"); err != nil {
				t.Errorf("append to %q: %v", path, err)
			}
		})
	}
	cache := newMemCache()
	first := discoverStubCached(t, s, nil, cache)
	if len(first) != 1 {
		t.Fatalf("first pass rows = %+v, want 1", first)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := cache.storedSize(path); got >= info.Size() {
		t.Fatalf("cached size %d is not below the grown file size %d: the row was keyed by a stat taken after the read", got, info.Size())
	}

	fsys, counts := jsonltest.CountingFS("/")
	second := discoverStubCached(t, newStubSchema(root), fsys, cache)
	if n := counts.Opens(strings.TrimPrefix(path, "/")); n != 1 {
		t.Fatalf("the grown file was opened %d times, want 1 (a stale row was trusted)", n)
	}
	if len(second) != 1 || second[0].MessageCount != 2 {
		t.Fatalf("second pass row = %+v, want the appended line counted", second)
	}
}

// TestFindJSONLSession_usesCacheWithoutWalking answers the lookup from the
// index: the cached path is confirmed with one stat and returned, with no
// file opened and no walk consulted. The schema's root is emptied first, so
// a walk could not produce the answer at all.
func TestFindJSONLSession_usesCacheWithoutWalking(t *testing.T) {
	root := t.TempDir()
	path := writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String(),
	)
	cache := newMemCache()
	if rows := discoverStubCached(t, newStubSchema(root), nil, cache); len(rows) != 1 {
		t.Fatalf("warming discovery rows = %+v, want 1", rows)
	}

	fsys, counts := jsonltest.CountingFS("/")
	empty := newStubSchema(t.TempDir())
	got, err := vendors.FindJSONLSession(context.Background(), empty, fsys, cache, "s1")
	if err != nil {
		t.Fatalf("FindJSONLSession: %v", err)
	}
	if got != path {
		t.Fatalf("got %q, want %q", got, path)
	}
	if n := counts.TotalOpens(); n != 0 {
		t.Fatalf("lookup opened %d files, want 0: %v", n, counts.OpenedPaths())
	}
	if n := counts.TotalStats(); n != 1 {
		t.Fatalf("lookup stat'ed %d times, want exactly one confirming stat", n)
	}
	if n := empty.lineCount(); n != 0 {
		t.Fatalf("lookup asked the schema about %d lines, want 0 (it walked)", n)
	}
}

// TestFindJSONLSession_staleCachedPathFallsBack rejects a cached path whose
// bytes moved, and whose file vanished, and finds the session by walking.
func TestFindJSONLSession_staleCachedPathFallsBack(t *testing.T) {
	t.Run("changed file", func(t *testing.T) {
		root := t.TempDir()
		path := writeStubSession(t, root, "s.jsonl",
			stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String(),
		)
		cache := newMemCache()
		discoverStubCached(t, newStubSchema(root), nil, cache)

		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := f.WriteString(stubLine{SessionID: "s1", Role: "assistant", Text: "more"}.String() + "\n"); err != nil {
			t.Fatalf("append: %v", err)
		}
		f.Close()

		s := newStubSchema(root)
		got, err := vendors.FindJSONLSession(context.Background(), s, nil, cache, "s1")
		if err != nil {
			t.Fatalf("FindJSONLSession: %v", err)
		}
		if got != path {
			t.Fatalf("got %q, want %q", got, path)
		}
		if s.lineCount() == 0 {
			t.Fatal("the stale cached path was trusted: no line was read")
		}
	})

	t.Run("vanished file", func(t *testing.T) {
		root := t.TempDir()
		gone := writeStubSession(t, root, "gone.jsonl",
			stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String(),
		)
		cache := newMemCache()
		discoverStubCached(t, newStubSchema(root), nil, cache)
		if err := os.Remove(gone); err != nil {
			t.Fatalf("remove: %v", err)
		}
		moved := writeStubSession(t, root, "moved.jsonl",
			stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String(),
		)

		got, err := vendors.FindJSONLSession(context.Background(), newStubSchema(root), nil, cache, "s1")
		if err != nil {
			t.Fatalf("FindJSONLSession: %v", err)
		}
		if got != moved {
			t.Fatalf("got %q, want the walked path %q", got, moved)
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		root := t.TempDir()
		writeStubSession(t, root, "s.jsonl", stubLine{SessionID: "s1", Role: "user", Text: "hi"}.String())
		cache := newMemCache()
		discoverStubCached(t, newStubSchema(root), nil, cache)
		if _, err := vendors.FindJSONLSession(context.Background(), newStubSchema(root), nil, cache, "absent"); err == nil {
			t.Fatal("expected an error for a session no cache row names")
		}
	})
}

// --- exact counting ---------------------------------------------------------

// deletedLineCap is the value the discovery line cap held before this phase
// deleted it. It survives only as the number a fixture must exceed for
// "exact" to mean anything; no production code knows it any more.
const deletedLineCap = 200

// TestDiscoverJSONL_countsMatchCorpusFactsExactly drives the real readers over
// a corpus whose sessions are longer than the deleted cap and checks every
// MessageCount against the oracle. Under the cap the long-session fixture
// undercounted; reading to EOF is what makes the field true.
func TestDiscoverJSONL_countsMatchCorpusFactsExactly(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			corpus := jsonltest.WriteCorpus(t, root, jsonltest.CorpusOptions{
				Shape:           tc.shape,
				Sessions:        jsonltest.MinSessions + 2,
				LinesPerSession: jsonltest.DefaultLinesPerSession,
			})
			rows, err := tc.reader(root).Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := keptFacts(corpus)
			if len(rows) != len(want) {
				t.Fatalf("got %d rows, want %d", len(rows), len(want))
			}

			beyondCap := 0
			for i, row := range rows {
				fact := want[i]
				if row.MessageCount != fact.Messages {
					t.Errorf("%v %q: MessageCount = %d, want the corpus fact %d",
						fact.Kind, fact.Path, row.MessageCount, fact.Messages)
				}
				if fact.Messages > deletedLineCap {
					beyondCap++
				}
			}
			if beyondCap == 0 {
				t.Fatalf("no fixture holds more than %d messages; the corpus cannot show the cap is gone", deletedLineCap)
			}

			// The long-session fixture is the one the cap truncated: a capped
			// scan could never have reported its whole message count.
			long := corpus.WithKind(jsonltest.KindLongSession)
			if len(long) != 1 {
				t.Fatalf("expected one long-session fixture, got %d", len(long))
			}
			row, ok := rowForPath(rows, long[0].Path)
			if !ok {
				t.Fatalf("no row for the long-session fixture %q", long[0].Path)
			}
			if row.MessageCount <= deletedLineCap {
				t.Fatalf("long-session MessageCount = %d, want more than the deleted cap %d", row.MessageCount, deletedLineCap)
			}

			// The quoted-role-marker fixture is the reason the count comes
			// from a decode and not from counting bytes: its body contains a
			// whole user line verbatim, which any substring counter — or an
			// over-eager prefilter turned counter — reports twice.
			quoted := corpus.WithKind(jsonltest.KindQuotedRoleMarker)
			if len(quoted) != 1 {
				t.Fatalf("expected one quoted-role-marker fixture, got %d", len(quoted))
			}
			row, ok = rowForPath(rows, quoted[0].Path)
			if !ok {
				t.Fatalf("no row for the quoted-role-marker fixture %q", quoted[0].Path)
			}
			if row.MessageCount != quoted[0].Messages {
				t.Fatalf("quoted-role-marker MessageCount = %d, want %d: the quoted marker was counted",
					row.MessageCount, quoted[0].Messages)
			}
		})
	}
}

// --- parallel discovery -----------------------------------------------------

// withDiscoverWorkers pins the pool bound for the duration of one test.
func withDiscoverWorkers(t *testing.T, n int) {
	t.Helper()
	t.Cleanup(vendors.SetDiscoverWorkers(n))
}

// TestDiscoverJSONL_parallelMatchesSerial holds the parallel result to the
// serial one field for field, not row for row: the walk fixes the order and
// workers fill indexed slots, so widening the pool may change nothing at all.
func TestDiscoverJSONL_parallelMatchesSerial(t *testing.T) {
	for _, tc := range vendorCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jsonltest.WriteCorpus(t, root, corpusOptions(tc.shape))
			reader := tc.reader(root)

			discover := func(workers int) []vendors.SourceRow {
				restore := vendors.SetDiscoverWorkers(workers)
				defer restore()
				rows, err := reader.Discover(context.Background(), nil)
				if err != nil {
					t.Fatalf("%d workers: Discover: %v", workers, err)
				}
				return rows
			}
			serial := discover(1)
			if len(serial) == 0 {
				t.Fatal("no rows; the corpus did not reach the reader")
			}
			for _, workers := range []int{2, 8, 64} {
				got := discover(workers)
				if !reflect.DeepEqual(got, serial) {
					t.Fatalf("%d workers changed the result:\n got %+v\nwant %+v", workers, got, serial)
				}
			}
		})
	}
}

// concurrencyProbe records how many Fields calls ran at once, and holds the
// first width arrivals until all of them are present so the peak is observed
// rather than raced for. A serial implementation never fills the barrier: it
// releases on the timeout and records a peak of one, which is the failure.
type concurrencyProbe struct {
	mu      sync.Mutex
	active  int
	peak    int
	arrived int
	width   int
	full    chan struct{}
	closed  bool
}

// probeTimeout is the barrier's escape hatch: long enough that a correct
// pool always fills it first, short enough that a serial one fails fast.
const probeTimeout = 2 * time.Second

func newConcurrencyProbe(width int) *concurrencyProbe {
	return &concurrencyProbe{width: width, full: make(chan struct{})}
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	p.active++
	if p.active > p.peak {
		p.peak = p.active
	}
	p.arrived++
	if p.arrived >= p.width {
		p.releaseLocked()
	}
	full, closed := p.full, p.closed
	p.mu.Unlock()
	if closed {
		return
	}
	select {
	case <-full:
	case <-time.After(probeTimeout):
		p.mu.Lock()
		p.releaseLocked()
		p.mu.Unlock()
	}
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

func (p *concurrencyProbe) peakValue() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

func (p *concurrencyProbe) releaseLocked() {
	if !p.closed {
		p.closed = true
		close(p.full)
	}
}

// writeStubCorpus writes n single-line stub sessions and returns their paths.
func writeStubCorpus(t *testing.T, root string, n int) []string {
	t.Helper()
	paths := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("s%02d", i)
		paths = append(paths, writeStubSession(t, root, id+".jsonl",
			stubLine{SessionID: id, Role: "user", Text: "hi " + id}.String()))
	}
	return paths
}

// TestDiscoverJSONL_workerPoolIsBounded executes the limit table: the peak
// number of concurrent scans equals the injected bound and never exceeds it.
func TestDiscoverJSONL_workerPoolIsBounded(t *testing.T) {
	for _, bound := range []int{2, 4} {
		t.Run(fmt.Sprintf("%d workers", bound), func(t *testing.T) {
			root := t.TempDir()
			files := writeStubCorpus(t, root, bound*3)
			withDiscoverWorkers(t, bound)

			s := newStubSchema(root)
			s.probe = newConcurrencyProbe(bound)
			rows := discoverStub(t, s, nil)

			if len(rows) != len(files) {
				t.Fatalf("got %d rows, want %d", len(rows), len(files))
			}
			if got := s.probe.peakValue(); got != bound {
				t.Fatalf("peak concurrent scans = %d, want exactly the injected bound %d", got, bound)
			}
		})
	}
}

// TestDiscoverJSONL_degenerateWorkerCount covers the limit table's degenerate
// row: a bound of zero or below falls back to one worker instead of
// deadlocking or dropping every file.
func TestDiscoverJSONL_degenerateWorkerCount(t *testing.T) {
	for _, bound := range []int{0, -1, -1000} {
		t.Run(fmt.Sprintf("bound %d", bound), func(t *testing.T) {
			root := t.TempDir()
			files := writeStubCorpus(t, root, 5)
			withDiscoverWorkers(t, bound)

			s := newStubSchema(root)
			s.probe = newConcurrencyProbe(1)
			rows := discoverStub(t, s, nil)
			if len(rows) != len(files) {
				t.Fatalf("got %d rows, want %d", len(rows), len(files))
			}
			if got := s.probe.peakValue(); got != 1 {
				t.Fatalf("peak concurrent scans = %d, want the single-worker fallback", got)
			}
		})
	}
}

// TestDiscoverJSONL_returnsAfterWorkersFinish makes the WaitGroup observable
// without a leak detector (D22). The lexically first file releases the last
// one, so the last file is provably still being scanned when the first
// finishes: a DiscoverJSONL that returned before Wait would lose its row and
// its unread lines.
func TestDiscoverJSONL_returnsAfterWorkersFinish(t *testing.T) {
	root := t.TempDir()
	const perFile = 6
	first := make([]string, 0, perFile)
	last := make([]string, 0, perFile)
	for i := range perFile {
		first = append(first, stubLine{SessionID: "a", Role: "user", Text: fmt.Sprintf("a-%d", i)}.String())
		last = append(last, stubLine{SessionID: "z", Role: "user", Text: fmt.Sprintf("z-%d", i)}.String())
	}
	writeStubSession(t, root, "a.jsonl", first...)
	writeStubSession(t, root, "z.jsonl", last...)
	withDiscoverWorkers(t, 2)

	released := make(chan struct{})
	var once sync.Once
	s := newStubSchema(root)
	s.onLine = func(line []byte) {
		switch {
		case bytes.Contains(line, fmt.Appendf(nil, "a-%d", perFile-1)):
			once.Do(func() { close(released) })
		case bytes.Contains(line, []byte("z-0")):
			select {
			case <-released:
			case <-time.After(probeTimeout):
				t.Error("the first file never finished while the last one waited")
			}
		}
	}

	rows := discoverStub(t, s, nil)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: a worker was abandoned", len(rows))
	}
	if n := s.lineCount(); n != 2*perFile {
		t.Fatalf("the schema saw %d lines, want %d: discovery returned before its workers finished", n, 2*perFile)
	}
	for _, row := range rows {
		if row.MessageCount != perFile {
			t.Fatalf("%q: MessageCount = %d, want %d", row.RawPath, row.MessageCount, perFile)
		}
	}
}

// --- prefilter --------------------------------------------------------------

// prefilteredStub is a stubSchema that also implements vendors.LinePrefilter,
// rejecting every line without the marker. It exists so the generic layer's
// use of the optional interface is observable without a vendor.
type prefilteredStub struct {
	*stubSchema
	marker string
}

func (p prefilteredStub) MayContribute(line []byte) bool {
	return bytes.Contains(line, []byte(p.marker))
}

// TestDiscoverJSONL_schemaWithoutPrefilter holds both directions of D18: a
// schema that does not implement LinePrefilter is decoded line by line and is
// correct, and a schema that does implement it skips lines without ever
// changing the result.
func TestDiscoverJSONL_schemaWithoutPrefilter(t *testing.T) {
	root := t.TempDir()
	writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "keep me"}.String(),
		`{"noise":"nothing this schema reads"}`,
		`{"noise":"nor this"}`,
		stubLine{SessionID: "s1", Role: "assistant", Model: "m1"}.String(),
	)

	plain := newStubSchema(root)
	if _, ok := any(plain).(vendors.LinePrefilter); ok {
		t.Fatal("the plain stub implements LinePrefilter; it is the not-implemented case")
	}
	plainRows := discoverStub(t, plain, nil)
	if plain.lineCount() != 4 {
		t.Fatalf("a schema without a prefilter was asked about %d lines, want every one of 4", plain.lineCount())
	}

	inner := newStubSchema(root)
	filtered := prefilteredStub{stubSchema: inner, marker: `"sid"`}
	rows, err := vendors.DiscoverJSONL(context.Background(), filtered, nil, nil)
	if err != nil {
		t.Fatalf("DiscoverJSONL: %v", err)
	}
	if !reflect.DeepEqual(rows, plainRows) {
		t.Fatalf("the prefilter changed the result:\n got %+v\nwant %+v", rows, plainRows)
	}
	if got := inner.lineCount(); got != 2 {
		t.Fatalf("the prefiltered schema decoded %d lines, want the 2 that pass the filter", got)
	}
}

// --- parallel error isolation -----------------------------------------------

// TestDiscoverJSONL_parallelUnreadableFileIsolated keeps one worker's failure
// inside that worker's slot: every other row survives, in order.
func TestDiscoverJSONL_parallelUnreadableFileIsolated(t *testing.T) {
	root := t.TempDir()
	files := writeStubCorpus(t, root, 8)
	bad := files[3]
	withDiscoverWorkers(t, 4)

	fsys := failOpenFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(bad, "/")}
	rows := discoverStub(t, newStubSchema(root), fsys)
	if len(rows) != len(files)-1 {
		t.Fatalf("got %d rows, want %d (only the unreadable file dropped)", len(rows), len(files)-1)
	}
	want := slices.Concat(files[:3], files[4:])
	for i, row := range rows {
		if row.RawPath != want[i] {
			t.Fatalf("row %d is %q, want %q: the compaction lost the walk order", i, row.RawPath, want[i])
		}
	}
}

// TestDiscoverJSONL_parallelOversizedLineIsolated truncates one worker's scan
// at the token bound and leaves every other worker's row whole.
func TestDiscoverJSONL_parallelOversizedLineIsolated(t *testing.T) {
	root := t.TempDir()
	files := writeStubCorpus(t, root, 8)
	huge := writeStubSession(t, root, "zz-huge.jsonl",
		stubLine{SessionID: "huge", Role: "user", Text: "before the giant"}.String(),
		stubLine{SessionID: "huge", Role: "assistant", Text: strings.Repeat("x", vendors.ReadMaxToken+1)}.String(),
		stubLine{SessionID: "huge", Role: "assistant", Model: "never-read"}.String(),
	)
	withDiscoverWorkers(t, 4)

	rows := discoverStub(t, newStubSchema(root), nil)
	if len(rows) != len(files)+1 {
		t.Fatalf("got %d rows, want %d", len(rows), len(files)+1)
	}
	for _, row := range rows {
		if row.RawPath == huge {
			if row.MessageCount != 1 || row.Model != "" {
				t.Fatalf("the oversized file's row is not the truncated one: %+v", row)
			}
			continue
		}
		if row.MessageCount != 1 || row.FullFirstUserMessage == "" {
			t.Fatalf("%q lost its fields to another worker's truncated scan: %+v", row.RawPath, row)
		}
	}
}

// TestDiscoverJSONL_cancelledBeforeStart surfaces the context error and opens
// nothing at all: cancellation is checked before the first worker is fed.
func TestDiscoverJSONL_cancelledBeforeStart(t *testing.T) {
	root := t.TempDir()
	writeStubCorpus(t, root, 8)
	withDiscoverWorkers(t, 4)

	fsys, counts := jsonltest.CountingFS("/")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newStubSchema(root)
	rows, err := vendors.DiscoverJSONL(ctx, s, fsys, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows = %+v, want nil", rows)
	}
	if n := counts.TotalOpens(); n != 0 {
		t.Fatalf("a cancelled discovery opened %d files: %v", n, counts.OpenedPaths())
	}
	if n := s.lineCount(); n != 0 {
		t.Fatalf("a cancelled discovery scanned %d lines", n)
	}
}

// TestDiscoverJSONL_cancelledWhileWorkersRun stops mid-flight: the context
// error is returned rather than a partial list, and the remaining files are
// never scanned.
func TestDiscoverJSONL_cancelledWhileWorkersRun(t *testing.T) {
	root := t.TempDir()
	files := writeStubCorpus(t, root, 32)
	withDiscoverWorkers(t, 2)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := newStubSchema(root)
	var once sync.Once
	s.onLine = func([]byte) { once.Do(cancel) }

	rows, err := vendors.DiscoverJSONL(ctx, s, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows = %+v, want nil", rows)
	}
	if n := s.lineCount(); n >= len(files) {
		t.Fatalf("every one of %d files was scanned after cancellation (%d lines)", len(files), n)
	}
}

// --- an error is not a fact about the file (phase 5) -------------------------

// stubSessions writes one single-line session per id and returns the paths in
// the order they were named.
func stubSessions(t *testing.T, root string, ids ...string) []string {
	t.Helper()
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		paths = append(paths, writeStubSession(t, root, id+".jsonl",
			stubLine{SessionID: id, Role: "user", Text: "hi " + id}.String()))
	}
	return paths
}

// TestDiscoverJSONL_openFailureIsNotCached: a refused open is a fact about
// this run, not about the file. Nothing may be written for it, because the
// (size, mtime) pair a negative row would be keyed by is unchanged by the
// refusal, so the row could never be invalidated and the conversation would
// leave the listing permanently (R1-01).
func TestDiscoverJSONL_openFailureIsNotCached(t *testing.T) {
	root := t.TempDir()
	paths := stubSessions(t, root, "a", "b")
	bad := paths[1]
	cache := newMemCache()

	blocked := failOpenFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(bad, "/")}
	first := discoverStubCached(t, newStubSchema(root), blocked, cache)
	if len(first) != 1 || first[0].RawPath != paths[0] {
		t.Fatalf("first pass rows = %+v, want only %q", first, paths[0])
	}
	if _, ok := cache.rows[bad]; ok {
		t.Fatalf("the unreadable file was cached as %+v; its size and mtime never change, so the row could never be invalidated", cache.rows[bad])
	}

	// The file becomes readable again with its size and mod time untouched,
	// which is exactly what a mode change or a transient denial leaves behind.
	fsys, counts := jsonltest.CountingFS("/")
	second := discoverStubCached(t, newStubSchema(root), fsys, cache)
	if len(second) != 2 {
		t.Fatalf("second pass rows = %+v, want both sessions back", second)
	}
	if n := counts.Opens(strings.TrimPrefix(bad, "/")); n != 1 {
		t.Fatalf("the previously unreadable file was opened %d times, want 1 retry", n)
	}
}

// TestDiscoverJSONL_negativeCachingSurvivesTheSplit: splitting the open
// failure out of the empty result must not take the negative cache with it.
// A scan that reached EOF and found no identity is a fact about the content
// and is still stored, or every identity-less file is rescanned forever.
func TestDiscoverJSONL_negativeCachingSurvivesTheSplit(t *testing.T) {
	root := t.TempDir()
	anon := writeStubSession(t, root, "anon.jsonl", stubLine{Role: "user", Text: "anonymous"}.String())
	cache := newMemCache()

	if rows := discoverStubCached(t, newStubSchema(root), nil, cache); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none: the file names no session", rows)
	}
	stored, ok := cache.rows[anon]
	if !ok {
		t.Fatal("the completed empty scan was not cached; it would be rescanned on every listing")
	}
	if stored.row.SourceID != "" {
		t.Fatalf("the cached row is not the zero row: %+v", stored.row)
	}

	fsys, counts := jsonltest.CountingFS("/")
	if rows := discoverStubCached(t, newStubSchema(root), fsys, cache); len(rows) != 0 {
		t.Fatalf("second pass rows = %+v, want none", rows)
	}
	if n := counts.Opens(strings.TrimPrefix(anon, "/")); n != 0 {
		t.Fatalf("the negatively cached file was opened %d times, want 0", n)
	}
}

// TestDiscoverJSONL_cacheAgnosticWithUnreadableFile is the blocker's own
// reproduction: over a corpus that held an unreadable file, the cached and
// the cache-less answers must still agree once the file is readable again.
// Deleting foreign_index.cache may cost time and may never change a row.
func TestDiscoverJSONL_cacheAgnosticWithUnreadableFile(t *testing.T) {
	root := t.TempDir()
	paths := stubSessions(t, root, "a", "b", "c")
	bad := strings.TrimPrefix(paths[1], "/")
	blocked := failOpenFS{inner: os.DirFS("/"), fail: bad}

	nilCache := discoverStubCached(t, newStubSchema(root), blocked, nil)
	cache := newMemCache()
	cold := discoverStubCached(t, newStubSchema(root), blocked, cache)
	warm := discoverStubCached(t, newStubSchema(root), blocked, cache)
	if len(nilCache) != 2 {
		t.Fatalf("nil-cache rows = %+v, want the two readable sessions", nilCache)
	}
	if !reflect.DeepEqual(cold, nilCache) {
		t.Fatalf("cold cache changed the rows:\n got %+v\nwant %+v", cold, nilCache)
	}
	if !reflect.DeepEqual(warm, nilCache) {
		t.Fatalf("warm cache changed the rows:\n got %+v\nwant %+v", warm, nilCache)
	}

	// Readable again, with neither size nor mod time touched.
	cached := discoverStubCached(t, newStubSchema(root), nil, cache)
	uncached := discoverStubCached(t, newStubSchema(root), nil, nil)
	if len(uncached) != len(paths) {
		t.Fatalf("uncached rows = %+v, want one per file", uncached)
	}
	if !reflect.DeepEqual(cached, uncached) {
		t.Fatalf("the cache changed the rows after an unreadable run:\n got %+v\nwant %+v", cached, uncached)
	}
}

// TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached executes the
// cancellation rule the phase-three contract left unstated (R1-51): a
// cancelled discovery returns no rows, but the files a worker finished
// before the cancel were committed through Store under their own pre-read
// stat, so they are complete rows and valid hits on the next run.
func TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached(t *testing.T) {
	root := t.TempDir()
	files := writeStubCorpus(t, root, 8)
	withDiscoverWorkers(t, 1)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := newStubSchema(root)
	var once sync.Once
	s.onLine = func([]byte) { once.Do(cancel) }

	cache := newMemCache()
	rows, err := vendors.DiscoverJSONL(ctx, s, nil, cache)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows = %+v, want nil on a cancelled discovery", rows)
	}
	if cache.stores == 0 {
		t.Fatal("no row survived the cancellation; the rule this test states is not reachable")
	}
	if cache.stores >= len(files) {
		t.Fatalf("every one of %d files was stored, so nothing was cancelled", len(files))
	}

	// Every stored row is a complete scan of its file, not a partial one.
	full := discoverStubCached(t, newStubSchema(root), nil, nil)
	byPath := map[string]vendors.SourceRow{}
	for _, row := range full {
		byPath[row.RawPath] = row
	}
	for p, stored := range cache.rows {
		if !reflect.DeepEqual(stored.row, byPath[p]) {
			t.Fatalf("%q was cached from a partial scan:\n got %+v\nwant %+v", p, stored.row, byPath[p])
		}
	}

	// And they are valid hits: the next, uncancelled run agrees with a
	// cache-less one field for field.
	resumed := discoverStubCached(t, newStubSchema(root), nil, cache)
	if !reflect.DeepEqual(resumed, full) {
		t.Fatalf("the rows committed before the cancel were not valid hits:\n got %+v\nwant %+v", resumed, full)
	}
}

// --- review two: a read failure is not a fact about the file (R2-01) --------

// errWireDown stands for the failure class D28 excludes from the cache: the
// open was served from the dentry cache and the read went to the wire.
var errWireDown = errors.New("read: transport endpoint is not connected")

// failReadFS opens every file normally and lets one path's reads deliver
// after bytes before failing. It is the shape phase five's own motivation
// described and did not cover: Open succeeds, Read does not.
type failReadFS struct {
	inner fs.FS
	fail  string
	after int
}

func (f failReadFS) Open(name string) (fs.File, error) {
	inner, err := f.inner.Open(name)
	if err != nil {
		return nil, err
	}
	if name != f.fail {
		return inner, nil
	}
	return &failReadFile{File: inner, budget: f.after}, nil
}

func (f failReadFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(f.inner, name) }

type failReadFile struct {
	fs.File
	budget int
}

func (f *failReadFile) Read(p []byte) (int, error) {
	if f.budget <= 0 {
		return 0, errWireDown
	}
	if len(p) > f.budget {
		p = p[:f.budget]
	}
	n, err := f.File.Read(p)
	f.budget -= n
	return n, err
}

// stubSessionOfLength writes one session of count user lines and returns its
// absolute path together with the byte offset that ends its first n lines.
func stubSessionOfLength(t *testing.T, root, id string, count int) string {
	t.Helper()
	lines := make([]string, 0, count)
	for i := range count {
		lines = append(lines, stubLine{SessionID: id, Role: "user", Text: fmt.Sprintf("%v line %d", id, i)}.String())
	}
	return writeStubSession(t, root, id+".jsonl", lines...)
}

// bytesOfFirstLines returns the byte length of the first n lines of path,
// terminators included, so a read budget lands on a line boundary.
func bytesOfFirstLines(t *testing.T, path string, n int) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	total := 0
	for range n {
		i := bytes.IndexByte(b[total:], '\n')
		if i < 0 {
			t.Fatalf("%q holds fewer than %d lines", path, n)
		}
		total += i + 1
	}
	return total
}

// TestDiscoverJSONL_readFailureIsNotCached: a scan that could not be read to
// its end is a fact about this run, exactly as a refused open is. Nothing may
// be written for it: the (size, mtime) pair a row would be keyed by is
// unchanged by the failure, so the row could never be invalidated and the
// conversation would leave the listing permanently (R2-01, D28).
func TestDiscoverJSONL_readFailureIsNotCached(t *testing.T) {
	root := t.TempDir()
	paths := stubSessions(t, root, "a", "b", "c")
	bad := paths[1]
	cache := newMemCache()

	// Nothing is delivered at all, so the scan sees no identity line: the
	// branch that used to cache the zero row and drop the file forever.
	broken := failReadFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(bad, "/")}
	first := discoverStubCached(t, newStubSchema(root), broken, cache)
	if len(first) != 2 {
		t.Fatalf("first pass rows = %+v, want the two readable sessions", first)
	}
	if row, ok := cache.rows[bad]; ok {
		t.Fatalf("the read-failing file was cached as %+v; its size and mtime never change, so the row could never be invalidated", row)
	}

	// The wire comes back with neither size nor mod time touched.
	fsys, counts := jsonltest.CountingFS("/")
	second := discoverStubCached(t, newStubSchema(root), fsys, cache)
	if len(second) != 3 {
		t.Fatalf("second pass rows = %+v, want all three sessions back", second)
	}
	if n := counts.Opens(strings.TrimPrefix(bad, "/")); n != 1 {
		t.Fatalf("the previously read-failing file was opened %d times, want 1 retry", n)
	}

	t.Run("a refused open is still not cached", func(t *testing.T) {
		root := t.TempDir()
		paths := stubSessions(t, root, "d", "e")
		bad := paths[1]
		cache := newMemCache()
		blocked := failOpenFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(bad, "/")}
		if rows := discoverStubCached(t, newStubSchema(root), blocked, cache); len(rows) != 1 {
			t.Fatalf("rows = %+v, want only the readable session", rows)
		}
		if row, ok := cache.rows[bad]; ok {
			t.Fatalf("the unopenable file was cached as %+v", row)
		}
	})
}

// TestDiscoverJSONL_partialReadNeverPersistsACount: a session read-failing
// partway through must not leave its partial count behind. Unlike the
// oversized-line bound the count is not determined by the bytes — two runs
// can see two different prefixes — so persisting it reintroduces through the
// failure branch the undercount D1 exists to remove.
func TestDiscoverJSONL_partialReadNeverPersistsACount(t *testing.T) {
	root := t.TempDir()
	const messages = 40
	full := stubSessionOfLength(t, root, "long", messages)
	cache := newMemCache()

	broken := failReadFS{
		inner: os.DirFS("/"),
		fail:  strings.TrimPrefix(full, "/"),
		after: bytesOfFirstLines(t, full, 2),
	}
	rows := discoverStubCached(t, newStubSchema(root), broken, cache)
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want none: the file was never read to its end", rows)
	}
	if row, ok := cache.rows[full]; ok {
		t.Fatalf("a partial read persisted %+v; MessageCount %d is not a fact about the file", row.row, row.row.MessageCount)
	}

	rescan := onlyRow(t, discoverStubCached(t, newStubSchema(root), nil, cache))
	if rescan.MessageCount != messages {
		t.Fatalf("MessageCount = %d, want the true %d", rescan.MessageCount, messages)
	}

	t.Run("a completed scan with no identity is still cached", func(t *testing.T) {
		root := t.TempDir()
		anon := writeStubSession(t, root, "anon.jsonl", stubLine{Role: "user", Text: "anonymous"}.String())
		cache := newMemCache()
		if rows := discoverStubCached(t, newStubSchema(root), nil, cache); len(rows) != 0 {
			t.Fatalf("rows = %+v, want none", rows)
		}
		stored, ok := cache.rows[anon]
		if !ok {
			t.Fatal("the completed empty scan was not cached; every identity-less file would be rescanned forever")
		}
		if stored.row.SourceID != "" {
			t.Fatalf("the cached row is not the zero row: %+v", stored.row)
		}
	})
}

// TestDiscoverJSONL_cacheAgnosticWithReadFailingFile is the blocker's own
// reproduction: over a corpus holding a file that opens and cannot be read,
// the cached and the cache-less answers must agree, and must keep agreeing
// once the filesystem heals without touching size or mod time.
func TestDiscoverJSONL_cacheAgnosticWithReadFailingFile(t *testing.T) {
	root := t.TempDir()
	paths := stubSessions(t, root, "a", "b", "c")
	broken := failReadFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(paths[1], "/")}

	nilCache := discoverStubCached(t, newStubSchema(root), broken, nil)
	cache := newMemCache()
	cold := discoverStubCached(t, newStubSchema(root), broken, cache)
	warm := discoverStubCached(t, newStubSchema(root), broken, cache)
	if len(nilCache) != 2 {
		t.Fatalf("nil-cache rows = %+v, want the two readable sessions", nilCache)
	}
	if !reflect.DeepEqual(cold, nilCache) {
		t.Fatalf("cold cache changed the rows:\n got %+v\nwant %+v", cold, nilCache)
	}
	if !reflect.DeepEqual(warm, nilCache) {
		t.Fatalf("warm cache changed the rows:\n got %+v\nwant %+v", warm, nilCache)
	}

	cached := discoverStubCached(t, newStubSchema(root), nil, cache)
	uncached := discoverStubCached(t, newStubSchema(root), nil, nil)
	if len(uncached) != len(paths) {
		t.Fatalf("uncached rows = %+v, want one per file", uncached)
	}
	if !reflect.DeepEqual(cached, uncached) {
		t.Fatalf("the cache changed the rows after a read-failing run:\n got %+v\nwant %+v", cached, uncached)
	}
}

// TestDiscoverJSONL_oversizedLineRowStaysCacheable pins the other half of
// D28: the token bound is a property of the bytes, the same file truncates in
// the same place every run, and (size, mtime) invalidates the row the moment
// the content changes. So the truncated row is cached and is a hit next run —
// a blanket "any scan error skips Store" would silently revert this.
func TestDiscoverJSONL_oversizedLineRowStaysCacheable(t *testing.T) {
	root := t.TempDir()
	huge := stubLine{SessionID: "s1", Role: "assistant", Text: strings.Repeat("x", vendors.ReadMaxToken+1)}.String()
	p := writeStubSession(t, root, "s.jsonl",
		stubLine{SessionID: "s1", Role: "user", Text: "before the giant"}.String(),
		huge,
		stubLine{SessionID: "s1", Role: "assistant", Model: "never-read"}.String(),
	)
	cache := newMemCache()
	first := onlyRow(t, discoverStubCached(t, newStubSchema(root), nil, cache))
	stored, ok := cache.rows[p]
	if !ok {
		t.Fatal("the truncated row was not cached; the token bound is a fact about the file (D28)")
	}
	if !reflect.DeepEqual(stored.row, first) {
		t.Fatalf("the cached row differs from the returned one:\n got %+v\nwant %+v", stored.row, first)
	}

	fsys, counts := jsonltest.CountingFS("/")
	second := onlyRow(t, discoverStubCached(t, newStubSchema(root), fsys, cache))
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("the warm row differs from the cold one:\n got %+v\nwant %+v", second, first)
	}
	if n := counts.Opens(strings.TrimPrefix(p, "/")); n != 0 {
		t.Fatalf("the truncated file was opened %d times on the warm run, want 0", n)
	}
	uncached := onlyRow(t, discoverStubCached(t, newStubSchema(root), nil, nil))
	if !reflect.DeepEqual(uncached, first) {
		t.Fatalf("the cache changed the truncated row:\n got %+v\nwant %+v", uncached, first)
	}
}

// --- the identity scan's discarded error (R3-01) -----------------------------

// TestFindJSONLSession_readFailureIsNotAMissingSession: a read that failed
// before a file's first identity line is a fact about this run, not about the
// file. Reporting it as "this file names no session" is the Strategy rule
// applied to an answer instead of to durable state: the lookup must fail with
// the transport error rather than walk on.
func TestFindJSONLSession_readFailureIsNotAMissingSession(t *testing.T) {
	root := t.TempDir()
	path := stubSessionOfLength(t, root, "s", 4)
	broken := failReadFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(path, "/"), after: 0}

	_, err := vendors.FindJSONLSession(context.Background(), newStubSchema(root), broken, nil, "s")
	if err == nil {
		t.Fatal("the lookup succeeded although the only candidate could not be read")
	}
	if !errors.Is(err, errWireDown) {
		t.Fatalf("error %q does not carry the transport failure", err)
	}
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("a read failure was reported as a missing session: %q", err)
	}

	t.Run("a refused open is still a skip", func(t *testing.T) {
		root := t.TempDir()
		paths := stubSessions(t, root, "a", "b")
		blocked := failOpenFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(paths[0], "/")}
		got, err := vendors.FindJSONLSession(context.Background(), newStubSchema(root), blocked, nil, "b")
		if err != nil {
			t.Fatalf("FindJSONLSession: %v", err)
		}
		if got != paths[1] {
			t.Fatalf("got %q, want %q: an unopenable file must not stop the walk", got, paths[1])
		}
	})

	t.Run("a read failure past the identity line leaves the identity standing", func(t *testing.T) {
		root := t.TempDir()
		path := stubSessionOfLength(t, root, "s", 8)
		// The budget covers the identity line and nothing after it.
		broken := failReadFS{
			inner: os.DirFS("/"),
			fail:  strings.TrimPrefix(path, "/"),
			after: bytesOfFirstLines(t, path, 1),
		}
		got, err := vendors.FindJSONLSession(context.Background(), newStubSchema(root), broken, nil, "s")
		if err != nil {
			t.Fatalf("FindJSONLSession: %v", err)
		}
		if got != path {
			t.Fatalf("got %q, want %q: one identity line decides the file (D17)", got, path)
		}
	})
}

// writeClaudeSession writes one Claude-shaped session file and returns its
// path, so a sibling-directory fixture can be driven through the real reader.
func writeClaudeSession(t *testing.T, dir, name, sessionID, text string) string {
	t.Helper()
	line := fmt.Sprintf(`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":%q,"cwd":"/work","message":{"content":%q}}`, sessionID, text)
	return writeStubSession(t, dir, name, line)
}

// TestFindJSONLSession_readFailureNeverAnswersWithASibling is the user-visible
// half of R3-01, over the sibling-directory fixture R2-02 introduced and
// through the reader boundary a continue actually crosses. With the walk-first
// duplicate unreadable, a discarded error makes the walk answer with the
// sibling while a warm index answers with the walk-first file, so `clai chat
// continue` opens a different transcript by cache warmth alone.
func TestFindJSONLSession_readFailureNeverAnswersWithASibling(t *testing.T) {
	root := t.TempDir()
	// The hyphen sorts below the separator, so "proj-bak" is the lexically
	// smaller full path while the walk reaches "proj" first.
	first := writeClaudeSession(t, filepath.Join(root, "proj"), "s.jsonl", "dup", "the walk reaches me first")
	writeClaudeSession(t, filepath.Join(root, "proj-bak"), "s.jsonl", "dup", "the backup copy")
	broken := failReadFS{inner: os.DirFS("/"), fail: strings.TrimPrefix(first, "/")}

	cold, err := anthropic.SourceReader{Root: root, FS: broken}.Read(context.Background(), nil, "dup")
	if err == nil {
		t.Fatalf("the cache-less read returned %+v; the walk-first duplicate was unreadable, so no answer is honest", cold.Messages)
	}
	assertNoSibling(t, cold, err)

	// The index was built while the wire was up; it names the walk-first file.
	warm, err := chat.NewForeignIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewForeignIndex: %v", err)
	}
	rows, err := (anthropic.SourceReader{Root: root}).Discover(context.Background(), warm)
	if err != nil {
		t.Fatalf("warming Discover: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want the two duplicates", len(rows))
	}
	if p, ok := warm.Locate("claude-code", "dup"); !ok || p != first {
		t.Fatalf("the index locates %q, want the walk-first %q", p, first)
	}
	hot, err := anthropic.SourceReader{Root: root, FS: broken}.Read(context.Background(), warm, "dup")
	if err == nil {
		t.Fatalf("the warm read returned %+v while the cache-less one failed: the answer depends on cache warmth", hot.Messages)
	}
	assertNoSibling(t, hot, err)
}

// assertNoSibling fails when a read that should not have answered handed back
// the backup copy, or named its directory in the error.
func assertNoSibling(t *testing.T, chat pub_models.Chat, err error) {
	t.Helper()
	if !errors.Is(err, errWireDown) {
		t.Fatalf("error %q does not carry the transport failure", err)
	}
	if strings.Contains(err.Error(), "proj-bak") {
		t.Fatalf("the error names the sibling the walk would not have chosen: %q", err)
	}
	for _, m := range chat.Messages {
		if strings.Contains(m.Content, "the backup copy") {
			t.Fatalf("the sibling's transcript was returned: %+v", chat.Messages)
		}
	}
}
