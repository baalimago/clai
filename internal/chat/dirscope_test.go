package chat

import (
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

	got, ok, err := cq.LoadDirScope(dir)
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	testboil.FailTestIfDiff(t, got.ChatID, chatID)
	if got.DirHash == "" {
		t.Fatalf("expected DirHash to be set")
	}
}

func TestDirScope_MissingReturnsOkFalse(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	cq := &ChatHandler{confDir: confDir}
	_, ok, err := cq.LoadDirScope(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false")
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
