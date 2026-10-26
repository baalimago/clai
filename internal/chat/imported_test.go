package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// exportReader is a foreign tool whose Read returns full chats and counts
// the reads, so a test can prove an unchanged conversation is not re-read.
type exportReader struct {
	name  string
	rows  []vendors.SourceRow
	chats map[string]pub_models.Chat
	reads *int
}

func (r exportReader) Source() string { return r.name }

func (r exportReader) Discover(context.Context, vendors.SourceCache) ([]vendors.SourceRow, error) {
	return r.rows, nil
}

func (r exportReader) Read(_ context.Context, _ vendors.SourceCache, id string) (pub_models.Chat, error) {
	*r.reads++
	c, ok := r.chats[id]
	if !ok {
		return pub_models.Chat{}, errors.New("gone")
	}
	return c, nil
}

func TestExportForeignAndImportedReader(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	img := testPNG(t, 7)
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	reads := 0
	reader := exportReader{
		name: "claude-code",
		rows: []vendors.SourceRow{
			{Source: "claude-code", SourceID: "s/1", MessageCount: 2, Cwd: "/work"},
			{Source: "claude-code", SourceID: "cloned", MessageCount: 1},
			{Source: "claude-code", SourceID: "broken", MessageCount: 1},
			{Source: "Bad Name", SourceID: "x", MessageCount: 1},
		},
		chats: map[string]pub_models.Chat{
			"s/1": {Created: created, Messages: []pub_models.Message{
				{Role: "user", Parts: []pub_models.Part{pub_models.TextPart{Text: "look  at\nthis"}, pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: img}}},
				{Role: "assistant", Content: "a cat"},
			}},
			"cloned": {Messages: []pub_models.Message{{Role: "user", Content: "hi"}}},
		},
		reads: &reads,
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	if err := UpsertChatIndexBatch(convDir, []pub_models.Chat{{ID: "n1", Source: "claude-code", SourceID: "cloned", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}}); err != nil {
		t.Fatal(err)
	}

	err := ExportForeign(context.Background(), confDir, nil)
	if err == nil {
		t.Fatal("the broken conversation's error was swallowed")
	}
	p := filepath.Join(convDir, "imported", "claude-code", "s_1.json")
	exported, err := FromPath(p)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Source != "claude-code" || exported.SourceID != "s/1" || exported.OriginDir != "/work" || exported.ID != "s/1" {
		t.Fatalf("exported %+v", exported)
	}
	part := exported.Messages[0].Parts[1].(pub_models.ImagePart)
	if part.Asset != pngRef(img) {
		t.Fatalf("asset %q", part.Asset)
	}
	if _, err := os.Stat(filepath.Join(convDir, "imported", "claude-code", "cloned.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a cloned conversation was exported too")
	}
	live, err := liveAssetRefs(convDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := live[pngRef(img)]; !ok {
		t.Fatal("the asset sweep would delete an export's image")
	}

	// Unchanged: no second read.
	before := reads
	_ = ExportForeign(context.Background(), confDir, nil)
	if reads != before+1 { // only the broken one is retried
		t.Fatalf("reads %d → %d", before, reads)
	}

	// On a machine without the tool, the imported reader lists it under
	// its own source and reads it back.
	ir := importedReader{convDir: convDir}
	rows, err := ir.Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v %v", rows, err)
	}
	row := rows[0]
	if row.Source != "claude-code" || row.SourceID != "s/1" || row.MessageCount != 2 || row.FirstUserMessage == "" || row.Cwd != "/work" {
		t.Fatalf("row %+v", row)
	}
	got, err := ir.Read(context.Background(), nil, "s/1")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("read %v", err)
	}
	var nf *ImportedNotFoundError
	if _, err := ir.Read(context.Background(), nil, "nope"); !errors.As(err, &nf) {
		t.Fatalf("err %v", err)
	}

	// With the tool present, its reader wins and the export is hidden; the
	// export alone is listed through the imported reader.
	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatal(err)
	}
	listRows, byName, err := cq.buildChatListRows(context.Background(), paginator)
	if err != nil {
		t.Fatal(err)
	}
	foreign := 0
	for _, r := range listRows {
		if r.Kind == chatRowForeign && r.SourceID == "s/1" {
			foreign++
			if r.Reader != "" {
				t.Fatalf("the tool's own reader lost the dedup: %+v", r)
			}
		}
	}
	if foreign != 1 || byName["imported"] == nil {
		t.Fatalf("foreign rows %d", foreign)
	}
	defer useTestSourceReaders(nil)()
	listRows, _, err = cq.buildChatListRows(context.Background(), paginator)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range listRows {
		if r.SourceID == "s/1" {
			found = r.Reader == "imported" && r.displaySource() == "claude-code"
		}
	}
	if !found {
		t.Fatal("the export is not listed on a machine without the tool")
	}
}

func TestImportedFileName(t *testing.T) {
	for in, want := range map[string]string{"a/b": "a_b.json", ".hidden": "_.hidden.json", "plain": "plain.json"} {
		if got := importedFileName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestReindexPulled(t *testing.T) {
	_, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	write := func(c pub_models.Chat) {
		b, _ := json.Marshal(c)
		if err := os.WriteFile(conversationPathFromDir(convDir, c.ID), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := pub_models.Chat{ID: "old", Messages: []pub_models.Message{{Role: "user", Content: "bye"}}}
	write(old)
	if err := UpsertChatIndexBatch(convDir, []pub_models.Chat{old}); err != nil {
		t.Fatal(err)
	}
	write(pub_models.Chat{ID: "new", Messages: []pub_models.Message{{Role: "user", Content: "hello"}}})
	if err := ReindexPulled(confDir, []string{"conversations/new.json"}, []string{"conversations/old.json"}); err != nil {
		t.Fatal(err)
	}
	rows, err := readChatIndex(convDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "new" {
		t.Fatalf("rows %+v", rows)
	}
	if err := ReindexPulled(confDir, []string{"conversations/missing.json"}, nil); err == nil {
		t.Fatal("a missing file reindexed fine")
	}
}
