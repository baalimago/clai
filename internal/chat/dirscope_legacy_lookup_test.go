package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDirScopeFallsBackToLegacyRelativePathHash(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "conversations", "dirs"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations/dirs): %v", err)
	}

	root := t.TempDir()
	parent := filepath.Join(root, "bar")
	boundDir := filepath.Join(parent, "baz")
	if err := os.MkdirAll(boundDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(boundDir): %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(oldWD); chdirErr != nil {
			t.Fatalf("Chdir(oldWD): %v", chdirErr)
		}
	})
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("Chdir(parent): %v", err)
	}

	legacyInput := "./baz"
	legacySum := sha256.Sum256([]byte(legacyInput))
	legacyHash := hex.EncodeToString(legacySum[:])

	scope := DirScope{
		Version: 1,
		DirHash: legacyHash,
		ChatID:  "chat-legacy",
		Updated: "2026-01-01T00:00:00Z",
	}
	b, err := json.Marshal(scope)
	if err != nil {
		t.Fatalf("Marshal(scope): %v", err)
	}
	legacyPath := filepath.Join(confDir, "conversations", "dirs", legacyHash+".json")
	if err := os.WriteFile(legacyPath, b, 0o644); err != nil {
		t.Fatalf("WriteFile(legacyPath): %v", err)
	}

	handler := &ChatHandler{confDir: confDir}
	if _, err := os.Stat(handler.dirScopePathFromHash(handler.dirHash(boundDir))); err == nil {
		t.Fatalf("expected canonical dirscope binding to be absent")
	}
	got, err := handler.LoadDirScope(legacyInput)
	if err != nil {
		t.Fatalf("LoadDirScope: %v", err)
	}
	if got.ChatID != scope.ChatID {
		t.Fatalf("expected chat id %q, got %q", scope.ChatID, got.ChatID)
	}
}