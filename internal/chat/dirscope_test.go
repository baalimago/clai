package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestDirScope_SaveLoadRoundTrip(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	cq := &ChatHandler{confDir: confDir}

	dir := filepath.Join(t.TempDir(), "proj")
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir(2): %v", err)
	}

	chatID := "my_chat_id"
	if err := cq.SaveDirScope(dir, chatID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	got, err := cq.LoadDirScope(dir)
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	testboil.FailTestIfDiff(t, got.ChatID, chatID)
	if got.DirHash == "" {
		t.Fatalf("expected DirHash to be set")
	}
}

func TestDirScope_StableHashAfterClean(t *testing.T) {
	d := t.TempDir()
	a, err := canonicalDir(d + string(filepath.Separator) + ".")
	if err != nil {
		t.Fatalf("canonicalDir(a): %v", err)
	}
	b, err := canonicalDir(d)
	if err != nil {
		t.Fatalf("canonicalDir(b): %v", err)
	}
	testboil.FailTestIfDiff(t, a, b)

	ha := dirHash(a)
	hb := dirHash(b)
	testboil.FailTestIfDiff(t, ha, hb)
}

func Test_UpdateDirScopeFromCWD_updatesBinding(t *testing.T) {
	confDir := t.TempDir()

	// Ensure required dirs exist (CreateConfigDir is called by main, but we use it directly here).
	if err := os.MkdirAll(filepath.Join(confDir, "conversations", "dirs"), 0o755); err != nil {
		t.Fatalf("MkdirAll(dirs): %v", err)
	}

	wd := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	chatID := "some_chat"
	cq := &ChatHandler{confDir: confDir}
	if err := cq.UpdateDirScopeFromCWD(chatID); err != nil {
		t.Fatalf("UpdateDirScopeFromCWD: %v", err)
	}

	ds, err := cq.LoadDirScope(wd)
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	if ds.ChatID != chatID {
		t.Fatalf("expected chatID %q got %q", chatID, ds.ChatID)
	}
}

func TestLoadDirScopedContext_keepsFields(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "conversations", "dirs"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	want := labelledChat("bound", pub_models.Message{Role: "user", Content: "hi"})
	if err := Save(filepath.Join(confDir, "conversations"), want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wd := t.TempDir()
	if err := saveDirScope(confDir, wd, want.ID); err != nil {
		t.Fatalf("saveDirScope: %v", err)
	}
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	got, err := LoadDirScopedContext(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopedContext: %v", err)
	}
	if got.ID != want.ID {
		t.Fatalf("id = %q, want %q", got.ID, want.ID)
	}
	assertLabelled(t, got, want)
}
