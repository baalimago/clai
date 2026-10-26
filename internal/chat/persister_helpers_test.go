package chat

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const textOnlyGoldenDir = "testdata/persister"

// textOnlyChats are the pre-worklog-shaped fixtures pinned by the text-only goldens.
func textOnlyChats() (plain, tools pub_models.Chat) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	usage := &pub_models.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}
	plain = pub_models.Chat{
		Created:   created,
		ID:        "text-plain",
		Profile:   "coder",
		OriginDir: "/work",
		Title:     "Greeting",
		Summary:   "A greeting and its answer.",
		SummaryAt: created.Add(time.Hour),
		Messages: []pub_models.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "hello there"},
			{Role: "assistant", Content: "general kenobi"},
		},
		TokenUsage:       usage,
		RecentTokenUsage: usage,
		Queries: []pub_models.QueryCost{{
			CreatedAt: created, CostUSD: 0.25, MessageTrigger: 1, Model: "gpt-test", Usage: *usage,
		}},
	}
	tools = pub_models.Chat{
		Created: created.Add(time.Minute),
		ID:      "text-tools",
		Messages: []pub_models.Message{
			{Role: "user", Content: "list the files"},
			{
				Role:             "assistant",
				ReasoningContent: "I should call ls.",
				ToolCalls: []pub_models.Call{{
					ID:       "call_1",
					Name:     "ls",
					Function: pub_models.Specification{Name: "ls", Arguments: `{"dir":"."}`},
				}},
				ReasoningItems: []pub_models.ReasoningItem{{ID: "rs_1", EncryptedContent: "sealed", Summary: []string{"plan"}}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "a.txt\nb.txt"},
			{Role: "assistant", Content: "Two files."},
		},
	}
	return plain, tools
}

var snapshotCreatedLine = regexp.MustCompile(`(?m)^  "created": "[^"]*",$`)

// snapshotConvDir renders every file under convDir, sorted by path. A promoted
// conversation's random id becomes <promoted>, and the wall-clock fields
// (index Updated, the Created of globalScope and the promoted copy) are zeroed.
func snapshotConvDir(t *testing.T, convDir string, fixtureIDs ...string) string {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(convDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(convDir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", convDir, err)
	}
	promoted := promotedID(files, fixtureIDs)
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data := files[name]
		volatile := name == globalScopeFile || (promoted != "" && name == promoted+".json")
		switch {
		case name == chatIndexJournalName || name == chatIndexCompactingName:
			continue
		case name == chatIndexFileName:
			data = normalizedIndex(t, mergedIndexBytes(t, convDir), fixtureIDs)
		case volatile:
			data = snapshotCreatedLine.ReplaceAll(data, []byte(`  "created": "<now>",`))
		}
		if promoted != "" {
			name = strings.ReplaceAll(name, promoted, "<promoted>")
			data = []byte(strings.ReplaceAll(string(data), promoted, "<promoted>"))
		}
		b.WriteString("== " + name + "\n")
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}

func promotedID(files map[string][]byte, fixtureIDs []string) string {
	for name := range files {
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || strings.Contains(name, "/") || id == globalScopeChatID || slices.Contains(fixtureIDs, id) {
			continue
		}
		return id
	}
	return ""
}

func normalizedIndex(t *testing.T, data []byte, fixtureIDs []string) []byte {
	t.Helper()
	var cache chatIndexCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("decode chat index: %v", err)
	}
	for i := range cache.Rows {
		cache.Rows[i].Updated = time.Time{}
		if !slices.Contains(fixtureIDs, cache.Rows[i].ID) {
			cache.Rows[i].Created = time.Time{}
		}
	}
	slices.SortFunc(cache.Rows, func(a, b chatIndexRow) int { return strings.Compare(a.ID, b.ID) })
	out, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		t.Fatalf("encode chat index: %v", err)
	}
	return out
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(textOnlyGoldenDir, name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(b)
}

// mergedIndexBytes encodes the index as readers see it, cache plus journal,
// in the cache's own format.
func mergedIndexBytes(t *testing.T, convDir string) []byte {
	t.Helper()
	rows, err := readChatIndex(convDir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	b, err := json.Marshal(chatIndexCache{Version: chatIndexVersion, Rows: rows})
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	return b
}
