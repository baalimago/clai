package chat

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
)

const foreignIndexFileName = "foreign_index.cache"

// foreignIndexVersion discards every row of any other version, older or newer.
const foreignIndexVersion = 1

type foreignIndexCache struct {
	Version int               `json:"version"`
	Rows    []foreignIndexRow `json:"rows"`
}

type foreignIndexRow struct {
	Path    string            `json:"path"`
	Size    int64             `json:"size"`
	ModTime time.Time         `json:"mod_time"`
	Row     vendors.SourceRow `json:"row"`
}

// ForeignIndex is safe for concurrent use: one index serves every source
// reader of an invocation.
type ForeignIndex struct {
	dir string

	mu   sync.Mutex
	rows map[string]foreignIndexRow
	// used marks the rows this run read or wrote; only the rest face a stat.
	used map[string]struct{}
}

var _ vendors.SourceCache = (*ForeignIndex)(nil)

var newForeignIndex = NewForeignIndex

// DefaultForeignCache yields an untyped nil on failure: a nil *ForeignIndex
// behind a live interface would be taken as a cache and panicked on.
func DefaultForeignCache() vendors.SourceCache {
	dir, err := utils.GetClaiCacheDir()
	if err != nil {
		return nil
	}
	idx, err := newForeignIndex(dir)
	if err != nil {
		return nil
	}
	return idx
}

// NewForeignIndex treats a missing, corrupt or version-stale file as an empty
// index; only an unusable dir is an error.
func NewForeignIndex(dir string) (*ForeignIndex, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("foreign index requires a cache directory")
	}
	f := &ForeignIndex{
		dir:  dir,
		rows: map[string]foreignIndexRow{},
		used: map[string]struct{}{},
	}
	f.load()
	return f, nil
}

func (f *ForeignIndex) path() string { return path.Join(f.dir, foreignIndexFileName) }

// load is silent by design: a rescan is the pre-existing behaviour.
func (f *ForeignIndex) load() {
	if SkipIndex {
		return
	}
	b, err := os.ReadFile(f.path())
	if err != nil {
		return
	}
	var cache foreignIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		return
	}
	if cache.Version != foreignIndexVersion {
		return
	}
	for _, row := range cache.Rows {
		// A row with no path can never be validated against a file again.
		if row.Path == "" {
			continue
		}
		f.rows[row.Path] = row
	}
}

// Lookup answers only while the stored (size, mod time) still holds, and marks
// the row in use for this run.
func (f *ForeignIndex) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	if info == nil {
		return vendors.SourceRow{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[absPath]
	if !ok || row.Size != info.Size() || !row.ModTime.Equal(info.ModTime()) {
		return vendors.SourceRow{}, false
	}
	f.used[absPath] = struct{}{}
	return row.Row, true
}

// Store marks the row in use. A zero row records that absPath yields no
// session, so it is not rescanned every invocation.
func (f *ForeignIndex) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	if info == nil || absPath == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[absPath] = foreignIndexRow{
		Path:    absPath,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Row:     row,
	}
	f.used[absPath] = struct{}{}
}

// Locate does not touch the filesystem; the caller validates with Lookup. Two
// files may carry one identity, so the tie-break must be the walk's own order.
func (f *ForeignIndex) Locate(source, sourceID string) (string, bool) {
	if source == "" || sourceID == "" {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	found := ""
	for p, row := range f.rows {
		if row.Row.Source != source || row.Row.SourceID != sourceID {
			continue
		}
		if found == "" || compareWalkOrder(p, found) < 0 {
			found = p
		}
	}
	return found, found != ""
}

// Segment by segment, NOT whole-path byte order: "-" sorts below "/", so for
// siblings "proj" and "proj-bak" the walk reaches the smaller full path second.
func compareWalkOrder(a, b string) int {
	as := strings.Split(a, string(filepath.Separator))
	bs := strings.Split(b, string(filepath.Separator))
	for i := range min(len(as), len(bs)) {
		if c := strings.Compare(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// Persist writes the index back. Only SkipIndex suppresses it, never
// utils.NoCreateConfig, which is scoped to the config directory (D24).
func (f *ForeignIndex) Persist() error {
	if SkipIndex {
		return nil
	}
	rows := f.liveRows()
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create foreign index cache dir %q: %w", f.dir, err)
	}
	b, err := json.Marshal(foreignIndexCache{Version: foreignIndexVersion, Rows: rows})
	if err != nil {
		return fmt.Errorf("failed to encode foreign index cache: %w", err)
	}
	if err := utils.WriteFileAtomic(f.path(), b, 0o644); err != nil {
		return fmt.Errorf("failed to persist foreign index cache: %w", err)
	}
	return nil
}

func (f *ForeignIndex) liveRows() []foreignIndexRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]foreignIndexRow, 0, len(f.rows))
	for p, row := range f.rows {
		if _, marked := f.used[p]; !marked {
			if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
				delete(f.rows, p)
				continue
			}
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b foreignIndexRow) int { return strings.Compare(a.Path, b.Path) })
	return out
}
