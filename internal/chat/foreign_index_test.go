package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
	"github.com/baalimago/clai/internal/vendors/pi"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func newForeignIndexT(t *testing.T, dir string) *ForeignIndex {
	t.Helper()
	idx, err := NewForeignIndex(dir)
	if err != nil {
		t.Fatalf("NewForeignIndex(%q): %v", dir, err)
	}
	return idx
}

// persistT persists an index that must succeed. A listing tolerates a failed
// write, so a test that does not name the failure it expects must say so.
func persistT(t *testing.T, idx *ForeignIndex) {
	t.Helper()
	if err := idx.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
}

// touchFile writes a file with the given contents and returns its path and
// the stat discovery would have taken before reading it.
func touchFile(t *testing.T, dir, name, contents string) (string, fs.FileInfo) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %q: %v", p, err)
	}
	return p, info
}

func sampleRow(source, id, path string) vendors.SourceRow {
	return vendors.SourceRow{Source: source, SourceID: id, RawPath: path, MessageCount: 3}
}

func readForeignCacheFile(t *testing.T, dir string) foreignIndexCache {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, foreignIndexFileName))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache foreignIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	return cache
}

// writeRawCache puts arbitrary bytes where the index expects its file.
func writeRawCache(t *testing.T, dir string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, foreignIndexFileName), b, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

// TestForeignIndex_missingStartsEmpty: no cache file means an empty index, a
// full scan, and a cache written at the end of the run.
func TestForeignIndex_missingStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	idx := newForeignIndexT(t, dir)
	src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	if _, ok := idx.Lookup(src, info); ok {
		t.Fatal("a missing cache answered a lookup")
	}
	idx.Store(src, info, sampleRow("claude-code", "s1", src))
	persistT(t, idx)
	if got := len(readForeignCacheFile(t, dir).Rows); got != 1 {
		t.Fatalf("persisted %d rows, want 1", got)
	}
}

// TestForeignIndex_corruptFallsBackToScan: unparsable bytes are not an
// error, they are an empty index that is rewritten.
func TestForeignIndex_corruptFallsBackToScan(t *testing.T) {
	dir := t.TempDir()
	writeRawCache(t, dir, []byte("this is not json{{{"))
	var idx *ForeignIndex
	loadOut := testboil.CaptureStderr(t, func(t *testing.T) { idx = newForeignIndexT(t, dir) })
	if loadOut != "" {
		t.Fatalf("a corrupt cache surfaced %q on load; a foreign rescan is silent", loadOut)
	}
	if n := len(idx.rows); n != 0 {
		t.Fatalf("corrupt cache yielded %d rows, want 0", n)
	}
	src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	if _, ok := idx.Lookup(src, info); ok {
		t.Fatal("a corrupt cache answered a lookup")
	}
	idx.Store(src, info, sampleRow("claude-code", "s1", src))
	persistT(t, idx)
	cache := readForeignCacheFile(t, dir)
	if cache.Version != foreignIndexVersion || len(cache.Rows) != 1 {
		t.Fatalf("cache was not rewritten: %+v", cache)
	}
}

// TestForeignIndex_versionMismatchRebuilds: a row cached at another version
// is discarded, and the file is rewritten at the current version.
func TestForeignIndex_versionMismatchRebuilds(t *testing.T) {
	for _, version := range []int{0, foreignIndexVersion - 1, foreignIndexVersion + 1} {
		dir := t.TempDir()
		stale, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
		b, err := json.Marshal(foreignIndexCache{
			Version: version,
			Rows: []foreignIndexRow{{
				Path: stale, Size: info.Size(), ModTime: info.ModTime(),
				Row: sampleRow("claude-code", "old", stale),
			}},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		writeRawCache(t, dir, b)

		idx := newForeignIndexT(t, dir)
		if _, ok := idx.Lookup(stale, info); ok {
			t.Fatalf("version %d: a row of another version was trusted", version)
		}
		idx.Store(stale, info, sampleRow("claude-code", "new", stale))
		persistT(t, idx)
		cache := readForeignCacheFile(t, dir)
		if cache.Version != foreignIndexVersion {
			t.Fatalf("version %d: rewritten at %d, want %d", version, cache.Version, foreignIndexVersion)
		}
		if len(cache.Rows) != 1 || cache.Rows[0].Row.SourceID != "new" {
			t.Fatalf("version %d: rows = %+v, want only the freshly stored row", version, cache.Rows)
		}
	}
}

// TestForeignIndex_unreadableStartsEmpty: a directory where the cache file
// belongs cannot be read as a file, and must not fail the listing.
func TestForeignIndex_unreadableStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, foreignIndexFileName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var idx *ForeignIndex
	loadOut := testboil.CaptureStderr(t, func(t *testing.T) { idx = newForeignIndexT(t, dir) })
	if loadOut != "" {
		t.Fatalf("an unreadable cache surfaced %q on load", loadOut)
	}
	if n := len(idx.rows); n != 0 {
		t.Fatalf("unreadable cache yielded %d rows, want 0", n)
	}
}

// TestForeignIndex_undirectoryableParentReturnsError: the cache directory's
// parent is a file, so no directory can be made. Every attempt reports the
// failure to its caller, prints nothing itself, and leaves nothing partial
// behind. Whether the failure is announced is the caller's decision, pinned
// by TestChatHandler_foreignCacheAnnouncement.
func TestForeignIndex_undirectoryableParentReturnsError(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(parent, []byte("occupied"), 0o644); err != nil {
		t.Fatalf("write %q: %v", parent, err)
	}
	dir := filepath.Join(parent, "clai")
	idx := newForeignIndexT(t, dir)
	src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	idx.Store(src, info, sampleRow("claude-code", "s1", src))

	out := testboil.CaptureStderr(t, func(t *testing.T) {
		for i := range 2 {
			if err := idx.Persist(); err == nil {
				t.Errorf("persist %d returned no error for an unmakeable cache dir", i)
			}
		}
	})
	if out != "" {
		t.Fatalf("the index printed %q; announcing is the caller's decision", out)
	}
	b, err := os.ReadFile(parent)
	if err != nil {
		t.Fatalf("read %q: %v", parent, err)
	}
	if string(b) != "occupied" {
		t.Fatalf("the blocking file was modified: %q", b)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("a partial artifact was left behind: %+v", entries)
	}
}

// TestForeignIndex_noCacheDirDisablesCaching: an unresolvable cache
// directory yields an error, never an unusable value, so the caller leaves
// the field a nil interface and discovery scans.
func TestForeignIndex_noCacheDirDisablesCaching(t *testing.T) {
	for _, dir := range []string{"", "   "} {
		idx, err := NewForeignIndex(dir)
		if err == nil {
			t.Fatalf("NewForeignIndex(%q) returned %v, want an error", dir, idx)
		}
		if idx != nil {
			t.Fatalf("NewForeignIndex(%q) returned a value alongside its error", dir)
		}
	}

	// The caller's own guard: a failed construction must leave a nil
	// interface, not an interface holding a nil pointer.
	cq, _ := newTestHandler(t)
	if cq.foreignCache != nil {
		t.Fatal("a handler with no injected cache must hold a nil interface")
	}
	root := t.TempDir()
	jsonltest.WriteCorpus(t, root, smallCorpus(jsonltest.ShapeClaude))
	rows, err := cq.foreignChatRows(context.Background(),
		[]vendors.SourceReader{anthropic.SourceReader{Root: root}}, map[string]struct{}{})
	if err != nil {
		t.Fatalf("foreignChatRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("an uncached listing produced no rows")
	}
}

// TestForeignIndex_fileVanishesMidRun: a cached row whose file is gone by
// the time the index is persisted can never be validated again, so it is
// dropped without an error.
func TestForeignIndex_fileVanishesMidRun(t *testing.T) {
	dir := t.TempDir()
	srcDir := t.TempDir()
	gone, info := touchFile(t, srcDir, "gone.jsonl", "{}\n")

	idx := newForeignIndexT(t, dir)
	idx.Store(gone, info, sampleRow("claude-code", "s1", gone))
	persistT(t, idx)

	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// A new run: the walk never reaches the file, so the row is untouched.
	next := newForeignIndexT(t, dir)
	if n := len(next.rows); n != 1 {
		t.Fatalf("loaded %d rows, want the row cached by the previous run", n)
	}
	persistT(t, next)
	if got := readForeignCacheFile(t, dir).Rows; len(got) != 0 {
		t.Fatalf("rows = %+v, want the vanished file's row pruned", got)
	}
}

// TestForeignIndex_prunesVanishedFiles executes the liveness table: read,
// written, and untouched-but-present rows survive; only an untouched row
// whose file is gone is dropped.
func TestForeignIndex_prunesVanishedFiles(t *testing.T) {
	dir := t.TempDir()
	srcDir := t.TempDir()
	read, readInfo := touchFile(t, srcDir, "read.jsonl", "{}\n")
	written, writtenInfo := touchFile(t, srcDir, "written.jsonl", "{}\n")
	untouched, untouchedInfo := touchFile(t, srcDir, "untouched.jsonl", "{}\n")
	vanished, vanishedInfo := touchFile(t, srcDir, "vanished.jsonl", "{}\n")

	seed := newForeignIndexT(t, dir)
	for _, p := range []struct {
		path string
		info fs.FileInfo
	}{{read, readInfo}, {written, writtenInfo}, {untouched, untouchedInfo}, {vanished, vanishedInfo}} {
		seed.Store(p.path, p.info, sampleRow("claude-code", filepath.Base(p.path), p.path))
	}
	persistT(t, seed)

	if err := os.Remove(vanished); err != nil {
		t.Fatalf("remove: %v", err)
	}

	idx := newForeignIndexT(t, dir)
	if _, ok := idx.Lookup(read, readInfo); !ok {
		t.Fatal("the seeded row was not readable")
	}
	idx.Store(written, writtenInfo, sampleRow("claude-code", "rewritten", written))
	persistT(t, idx)

	kept := map[string]bool{}
	for _, row := range readForeignCacheFile(t, dir).Rows {
		kept[row.Path] = true
	}
	for _, want := range []string{read, written, untouched} {
		if !kept[want] {
			t.Errorf("row for %q was pruned; only a vanished untouched row may be", want)
		}
	}
	if kept[vanished] {
		t.Error("the deleted file's row was resurrected")
	}
}

// TestForeignIndex_persistWithoutLookups writes a valid empty cache rather
// than no file at all, so the next run reads a current-version index.
func TestForeignIndex_persistWithoutLookups(t *testing.T) {
	dir := t.TempDir()
	idx := newForeignIndexT(t, dir)
	persistT(t, idx)
	cache := readForeignCacheFile(t, dir)
	if cache.Version != foreignIndexVersion {
		t.Fatalf("version = %d, want %d", cache.Version, foreignIndexVersion)
	}
	if len(cache.Rows) != 0 {
		t.Fatalf("rows = %+v, want none", cache.Rows)
	}
}

// TestForeignIndex_rowWithEmptyPathDropped: a row with no path can never be
// validated against a file, so it is dropped at load.
func TestForeignIndex_rowWithEmptyPathDropped(t *testing.T) {
	dir := t.TempDir()
	keep, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	b, err := json.Marshal(foreignIndexCache{
		Version: foreignIndexVersion,
		Rows: []foreignIndexRow{
			{Path: "", Size: 1, Row: sampleRow("claude-code", "ghost", "")},
			{Path: keep, Size: info.Size(), ModTime: info.ModTime(), Row: sampleRow("claude-code", "kept", keep)},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeRawCache(t, dir, b)

	idx := newForeignIndexT(t, dir)
	if n := len(idx.rows); n != 1 {
		t.Fatalf("loaded %d rows, want only the one with a path", n)
	}
	if _, ok := idx.Locate("claude-code", "ghost"); ok {
		t.Fatal("the path-less row is reachable")
	}
	if p, ok := idx.Locate("claude-code", "kept"); !ok || p != keep {
		t.Fatalf("Locate(kept) = %q, %v, want %q, true", p, ok, keep)
	}
}

// TestForeignIndex_createsMissingCacheDir: nothing else in clai creates the
// cache directory, so the index must, or a fresh machine never caches.
func TestForeignIndex_createsMissingCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent", "clai")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("fixture directory already exists: %v", err)
	}
	idx := newForeignIndexT(t, dir)
	src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	idx.Store(src, info, sampleRow("claude-code", "s1", src))
	persistT(t, idx)

	reloaded := newForeignIndexT(t, dir)
	row, ok := reloaded.Lookup(src, info)
	if !ok {
		t.Fatal("the cache written into a fresh directory is not usable")
	}
	if row.SourceID != "s1" {
		t.Fatalf("SourceID = %q, want s1", row.SourceID)
	}
}

// TestForeignIndex_concurrentAccess exercises the mutex: one index serves
// every reader of an invocation, and phase 3 will call it from a pool.
func TestForeignIndex_concurrentAccess(t *testing.T) {
	dir := t.TempDir()
	srcDir := t.TempDir()
	idx := newForeignIndexT(t, dir)

	paths := make([]string, 8)
	infos := make([]fs.FileInfo, 8)
	for i := range paths {
		paths[i], infos[i] = touchFile(t, srcDir, filepath.Base(t.TempDir())+".jsonl", "{}\n")
	}

	var wg sync.WaitGroup
	for i := range paths {
		wg.Add(3)
		go func() {
			defer wg.Done()
			idx.Store(paths[i], infos[i], sampleRow("claude-code", filepath.Base(paths[i]), paths[i]))
		}()
		go func() {
			defer wg.Done()
			idx.Lookup(paths[i], infos[i])
		}()
		go func() {
			defer wg.Done()
			idx.Locate("claude-code", filepath.Base(paths[i]))
		}()
	}
	wg.Wait()
	persistT(t, idx)
	if got := len(readForeignCacheFile(t, dir).Rows); got != len(paths) {
		t.Fatalf("persisted %d rows, want %d", got, len(paths))
	}
}

// TestForeignIndex_readOnlyDegradesSilently: read-only mode still writes the
// cache on a writable machine — the flag is scoped to the config directory
// and this cache lives in the cache directory (D24). Where the write
// genuinely cannot happen the listing proceeds, and the failure is silent on
// a raw run only: utils.ReadonlyConfig is what marks the shell hook or script
// that must see no stderr, while utils.NoCreateConfig is set for every
// listing and so cannot be the gate (D25).
func TestForeignIndex_readOnlyDegradesSilently(t *testing.T) {
	old := utils.NoCreateConfig
	t.Cleanup(func() { utils.NoCreateConfig = old })
	utils.NoCreateConfig = true
	// Provokes the failure the three subtests below judge differently.
	undirectoryable := func(t *testing.T) (string, string) {
		t.Helper()
		tmp := t.TempDir()
		parent := filepath.Join(tmp, "not-a-dir")
		if err := os.WriteFile(parent, []byte("occupied"), 0o644); err != nil {
			t.Fatalf("write %q: %v", parent, err)
		}
		return tmp, filepath.Join(parent, "clai")
	}
	// announce hands a persist result to the verb boundary that owns the
	// decision, twice, so "at most once" is asserted alongside the gate.
	announce := func(t *testing.T, err error) string {
		t.Helper()
		cq, _ := newTestHandler(t)
		warnings := &bytes.Buffer{}
		cq.errOut = warnings
		cq.announceForeignCacheFailure(err)
		cq.announceForeignCacheFailure(err)
		return warnings.String()
	}
	stored := func(t *testing.T, dir string) (*ForeignIndex, string, fs.FileInfo) {
		t.Helper()
		idx := newForeignIndexT(t, dir)
		src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
		idx.Store(src, info, sampleRow("claude-code", "s1", src))
		return idx, src, info
	}

	t.Run("writable cache dir still persists", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clai")
		idx, _, _ := stored(t, dir)
		if err := idx.Persist(); err != nil {
			t.Fatalf("read-only mode suppressed a cache-dir write: %v", err)
		}
		if got := len(readForeignCacheFile(t, dir).Rows); got != 1 {
			t.Fatalf("persisted %d rows, want 1: read-only mode suppressed a cache-dir write", got)
		}
		if got := announce(t, nil); got != "" {
			t.Fatalf("a successful read-only persist surfaced %q", got)
		}
	})

	t.Run("unwritable cache dir lists in silence on a raw run", func(t *testing.T) {
		oldRaw := utils.ReadonlyConfig
		t.Cleanup(func() { utils.ReadonlyConfig = oldRaw })
		utils.ReadonlyConfig = true
		tmp, dir := undirectoryable(t)
		idx, src, info := stored(t, dir)

		err := idx.Persist()
		if err == nil {
			t.Fatal("persisting into an unmakeable cache dir reported success")
		}
		if got := announce(t, err); got != "" {
			t.Fatalf("a raw run surfaced %q; a shell-prompt hook must stay quiet", got)
		}
		entries, err := os.ReadDir(tmp)
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("a partial artifact was left behind: %+v", entries)
		}
		// The in-memory row still serves this invocation.
		if _, ok := idx.Lookup(src, info); !ok {
			t.Fatal("the row stored this run was lost")
		}
	})

	t.Run("no-create-config alone does not silence the failure", func(t *testing.T) {
		oldRaw := utils.ReadonlyConfig
		t.Cleanup(func() { utils.ReadonlyConfig = oldRaw })
		utils.ReadonlyConfig = false
		_, dir := undirectoryable(t)
		idx, _, _ := stored(t, dir)

		err := idx.Persist()
		if err == nil {
			t.Fatal("persisting into an unmakeable cache dir reported success")
		}
		if n := strings.Count(announce(t, err), "warning:"); n != 1 {
			t.Fatalf("got %d warnings, want exactly 1: an interactive listing sets NoCreateConfig and must still be told its cache cannot be written", n)
		}
	})
}

// TestForeignIndex_skipIndexNoIO: embedded consumers read nothing and write
// nothing, exactly as they do for the native index.
func TestForeignIndex_skipIndexNoIO(t *testing.T) {
	dir := t.TempDir()
	src, info := touchFile(t, t.TempDir(), "s.jsonl", "{}\n")
	b, err := json.Marshal(foreignIndexCache{
		Version: foreignIndexVersion,
		Rows: []foreignIndexRow{{
			Path: src, Size: info.Size(), ModTime: info.ModTime(),
			Row: sampleRow("claude-code", "s1", src),
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeRawCache(t, dir, b)
	before, err := os.Stat(filepath.Join(dir, foreignIndexFileName))
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}

	old := SkipIndex
	t.Cleanup(func() { SkipIndex = old })
	SkipIndex = true

	var idx *ForeignIndex
	out := testboil.CaptureStderr(t, func(t *testing.T) {
		idx = newForeignIndexT(t, dir)
		if n := len(idx.rows); n != 0 {
			t.Errorf("SkipIndex read %d rows", n)
		}
		idx.Store(src, info, sampleRow("claude-code", "s1", src))
		persistT(t, idx)
	})
	if out != "" {
		t.Fatalf("SkipIndex surfaced %q", out)
	}

	after, err := os.Stat(filepath.Join(dir, foreignIndexFileName))
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("SkipIndex rewrote the cache file")
	}
}

// smallCorpus keeps every corpus in this file at fixture scale.
func smallCorpus(shape jsonltest.Shape) jsonltest.CorpusOptions {
	return jsonltest.CorpusOptions{
		Shape:           shape,
		Sessions:        jsonltest.MinSessions + 2,
		LinesPerSession: 12,
	}
}

// TestForeignIndex_unchangedCorpusSurvivesSecondRun is the end-to-end shape
// of the feature: two real readers, one persisted index, and a corpus nobody
// touched between runs. The second and third listings open no session file
// at all, and every row is identical to the scanned one.
func TestForeignIndex_unchangedCorpusSurvivesSecondRun(t *testing.T) {
	claudeRoot := filepath.Join(t.TempDir(), "projects")
	piRoot := filepath.Join(t.TempDir(), "sessions")
	claudeCorpus := jsonltest.WriteCorpus(t, claudeRoot, smallCorpus(jsonltest.ShapeClaude))
	piCorpus := jsonltest.WriteCorpus(t, piRoot, smallCorpus(jsonltest.ShapePi))
	totalFiles := len(claudeCorpus.Files) + len(piCorpus.Files)
	dir := t.TempDir()

	discover := func(idx *ForeignIndex, fsys fs.FS) []vendors.SourceRow {
		t.Helper()
		out := []vendors.SourceRow{}
		readers := []vendors.SourceReader{
			anthropic.SourceReader{Root: claudeRoot, FS: fsys},
			pi.SourceReader{Root: piRoot, FS: fsys},
		}
		for _, r := range readers {
			rows, err := r.Discover(context.Background(), idx)
			if err != nil {
				t.Fatalf("Discover(%v): %v", r.Source(), err)
			}
			out = append(out, rows...)
		}
		return out
	}

	first := newForeignIndexT(t, dir)
	want := discover(first, nil)
	if len(want) == 0 {
		t.Fatal("no rows; the corpora did not reach the readers")
	}
	persistT(t, first)

	for run := 2; run <= 3; run++ {
		idx := newForeignIndexT(t, dir)
		fsys, counts := jsonltest.CountingFS("/")
		got := discover(idx, fsys)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d rows differ from the scanned rows:\n got %+v\nwant %+v", run, got, want)
		}
		if n := counts.TotalOpens(); n != 0 {
			t.Fatalf("run %d opened %d session files, want 0: %v", run, n, counts.OpenedPaths())
		}
		if n := counts.TotalStats(); n != totalFiles {
			t.Fatalf("run %d stat'ed %d times, want one per file (%d)", run, n, totalFiles)
		}
		persistT(t, idx)
		if got := len(readForeignCacheFile(t, dir).Rows); got != totalFiles {
			t.Fatalf("run %d persisted %d rows, want one per file (%d)", run, got, totalFiles)
		}
	}
}

// --- an error is not a fact about the file (phase 5) -------------------------

// unreachableDir makes dir unreadable and unsearchable for the rest of the
// test, so os.Stat on anything inside it fails with a permission error rather
// than with "no such file". The mode is restored in cleanup, or t.TempDir
// could not remove the tree.
func unreachableDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so a non-absent stat failure is unreachable")
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %q: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// TestForeignIndex_statFailureKeepsRow: the prune stat answers one question
// only — is this file gone? A permission error says nothing about the file's
// existence, so the row stays (R1-04). It is the same rule as R1-01, one
// layer up: an error is a fact about this run.
func TestForeignIndex_statFailureKeepsRow(t *testing.T) {
	dir := t.TempDir()
	srcRoot := t.TempDir()
	blocked := filepath.Join(srcRoot, "blocked")
	unreadable, unreadableInfo := touchFile(t, blocked, "s.jsonl", "{}\n")
	gone, goneInfo := touchFile(t, srcRoot, "gone.jsonl", "{}\n")

	seed := newForeignIndexT(t, dir)
	seed.Store(unreadable, unreadableInfo, sampleRow("claude-code", "blocked", unreadable))
	seed.Store(gone, goneInfo, sampleRow("claude-code", "gone", gone))
	persistT(t, seed)

	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove %q: %v", gone, err)
	}
	unreachableDir(t, blocked)

	// A second run that reaches neither file: nothing is marked in use, so
	// both rows face the prune stat.
	idx := newForeignIndexT(t, dir)
	persistT(t, idx)

	kept := map[string]bool{}
	for _, row := range readForeignCacheFile(t, dir).Rows {
		kept[row.Path] = true
	}
	if !kept[unreadable] {
		t.Error("a row was pruned on a permission error; the file still exists and its scan is still valid")
	}
	if kept[gone] {
		t.Error("a row whose file the filesystem reported absent was kept")
	}
}

// TestForeignIndex_unreachableRootKeepsEveryRow: a source root that could not
// be walked this run leaves every row unmarked and every stat failing. The
// cache must survive intact, or one unreachable mount empties it and the next
// good run rescans the whole foreign corpus.
func TestForeignIndex_unreachableRootKeepsEveryRow(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(t.TempDir(), "projects")
	want := []string{}
	for _, name := range []string{"a.jsonl", "b.jsonl", "c.jsonl", "d.jsonl"} {
		p, info := touchFile(t, root, name, "{}\n")
		seedRow := sampleRow("claude-code", name, p)
		idx := newForeignIndexT(t, dir)
		idx.Store(p, info, seedRow)
		persistT(t, idx)
		want = append(want, p)
	}
	unreachableDir(t, root)

	idx := newForeignIndexT(t, dir)
	persistT(t, idx)

	got := []string{}
	for _, row := range readForeignCacheFile(t, dir).Rows {
		got = append(got, row.Path)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("an unreachable root cost rows:\n got %v\nwant %v", got, want)
	}
}

// TestForeignIndex_locateReturnsLexicallySmallestPath: Locate used to range a
// map, so two rows sharing a source and session identifier answered
// differently per run and chat continue opened a different transcript each
// time (R1-05). The tie-break is now the walk's own.
func TestForeignIndex_locateReturnsLexicallySmallestPath(t *testing.T) {
	dir := t.TempDir()
	srcDir := t.TempDir()
	idx := newForeignIndexT(t, dir)
	paths := []string{}
	for _, name := range []string{"m.jsonl", "a.jsonl", "z.jsonl", "b.jsonl"} {
		p, info := touchFile(t, srcDir, name, "{}\n")
		idx.Store(p, info, sampleRow("claude-code", "shared", p))
		paths = append(paths, p)
	}
	// One row of another source with the same identifier must not be chosen
	// however small its path sorts.
	other, otherInfo := touchFile(t, srcDir, "A-other.jsonl", "{}\n")
	idx.Store(other, otherInfo, sampleRow("pi", "shared", other))

	slices.Sort(paths)
	want := paths[0]
	// Map iteration order is randomized per range, so a map-order answer
	// fails this within a few rounds.
	for i := range 64 {
		got, ok := idx.Locate("claude-code", "shared")
		if !ok {
			t.Fatalf("round %d: Locate found nothing", i)
		}
		if got != want {
			t.Fatalf("round %d: Locate = %q, want the lexically smallest match %q", i, got, want)
		}
	}
}

// TestForeignIndex_locateAgreesWithWalkFallback drives the same tie-break
// through the real reader: two files carry one session identity, and the
// answer the index gives must be the file the cache-less walk would have
// opened — on every run.
func TestForeignIndex_locateAgreesWithWalkFallback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	const id = "duplicated-session"
	line := func(text string) string {
		b, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": id, "timestamp": "2026-01-02T03:04:05Z",
			"message": map[string]any{"content": text},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b) + "\n"
	}
	// The lexically first path holds a different message, so which file was
	// opened is visible in the conversation that comes back.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, text := range map[string]string{
		"aaa-first.jsonl": "the walk would pick me",
		"zzz-last.jsonl":  "the map might pick me",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(line(text)), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}

	reader := anthropic.SourceReader{Root: root}
	walked, err := reader.Read(context.Background(), nil, id)
	if err != nil {
		t.Fatalf("cache-less Read: %v", err)
	}

	dir := t.TempDir()
	idx := newForeignIndexT(t, dir)
	if _, err := reader.Discover(context.Background(), idx); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for i := range 32 {
		got, err := reader.Read(context.Background(), idx, id)
		if err != nil {
			t.Fatalf("round %d: cached Read: %v", i, err)
		}
		if !reflect.DeepEqual(got.Messages, walked.Messages) {
			t.Fatalf("round %d: the index opened a different transcript than the walk:\n got %+v\nwant %+v",
				i, got.Messages, walked.Messages)
		}
	}
}

// --- review two: Locate ranks by walk order (R2-02) -------------------------

// walkedJSONLPaths returns every *.jsonl file under root in the order
// WalkJSONLFiles visits them. It is the oracle Locate's tie-break is held to:
// the walk, not a sort of this test's own choosing.
func walkedJSONLPaths(t *testing.T, root string) []string {
	t.Helper()
	out := []string{}
	if err := vendors.WalkJSONLFiles(context.Background(), root, nil, func(p string) bool {
		out = append(out, p)
		return false
	}); err != nil {
		t.Fatalf("WalkJSONLFiles(%q): %v", root, err)
	}
	return out
}

// TestForeignIndex_locateFollowsWalkOrder: filepath.WalkDir orders entries
// within each directory, so for sibling directories whose names differ by a
// suffix the walk reaches the shorter name first while the longer one holds
// the lexically smaller full path — the hyphen sorts below the separator.
// Locate must answer with the file the walk would have opened (R2-02).
func TestForeignIndex_locateFollowsWalkOrder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	idx := newForeignIndexT(t, t.TempDir())
	paths := []string{}
	for _, project := range []string{"proj", "proj-bak", "proj.old"} {
		p, info := touchFile(t, filepath.Join(root, project), "s.jsonl", "{}\n")
		idx.Store(p, info, sampleRow("claude-code", "shared", p))
		paths = append(paths, p)
	}
	walked := walkedJSONLPaths(t, root)
	if len(walked) != len(paths) {
		t.Fatalf("the walk saw %d files, want %d", len(walked), len(paths))
	}
	want := walked[0]

	lexical := slices.Clone(paths)
	slices.Sort(lexical)
	if lexical[0] == want {
		t.Fatalf("the fixture does not separate the two orderings: both name %q", want)
	}

	for i := range 32 {
		got, ok := idx.Locate("claude-code", "shared")
		if !ok {
			t.Fatalf("round %d: Locate found nothing", i)
		}
		if got != want {
			t.Fatalf("round %d: Locate = %q, want the file the walk reaches first, %q (the lexically smallest full path is %q)",
				i, got, want, lexical[0])
		}
	}

	// The ranking is a total order, including the tail rule a cached path
	// from another layout can still reach: a shorter path sorts first.
	t.Run("the ranking is a total order", func(t *testing.T) {
		for _, tc := range []struct {
			a, b string
			want int
		}{
			{"/r/proj/s.jsonl", "/r/proj-bak/s.jsonl", -1},
			{"/r/proj-bak/s.jsonl", "/r/proj/s.jsonl", 1},
			{"/r/proj/s.jsonl", "/r/proj/s.jsonl", 0},
			{"/r/proj", "/r/proj/s.jsonl", -1},
			{"/r/proj/s.jsonl", "/r/proj", 1},
		} {
			if got := compareWalkOrder(tc.a, tc.b); got != tc.want {
				t.Errorf("compareWalkOrder(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		}
	})
}

// TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs proves the
// disagreement end to end: two files carry one session identity in sibling
// project directories, and `chat continue` must open the same transcript
// whether or not the cache is warm. Claude Code's own layout produces this —
// the cwd slug replaces separators with hyphens, so a worktree, a backup or a
// renamed checkout is a suffix-related sibling.
func TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	const id = "duplicated-session"
	line := func(text string) string {
		b, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": id, "timestamp": "2026-01-02T03:04:05Z",
			"message": map[string]any{"content": text},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b) + "\n"
	}
	// The walk reaches "proj" first; "proj-bak" holds the lexically smaller
	// full path. Which file was opened is visible in the message that returns.
	for project, text := range map[string]string{
		"proj":     "the walk would pick me",
		"proj-bak": "the lexical tie-break would pick me",
	} {
		dir := filepath.Join(root, project)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line(text)), 0o644); err != nil {
			t.Fatalf("write %q: %v", project, err)
		}
	}

	reader := anthropic.SourceReader{Root: root}
	walked, err := reader.Read(context.Background(), nil, id)
	if err != nil {
		t.Fatalf("cache-less Read: %v", err)
	}

	idx := newForeignIndexT(t, t.TempDir())
	if _, err := reader.Discover(context.Background(), idx); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for i := range 32 {
		got, err := reader.Read(context.Background(), idx, id)
		if err != nil {
			t.Fatalf("round %d: cached Read: %v", i, err)
		}
		if !reflect.DeepEqual(got.Messages, walked.Messages) {
			t.Fatalf("round %d: the warm index opened a different transcript than the walk:\n got %+v\nwant %+v",
				i, got.Messages, walked.Messages)
		}
	}
}

// TestDefaultForeignCache_failureYieldsNilInterface: an interface holding a
// nil *ForeignIndex is not nil, so discovery would take the cached branch and
// panic instead of scanning. The guard in setChatQuerier cannot see the
// difference, so the factory has to be right.
func TestDefaultForeignCache_failureYieldsNilInterface(t *testing.T) {
	t.Run("a failed construction yields a nil interface", func(t *testing.T) {
		previous := newForeignIndex
		t.Cleanup(func() { newForeignIndex = previous })
		t.Setenv("CLAI_CACHE_DIR", t.TempDir())
		newForeignIndex = func(string) (*ForeignIndex, error) {
			return nil, errors.New("no cache directory")
		}
		if cache := DefaultForeignCache(); cache != nil {
			t.Fatalf("the factory yielded %#v, want a nil interface", cache)
		}
	})

	t.Run("an unresolvable cache dir constructs nothing", func(t *testing.T) {
		previous := newForeignIndex
		t.Cleanup(func() { newForeignIndex = previous })
		t.Setenv("CLAI_CACHE_DIR", "")
		t.Setenv("HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		newForeignIndex = func(dir string) (*ForeignIndex, error) {
			t.Errorf("the index was constructed for %q although the cache dir is unresolvable", dir)
			return nil, errors.New("unreachable")
		}
		if cache := DefaultForeignCache(); cache != nil {
			t.Fatalf("an unresolvable cache dir yielded %#v, want a nil interface", cache)
		}
	})
}
