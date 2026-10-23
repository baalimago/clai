package asset_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
)

// subject is one AssetStorer under the contract suite, with probes into its storage.
type subject struct {
	store asset.AssetStorer
	// plant stores data under name, bypassing Put, to stage crafted or foreign files.
	plant func(t *testing.T, name string, data []byte)
	names func(t *testing.T) []string
	// dir is the disk store's directory; empty for the memory fake.
	dir string
}

func diskSubject(t *testing.T) subject {
	dir := filepath.Join(t.TempDir(), "assets")
	return subject{
		store: asset.NewStore(dir),
		dir:   dir,
		plant: func(t *testing.T, name string, data []byte) {
			t.Helper()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("create %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatalf("plant %s: %v", name, err)
			}
		},
		names: func(t *testing.T) []string {
			t.Helper()
			entries, err := os.ReadDir(dir)
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return names
		},
	}
}

func memorySubject(*testing.T) subject {
	m := assettest.NewMemory()
	return subject{
		store: m,
		plant: func(_ *testing.T, name string, data []byte) { m.Plant(name, data) },
		names: func(*testing.T) []string { return m.Names() },
	}
}

type contractCase struct {
	name   string
	proves string
	run    func(t *testing.T, s subject)
}

func TestAssetStorerContract(t *testing.T) {
	subjects := []struct {
		name string
		new  func(t *testing.T) subject
	}{
		{"disk", diskSubject},
		{"memory", memorySubject},
	}
	for _, sub := range subjects {
		t.Run(sub.name, func(t *testing.T) {
			for _, c := range contractCases() {
				t.Run(c.name, func(t *testing.T) {
					t.Cleanup(func() {
						if t.Failed() {
							t.Logf("clause: %s", c.proves)
						}
					})
					c.run(t, sub.new(t))
				})
			}
		})
	}
}

func contractCases() []contractCase {
	return []contractCase{
		{"PutLoad", "Put.1, Load.1", testPutLoad},
		{"PutIdempotent", "Put.2", testPutIdempotent},
		{"PutUnsupportedMIME", "Put.4", testPutUnsupportedMIME},
		{"PutMismatch", "Put.5", testPutMismatch},
		{"PathStored", "Path.1", testPathStored},
		{"PathMissing", "Path.2", testPathMissing},
		{"LoadMissing", "Load.2", testLoadMissing},
		{"LoadCorrupt", "Load.3", testLoadCorrupt},
		{"LoadBadRef", "Load.4", testLoadBadRef},
		{"ResolveUnique", "Resolve.1", testResolveUnique},
		{"ResolveAmbiguous", "Resolve.3", testResolveAmbiguous},
		{"ResolveUnknown", "Resolve.2", testResolveUnknown},
		{"ResolveBadHandle", "Resolve.4", testResolveBadHandle},
		{"SweepRemovesDead", "Sweep.1", testSweepRemovesDead},
		{"SweepKeepsForeign", "Sweep.2", testSweepKeepsForeign},
		{"SweepAllLive", "Sweep.3", testSweepAllLive},
		{"SweepNoDir", "Sweep.4", testSweepNoDir},
	}
}

func mustPut(t *testing.T, s subject, data []byte, mime string) string {
	t.Helper()
	ref, err := s.store.Put(data, mime)
	if err != nil {
		t.Fatalf("Put(%s): %v", mime, err)
	}
	return ref
}

func wantNames(t *testing.T, s subject, want ...string) {
	t.Helper()
	got := s.names(t)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("stored names = %q, want %q", got, want)
	}
}

func testPutLoad(t *testing.T, s subject) {
	for _, tc := range []struct {
		mime, ext string
		data      []byte
	}{
		{"image/png", "png", pngBytes(t, 1)},
		{"image/jpeg", "jpg", jpegBytes(t)},
		{"image/gif", "gif", gifBytes(t)},
		{"image/webp", "webp", webpBytes()},
	} {
		ref := mustPut(t, s, tc.data, tc.mime)
		if want := sha256Hex(tc.data) + "." + tc.ext; ref != want {
			t.Fatalf("Put(%s) ref = %q, want %q", tc.mime, ref, want)
		}
		img, err := s.store.Load(ref)
		if err != nil {
			t.Fatalf("Load(%s): %v", ref, err)
		}
		if string(img.Data) != string(tc.data) || img.MIME != tc.mime {
			t.Fatalf("Load(%s) = %d bytes %q, want %d bytes %q", ref, len(img.Data), img.MIME, len(tc.data), tc.mime)
		}
	}
}

func testPutIdempotent(t *testing.T, s subject) {
	data := pngBytes(t, 2)
	first := mustPut(t, s, data, "image/png")
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if s.dir != "" {
		if err := os.Chtimes(filepath.Join(s.dir, first), old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
	second := mustPut(t, s, data, "image/png")
	if first != second {
		t.Fatalf("refs differ: %q then %q", first, second)
	}
	wantNames(t, s, first)
	if s.dir != "" {
		info, err := os.Stat(filepath.Join(s.dir, first))
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if !info.ModTime().Equal(old) {
			t.Fatalf("second Put rewrote the file: mtime %v, want %v", info.ModTime(), old)
		}
	}
}

func testPutUnsupportedMIME(t *testing.T, s subject) {
	_, err := s.store.Put(pngBytes(t, 3), "image/tiff")
	if !errors.Is(err, asset.ErrUnsupportedMIME) {
		t.Fatalf("Put(image/tiff) error = %v, want ErrUnsupportedMIME", err)
	}
	wantNames(t, s)
}

func testPutMismatch(t *testing.T, s subject) {
	_, err := s.store.Put(pngBytes(t, 4), "image/jpeg")
	var mismatch *asset.MIMEMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Put(png as jpeg) error = %v, want *MIMEMismatchError", err)
	}
	if mismatch.Declared != "image/jpeg" || mismatch.Sniffed != "image/png" {
		t.Fatalf("mismatch = %+v, want {image/jpeg image/png}", *mismatch)
	}
	if _, err := s.store.Put([]byte("plain text"), "image/png"); !errors.Is(err, asset.ErrNotImage) {
		t.Fatalf("Put(text as png) error = %v, want ErrNotImage", err)
	}
	wantNames(t, s)
}

func testPathStored(t *testing.T, s subject) {
	ref := mustPut(t, s, pngBytes(t, 5), "image/png")
	path, err := s.store.Path(ref)
	if err != nil {
		t.Fatalf("Path(%s): %v", ref, err)
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != ref {
		t.Fatalf("Path(%s) = %q, want an absolute path ending in the ref", ref, path)
	}
	if !slices.Contains(s.names(t), filepath.Base(path)) {
		t.Fatalf("Path(%s) = %q names no stored file", ref, path)
	}
	if s.dir != "" {
		if want, _ := filepath.Abs(filepath.Join(s.dir, ref)); path != want {
			t.Fatalf("Path(%s) = %q, want %q", ref, path, want)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

func testPathMissing(t *testing.T, s subject) {
	unknown := sha256Hex([]byte("never stored")) + ".png"
	if _, err := s.store.Path(unknown); !errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("Path(unknown) error = %v, want ErrAssetNotFound", err)
	}
	s.plant(t, "../x.png", pngBytes(t, 6))
	if _, err := s.store.Path("../x.png"); !errors.Is(err, asset.ErrAssetRef) {
		t.Fatalf("Path(../x.png) error = %v, want ErrAssetRef", err)
	}
}

func testLoadMissing(t *testing.T, s subject) {
	unknown := sha256Hex([]byte("never stored")) + ".png"
	if _, err := s.store.Load(unknown); !errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("Load(unknown) on an empty store error = %v, want ErrAssetNotFound", err)
	}
	mustPut(t, s, pngBytes(t, 7), "image/png")
	if _, err := s.store.Load(unknown); !errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("Load(unknown) error = %v, want ErrAssetNotFound", err)
	}
}

func testLoadCorrupt(t *testing.T, s subject) {
	ref := sha256Hex(pngBytes(t, 8)) + ".png"
	s.plant(t, ref, pngBytes(t, 9))
	img, err := s.store.Load(ref)
	if !errors.Is(err, asset.ErrAssetCorrupt) {
		t.Fatalf("Load(corrupt) error = %v, want ErrAssetCorrupt", err)
	}
	if img.Data != nil {
		t.Fatalf("Load(corrupt) returned %d bytes", len(img.Data))
	}
}

func testLoadBadRef(t *testing.T, s subject) {
	s.plant(t, "../x.png", pngBytes(t, 10))
	upper := strings.ToUpper(sha256Hex([]byte("x"))) + ".png"
	for _, ref := range []string{"../x.png", "abc.png", sha256Hex([]byte("x")) + ".bmp", upper, ""} {
		if _, err := s.store.Load(ref); !errors.Is(err, asset.ErrAssetRef) {
			t.Fatalf("Load(%q) error = %v, want ErrAssetRef", ref, err)
		}
	}
}

func testResolveUnique(t *testing.T, s subject) {
	ref := mustPut(t, s, pngBytes(t, 11), "image/png")
	got, err := s.store.Resolve(ref[:6])
	if err != nil {
		t.Fatalf("Resolve(%s): %v", ref[:6], err)
	}
	if got != ref {
		t.Fatalf("Resolve(%s) = %q, want %q", ref[:6], got, ref)
	}
}

func testResolveAmbiguous(t *testing.T, s subject) {
	a := "abcdef" + strings.Repeat("0", 58) + ".png"
	b := "abcdef" + strings.Repeat("1", 58) + ".jpg"
	other := "abcde0" + strings.Repeat("2", 58) + ".png"
	for _, name := range []string{b, a, other} {
		s.plant(t, name, []byte("crafted"))
	}
	_, err := s.store.Resolve("abcdef")
	var ambiguous *asset.AmbiguousHandleError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("Resolve(abcdef) error = %v, want *AmbiguousHandleError", err)
	}
	if ambiguous.Handle != "abcdef" || !slices.Equal(ambiguous.Refs, []string{a, b}) {
		t.Fatalf("ambiguous = %+v, want handle abcdef and refs %q", *ambiguous, []string{a, b})
	}
}

func testResolveUnknown(t *testing.T, s subject) {
	if _, err := s.store.Resolve("abcdef"); !errors.Is(err, asset.ErrUnknownHandle) {
		t.Fatalf("Resolve on an empty store error = %v, want ErrUnknownHandle", err)
	}
	ref := mustPut(t, s, pngBytes(t, 12), "image/png")
	miss := "0" + ref[1:6]
	if ref[0] == '0' {
		miss = "1" + ref[1:6]
	}
	if _, err := s.store.Resolve(miss); !errors.Is(err, asset.ErrUnknownHandle) {
		t.Fatalf("Resolve(%s) error = %v, want ErrUnknownHandle", miss, err)
	}
}

func testResolveBadHandle(t *testing.T, s subject) {
	mustPut(t, s, pngBytes(t, 13), "image/png")
	for _, handle := range []string{"ABCDEF", "abc", "abcdefg", "", "abcdeg"} {
		if _, err := s.store.Resolve(handle); !errors.Is(err, asset.ErrHandle) {
			t.Fatalf("Resolve(%q) error = %v, want ErrHandle", handle, err)
		}
	}
}

func testSweepRemovesDead(t *testing.T, s subject) {
	live := mustPut(t, s, pngBytes(t, 14), "image/png")
	mustPut(t, s, pngBytes(t, 15), "image/png")
	mustPut(t, s, gifBytes(t), "image/gif")
	n, err := s.store.Sweep(map[string]struct{}{live: {}})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 2 {
		t.Fatalf("Sweep removed %d, want 2", n)
	}
	wantNames(t, s, live)
}

func testSweepKeepsForeign(t *testing.T, s subject) {
	dead := mustPut(t, s, pngBytes(t, 16), "image/png")
	tmp := dead + "-3k2j1.tmp"
	s.plant(t, "README", []byte("hello"))
	s.plant(t, tmp, []byte("partial"))
	n, err := s.store.Sweep(nil)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("Sweep removed %d, want 1", n)
	}
	wantNames(t, s, "README", tmp)
}

func testSweepAllLive(t *testing.T, s subject) {
	a := mustPut(t, s, pngBytes(t, 17), "image/png")
	b := mustPut(t, s, jpegBytes(t), "image/jpeg")
	n, err := s.store.Sweep(map[string]struct{}{a: {}, b: {}})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 0 {
		t.Fatalf("Sweep removed %d, want 0", n)
	}
	wantNames(t, s, a, b)
}

func testSweepNoDir(t *testing.T, s subject) {
	n, err := s.store.Sweep(nil)
	if err != nil || n != 0 {
		t.Fatalf("Sweep on a never-written store = %d, %v; want 0, nil", n, err)
	}
	if s.dir == "" {
		return
	}
	if _, err := os.Stat(s.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Sweep created the store dir: stat error %v", err)
	}
}
