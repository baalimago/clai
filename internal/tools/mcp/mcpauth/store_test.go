package mcpauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStoreFileModeIsRestrictive(t *testing.T) {
	store := NewTokenStore(t.TempDir())
	if err := store.Save("srv", TokenEntry{AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(store.Path("srv"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != TokenStoreFileMode {
		t.Errorf("mode = %o, want %o", got, TokenStoreFileMode)
	}
}

func TestTokenStoreUnreadableIsCacheMiss(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)
	// A directory where the entry file would be: os.ReadFile fails
	// deterministically regardless of the test's uid, unlike a permission
	// bit flip.
	if err := os.Mkdir(store.Path("srv"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := store.Load("srv"); ok {
		t.Fatal("expected a miss for an unreadable entry")
	}
}

func TestTokenStoreUnwritableIsTypedError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	// store.dir is a path through a file, not a directory, so MkdirAll
	// fails deterministically.
	store := NewTokenStore(filepath.Join(blocker, "mcpAuth"))

	err := store.Save("srv", TokenEntry{AccessToken: "tok"})
	var writeErr *TokenStoreWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("got %v (%T), want *TokenStoreWriteError", err, err)
	}
}

func TestTokenStoreCorruptEntryIsCacheMiss(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)
	if err := os.WriteFile(store.Path("srv"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt entry: %v", err)
	}
	if _, ok := store.Load("srv"); ok {
		t.Fatal("expected a miss for a corrupt entry")
	}
}
