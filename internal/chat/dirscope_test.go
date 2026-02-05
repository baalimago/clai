package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/utils"
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
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	cq := &ChatHandler{confDir: confDir}

	d := t.TempDir()
	a, err := cq.canonicalDir(d + string(filepath.Separator) + ".")
	if err != nil {
		t.Fatalf("canonicalDir(a): %v", err)
	}
	b, err := cq.canonicalDir(d)
	if err != nil {
		t.Fatalf("canonicalDir(b): %v", err)
	}
	testboil.FailTestIfDiff(t, a, b)

	ha := cq.dirHash(a)
	hb := cq.dirHash(b)
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
