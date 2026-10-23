package photo_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/photo"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestPreviousQueryImage(t *testing.T) {
	first := append([]byte("\x89PNG\r\n\x1a\n"), 1)
	second := append([]byte("\x89PNG\r\n\x1a\n"), 2)
	saveGlobalScope := func(t *testing.T, confDir string, msgs ...pub_models.Message) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(confDir, "conversations"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		c := pub_models.Chat{ID: "globalScope", Messages: msgs}
		if _, err := chat.NewPersister(confDir, asset.NewStore(chat.AssetsDir(confDir))).Save(c); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	image := func(data []byte) pub_models.ImagePart {
		return pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: data}
	}

	t.Run("no globalScope.json", func(t *testing.T) {
		if _, err := photo.PreviousQueryImage(t.TempDir()); !errors.Is(err, photo.ErrNoPreviousImage) {
			t.Fatalf("err = %v, want ErrNoPreviousImage", err)
		}
	})

	t.Run("text-only previous query", func(t *testing.T) {
		confDir := t.TempDir()
		saveGlobalScope(t, confDir, pub_models.Message{Role: "user", Content: "hi"})
		if _, err := photo.PreviousQueryImage(confDir); !errors.Is(err, photo.ErrNoPreviousImage) {
			t.Fatalf("err = %v, want ErrNoPreviousImage", err)
		}
	})

	t.Run("stored images give the last one", func(t *testing.T) {
		confDir := t.TempDir()
		saveGlobalScope(t, confDir,
			pub_models.Message{Role: "user", Parts: []pub_models.Part{image(first), pub_models.TextPart{Text: "draw"}}},
			pub_models.Message{Role: "assistant", Parts: []pub_models.Part{image(second), pub_models.TextPart{Text: "done"}}},
			pub_models.Message{Role: "user", Content: "more"},
		)
		got, err := photo.PreviousQueryImage(confDir)
		if err != nil || !bytes.Equal(got.Data, second) || got.MIME != "image/png" {
			t.Fatalf("got %d bytes %q, err %v; want the last image", len(got.Data), got.MIME, err)
		}
	})

	t.Run("a legacy part gives its own bytes", func(t *testing.T) {
		confDir := t.TempDir()
		convDir := filepath.Join(confDir, "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		legacy := `{"id":"globalScope","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` +
			base64.StdEncoding.EncodeToString(first) + `"}}]}]}`
		if err := os.WriteFile(chat.GlobalScopePath(confDir), []byte(legacy), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := photo.PreviousQueryImage(confDir)
		if err != nil || !bytes.Equal(got.Data, first) || got.MIME != "image/png" {
			t.Fatalf("got %d bytes, err %v; want the legacy image", len(got.Data), err)
		}
		if _, err := os.Stat(chat.AssetsDir(confDir)); !os.IsNotExist(err) {
			t.Fatalf("reading the previous image wrote the asset store: %v", err)
		}
	})

	t.Run("a missing asset file fails", func(t *testing.T) {
		confDir := t.TempDir()
		saveGlobalScope(t, confDir, pub_models.Message{Role: "user", Parts: []pub_models.Part{image(first)}})
		if err := os.RemoveAll(chat.AssetsDir(confDir)); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		if _, err := photo.PreviousQueryImage(confDir); !errors.Is(err, asset.ErrAssetNotFound) {
			t.Fatalf("err = %v, want ErrAssetNotFound", err)
		}
	})

	t.Run("an unreadable globalScope.json fails", func(t *testing.T) {
		confDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(confDir, "conversations"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(chat.GlobalScopePath(confDir), []byte("{"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := photo.PreviousQueryImage(confDir)
		if err == nil || errors.Is(err, photo.ErrNoPreviousImage) {
			t.Fatalf("err = %v, want the decode error", err)
		}
	})
}
