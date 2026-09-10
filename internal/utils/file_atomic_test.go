package utils

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileAtomic_readerSeesWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.json")
	payloads := [][]byte{
		[]byte(strings.Repeat("a", 1<<16)),
		[]byte(strings.Repeat("b", 1<<16)),
	}
	if err := WriteFileAtomic(path, payloads[0], 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	const rounds = 200
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range rounds {
				if err := WriteFileAtomic(path, payloads[(w+i)%2], 0o644); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			for range rounds {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if !bytes.Equal(got, payloads[0]) && !bytes.Equal(got, payloads[1]) {
					t.Errorf("reader saw a partial file of %d bytes", len(got))
					return
				}
			}
		})
	}
	wg.Wait()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("stat = %v, %v; want perm 0644", info, err)
	}
}

func TestWriteFileAtomic_errorLeavesOldFile(t *testing.T) {
	t.Run("parent is not a directory", func(t *testing.T) {
		dir := t.TempDir()
		notDir := filepath.Join(dir, "file")
		if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := WriteFileAtomic(filepath.Join(notDir, "model.json"), []byte("{}"), 0o644); err == nil {
			t.Fatal("expected an error when the parent is a file")
		}
		if got, _ := os.ReadFile(notDir); string(got) != "x" {
			t.Fatalf("sibling file altered: %q", got)
		}
	})
	t.Run("target is a non-empty directory", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "model.json")
		if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := WriteFileAtomic(target, []byte("{}"), 0o644); err == nil {
			t.Fatal("expected a rename error over a non-empty directory")
		}
		if _, err := os.Stat(filepath.Join(target, "child")); err != nil {
			t.Fatalf("existing directory altered: %v", err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Fatalf("temp file left behind: %v", entries)
		}
	})
}
