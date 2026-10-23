package asset_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/asset"
)

func chmodForTest(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%s, %o): %v", path, mode, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestStore_NoDiskOnConstruct(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	store := asset.NewStore(dir)
	missing := sha256Hex([]byte("missing")) + ".png"
	_, _ = store.Load(missing)
	_, _ = store.Path(missing)
	_, _ = store.Resolve(missing[:6])
	_, _ = store.Sweep(nil)
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the store touched %s before the first Put: stat error %v", dir, err)
	}
	ref, err := store.Put(pngBytes(t, 20), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !slices.Equal(dirNames(t, dir), []string{ref}) {
		t.Fatalf("after the first Put %s holds %q, want [%s]", dir, dirNames(t, dir), ref)
	}
}

func TestStore_FailedPutLeavesNothing(t *testing.T) {
	t.Run("read-only store dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "assets")
		store := asset.NewStore(dir)
		first, err := store.Put(pngBytes(t, 21), "image/png")
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		chmodForTest(t, dir, 0o500)
		data := pngBytes(t, 22)
		ref := sha256Hex(data) + ".png"
		if _, err := store.Put(data, "image/png"); err == nil || !strings.Contains(err.Error(), ref) {
			t.Fatalf("Put into a read-only dir error = %v, want an error naming %s", err, ref)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{first}) {
			t.Fatalf("a failed Put left %q, want only [%s]", got, first)
		}
	})
	t.Run("store dir cannot be created", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(parent, 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		chmodForTest(t, parent, 0o500)
		data := pngBytes(t, 23)
		ref := sha256Hex(data) + ".png"
		if _, err := asset.NewStore(filepath.Join(parent, "assets")).Put(data, "image/png"); err == nil || !strings.Contains(err.Error(), ref) {
			t.Fatalf("Put under a read-only parent error = %v, want an error naming %s", err, ref)
		}
		if got := dirNames(t, parent); len(got) != 0 {
			t.Fatalf("a failed Put left %q", got)
		}
	})
}

func TestStore_CorruptDetected(t *testing.T) {
	dir := t.TempDir()
	store := asset.NewStore(dir)
	ref, err := store.Put(pngBytes(t, 24), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ref), pngBytes(t, 25), 0o644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	img, err := store.Load(ref)
	if !errors.Is(err, asset.ErrAssetCorrupt) {
		t.Fatalf("Load(corrupt) error = %v, want ErrAssetCorrupt", err)
	}
	if img.Data != nil || !strings.Contains(err.Error(), ref) {
		t.Fatalf("Load(corrupt) = %d bytes, %v; want no bytes and an error naming %s", len(img.Data), err, ref)
	}
}

func TestStore_FilesystemErrorsAreNotAbsence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := asset.NewStore(file)
	ref := sha256Hex([]byte("x")) + ".png"
	absence := []error{asset.ErrAssetNotFound, asset.ErrUnknownHandle}
	checks := map[string]error{}
	_, checks["Load"] = store.Load(ref)
	_, checks["Path"] = store.Path(ref)
	_, checks["Resolve"] = store.Resolve(ref[:6])
	_, checks["Put"] = store.Put(pngBytes(t, 26), "image/png")
	_, checks["Sweep"] = store.Sweep(nil)
	for op, err := range checks {
		if err == nil {
			t.Fatalf("%s on a store whose dir is a file: no error", op)
		}
		for _, sentinel := range absence {
			if errors.Is(err, sentinel) {
				t.Fatalf("%s on a store whose dir is a file reported absence: %v", op, err)
			}
		}
	}

	dir := filepath.Join(t.TempDir(), "assets")
	store = asset.NewStore(dir)
	ref, err := store.Put(pngBytes(t, 27), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ref)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ref), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := store.Load(ref); err == nil || errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("Load of a directory named like a ref error = %v, want a read error", err)
	}
	if _, err := store.Resolve(ref[:6]); !errors.Is(err, asset.ErrUnknownHandle) {
		t.Fatalf("Resolve matched a directory: %v", err)
	}
	dead, err := store.Put(pngBytes(t, 28), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	chmodForTest(t, dir, 0o500)
	n, err := store.Sweep(nil)
	if err == nil || n != 0 || !strings.Contains(err.Error(), dead) {
		t.Fatalf("Sweep of a read-only dir = %d, %v; want 0 and an error naming %s", n, err, dead)
	}
}
