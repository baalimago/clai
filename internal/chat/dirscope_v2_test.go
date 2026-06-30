package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func newTestHandler(t *testing.T) (*ChatHandler, string) {
	t.Helper()
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	return &ChatHandler{confDir: confDir, convDir: conversationsDir(confDir)}, confDir
}

func TestSaveDirScope_WritesVersion2WithHistoryAndTypedTime(t *testing.T) {
	cq, _ := newTestHandler(t)
	dir := t.TempDir()

	if err := cq.SaveDirScope(dir, "chat-a"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	ds, err := cq.LoadDirScope(dir)
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	if ds.Version != 2 {
		t.Fatalf("expected version 2, got %d", ds.Version)
	}
	if ds.ChatID != "chat-a" {
		t.Fatalf("expected head chat-a, got %q", ds.ChatID)
	}
	canon, _ := canonicalDir(dir)
	if ds.AbsPath != canon {
		t.Fatalf("expected abs_path %q, got %q", canon, ds.AbsPath)
	}
	if len(ds.History) != 1 || ds.History[0].ChatID != "chat-a" {
		t.Fatalf("expected history seeded with chat-a, got %+v", ds.History)
	}
	if ds.Updated.IsZero() {
		t.Fatalf("expected typed Updated to be set")
	}

	// updated must marshal to an RFC3339 string on disk.
	raw, err := os.ReadFile(cq.dirScopePathFromHash(dirHash(canon)))
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal binding: %v", err)
	}
	updatedStr, ok := asMap["updated"].(string)
	if !ok {
		t.Fatalf("expected updated as JSON string, got %T", asMap["updated"])
	}
	if _, err := time.Parse(time.RFC3339, updatedStr); err != nil {
		t.Fatalf("updated %q is not RFC3339: %v", updatedStr, err)
	}
}

func TestSaveDirScope_HistoryUpsertDedupAndFirstLast(t *testing.T) {
	cq, _ := newTestHandler(t)
	dir := t.TempDir()

	if err := cq.SaveDirScope(dir, "a"); err != nil {
		t.Fatalf("SaveDirScope(a): %v", err)
	}
	ds, _ := cq.LoadDirScope(dir)
	firstScopedA := ds.History[0].FirstScoped

	if err := cq.SaveDirScope(dir, "b"); err != nil {
		t.Fatalf("SaveDirScope(b): %v", err)
	}
	if err := cq.SaveDirScope(dir, "a"); err != nil {
		t.Fatalf("SaveDirScope(a again): %v", err)
	}

	ds, _ = cq.LoadDirScope(dir)
	if ds.ChatID != "a" {
		t.Fatalf("expected head a after re-bind, got %q", ds.ChatID)
	}
	if len(ds.History) != 2 {
		t.Fatalf("expected dedup to 2 entries, got %d: %+v", len(ds.History), ds.History)
	}
	if ds.History[0].ChatID != "a" || ds.History[1].ChatID != "b" {
		t.Fatalf("expected newest-first [a, b], got %+v", ds.History)
	}
	// FirstScoped preserved across the re-bind; LastScoped advanced.
	if !ds.History[0].FirstScoped.Equal(firstScopedA) {
		t.Fatalf("expected FirstScoped preserved %v, got %v", firstScopedA, ds.History[0].FirstScoped)
	}
	if ds.History[0].LastScoped.Before(ds.History[0].FirstScoped) {
		t.Fatalf("expected LastScoped >= FirstScoped")
	}
}

func TestUpsertScopedHistory_CapsNewestFirst(t *testing.T) {
	var history []ScopedChat
	now := time.Now().UTC()
	for i := 0; i < dirScopeHistoryCap+10; i++ {
		id := fmt.Sprintf("chat-%03d", i)
		history = upsertScopedHistory(history, id, now.Add(time.Duration(i)*time.Second))
	}
	if len(history) != dirScopeHistoryCap {
		t.Fatalf("expected cap %d, got %d", dirScopeHistoryCap, len(history))
	}
	// Newest-first: the last inserted id is at the front.
	if history[0].ChatID != fmt.Sprintf("chat-%03d", dirScopeHistoryCap+9) {
		t.Fatalf("expected newest at front, got %q", history[0].ChatID)
	}
}

func TestLoadDirScope_V1UpgradesToV2InPlaceSamePath(t *testing.T) {
	cq, confDir := newTestHandler(t)
	dir := t.TempDir()
	canon, _ := canonicalDir(dir)
	hash := dirHash(canon)
	path := dirscopePath(confDir, hash)

	// Seed a version 1 binding: no abs_path, no history, updated as RFC3339 string.
	v1 := `{"version":1,"dir_hash":"` + hash + `","chat_id":"legacy","updated":"2024-01-02T03:04:05Z"}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatalf("WriteFile(v1): %v", err)
	}

	// A v1 binding resolves directly.
	ds, err := cq.LoadDirScope(dir)
	if err != nil {
		t.Fatalf("LoadDirScope(v1): %v", err)
	}
	if ds.Version != 1 || ds.ChatID != "legacy" {
		t.Fatalf("expected v1 legacy resolution, got %+v", ds)
	}

	// Next write upgrades in place at the same path, preserving chat history head.
	if err := cq.SaveDirScope(dir, "fresh"); err != nil {
		t.Fatalf("SaveDirScope(upgrade): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected same path to still exist: %v", err)
	}
	entries, _ := os.ReadDir(dirscopeRoot(confDir))
	if len(entries) != 1 {
		t.Fatalf("expected exactly one binding file (no second key), got %d", len(entries))
	}
	ds, _ = cq.LoadDirScope(dir)
	if ds.Version != 2 {
		t.Fatalf("expected upgrade to version 2, got %d", ds.Version)
	}
	if ds.History[0].ChatID != "fresh" {
		t.Fatalf("expected history head fresh, got %+v", ds.History)
	}
}

func TestEnsureOriginDir_StampsOnFirstPersistAndIsImmutable(t *testing.T) {
	_, confDir := newTestHandler(t)
	wd := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	canonWd, _ := canonicalDir(wd)

	c := pub_models.Chat{ID: "origin-chat", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := EnsureOriginDir(confDir, &c); err != nil {
		t.Fatalf("EnsureOriginDir(first): %v", err)
	}
	if c.OriginDir != canonWd {
		t.Fatalf("expected origin %q, got %q", canonWd, c.OriginDir)
	}
	if err := Save(conversationsDir(confDir), c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Index mirroring: the persisted row carries origin_dir.
	rows, err := readChatIndex(conversationsDir(confDir))
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].OriginDir != canonWd {
		t.Fatalf("expected index row origin %q, got %+v", canonWd, rows)
	}

	// Simulate a reply from a different directory: a fresh in-memory chat with the
	// same id and empty origin must adopt the persisted origin, not the new CWD.
	other := t.TempDir()
	if err := os.Chdir(other); err != nil {
		t.Fatalf("Chdir(other): %v", err)
	}
	reply := pub_models.Chat{ID: "origin-chat"}
	if err := EnsureOriginDir(confDir, &reply); err != nil {
		t.Fatalf("EnsureOriginDir(reply): %v", err)
	}
	if reply.OriginDir != canonWd {
		t.Fatalf("expected reply to preserve origin %q, got %q", canonWd, reply.OriginDir)
	}
}

func TestOriginMatches_SubtreeAndExact(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		query   string
		subtree bool
		want    bool
	}{
		{"exact", "/a/b", "/a/b", true, true},
		{"nested subtree", "/a/b/c", "/a/b", true, true},
		{"nested exact-only", "/a/b/c", "/a/b", false, false},
		{"boundary not prefix", "/a/bc", "/a/b", true, false},
		{"empty origin", "", "/a/b", true, false},
		{"root subtree", "/anything", string(os.PathSeparator), true, true},
		{"unrelated", "/x/y", "/a/b", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := originMatches(c.origin, c.query, c.subtree); got != c.want {
				t.Fatalf("originMatches(%q,%q,%v)=%v want %v", c.origin, c.query, c.subtree, got, c.want)
			}
		})
	}
}
