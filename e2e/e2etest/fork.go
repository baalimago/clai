package e2etest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

var forkNoticeLine = regexp.MustCompile(`fork (\S+) of (\S+)`)

// ForkNotice is the IDs in the "fork <F> of <P>" line on stderr.
func ForkNotice(t *testing.T, stderr string) (forkID, parentID string) {
	t.Helper()
	m := forkNoticeLine.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("stderr has no fork notice: %q", stderr)
	}
	return m[1], m[2]
}

// WriteTextConfig writes a textConfig.json for model with summaries off.
func WriteTextConfig(t *testing.T, confDir, model string) {
	t.Helper()
	textConf := text.Default
	textConf.Model = model
	textConf.SummarizeConversations = false
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
}

// ConfigTree is every file under root, keyed by path, with its contents.
func ConfigTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(p)
		tree[p] = string(b)
		return readErr
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tree
}

// IndexRows is the chat index as the list reads it, keyed by chat ID.
func IndexRows(t *testing.T, confDir string) map[string]map[string]any {
	t.Helper()
	pager, err := chat.NewChatIndexPaginator(context.Background(), filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	rows, err := pager.Page(0, pager.Len())
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	out := map[string]map[string]any{}
	for _, r := range list {
		out[r["id"].(string)] = r
	}
	return out
}
