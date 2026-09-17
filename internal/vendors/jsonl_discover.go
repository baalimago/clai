package vendors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"sync"
)

// jsonl_discover.go owns the discovery algorithm every JSONL-backed source
// shares: walk, scan, aggregate one SourceRow per file, and resolve a session
// identifier back to its file.

const (
	// sourcePreviewRunes is the width of the one-line list preview.
	sourcePreviewRunes   = 100
	sourceMissingPreview = "(no preview)"
)

// discoverWorkers bounds how many files DiscoverJSONL scans at once. It is a
// variable so a test can reach its degenerate values (D20).
var discoverWorkers = func() int { return min(8, runtime.NumCPU()) }

// discoverSlot lets a worker write its own index rather than append, so the
// walk's order survives concurrency without a lock.
type discoverSlot struct {
	row SourceRow
	ok  bool
}

// DiscoverJSONL builds one SourceRow per discoverable session file under the
// schema's root. A file that yields no identity, or that cannot be read to its
// end, is dropped rather than failing the walk; a nil cache scans everything.
// Only the scan is concurrent, so the result equals a serial discovery.
func DiscoverJSONL(ctx context.Context, schema JSONLSchema, fsys fs.FS, cache SourceCache) ([]SourceRow, error) {
	paths, err := walkJSONLPaths(ctx, schema)
	if err != nil {
		return nil, fmt.Errorf("discover %v sessions: %w", schema.SourceName(), err)
	}
	slots := make([]discoverSlot, len(paths))
	scanJSONLFileSlots(ctx, schema, fsys, cache, paths, slots)
	// A partial list is worse than none, because nothing says it is partial.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover %v sessions: %w", schema.SourceName(), err)
	}
	rows := make([]SourceRow, 0, len(slots))
	for _, slot := range slots {
		if slot.ok {
			rows = append(rows, slot.row)
		}
	}
	return rows, nil
}

func walkJSONLPaths(ctx context.Context, schema JSONLSchema) ([]string, error) {
	paths := []string{}
	err := WalkJSONLFiles(ctx, schema.Root(), schema.SkipDirs(), func(absPath string) bool {
		paths = append(paths, absPath)
		return false
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func scanJSONLFileSlots(ctx context.Context, schema JSONLSchema, fsys fs.FS, cache SourceCache, paths []string, slots []discoverSlot) {
	if len(paths) == 0 {
		return
	}
	workers := min(max(discoverWorkers(), 1), len(paths))
	work := make(chan int)
	wg := sync.WaitGroup{}
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range work {
				if ctx.Err() != nil {
					continue
				}
				slots[i].row, slots[i].ok = discoverJSONLFile(schema, fsys, cache, paths[i])
			}
		}()
	}
feed:
	for i := range paths {
		select {
		case work <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
}

// discoverJSONLFile takes the fs.FileInfo BEFORE the read, and takes only that
// one, so a file appended to mid-scan is recorded under the state actually read
// and is rescanned next run (D15).
func discoverJSONLFile(schema JSONLSchema, fsys fs.FS, cache SourceCache, absPath string) (SourceRow, bool) {
	info, statErr := StatAbs(fsys, absPath)
	cacheable := cache != nil && statErr == nil
	if cacheable {
		if row, ok := cache.Lookup(absPath, info); ok {
			// A cached zero row is the record that this file yields nothing.
			return row, row.SourceID != ""
		}
	}
	row, ok, err := scanJSONLFileRow(schema, fsys, absPath, info)
	// A failed read is a fact about this run, and caching it would strand the
	// file at whatever prefix was read. An oversized line is a fact about the
	// bytes, which (size, mtime) invalidates, so it stays cacheable (D28).
	if err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return SourceRow{}, false
	}
	if cacheable {
		cache.Store(absPath, info, row)
	}
	return row, ok
}

// A non-nil error means the scan did not reach EOF; (SourceRow{}, false, nil)
// means it did and the file names no session.
func scanJSONLFileRow(schema JSONLSchema, fsys fs.FS, absPath string, info fs.FileInfo) (SourceRow, bool, error) {
	f, err := OpenAbs(fsys, absPath)
	if err != nil {
		return SourceRow{}, false, err
	}
	defer f.Close()

	row := SourceRow{Source: schema.SourceName(), RawPath: absPath}
	// No prefilter means every line is decoded: correct, and slower (D18).
	prefilter, _ := schema.(LinePrefilter)
	scanErr := scanJSONLRawLines(f, ReadMaxToken, func(line []byte) bool {
		if prefilter != nil && !prefilter.MayContribute(line) {
			return true
		}
		applyLineFields(&row, schema.Fields(line))
		return true
	})
	if row.SourceID == "" {
		return SourceRow{}, false, scanErr
	}
	if row.Created.IsZero() && info != nil {
		row.Created = info.ModTime()
	}
	if row.FirstUserMessage == "" {
		row.FirstUserMessage = sourceMissingPreview
	}
	return row, true, scanErr
}

func applyLineFields(row *SourceRow, lf LineFields) {
	if lf.Role == LineRoleSkip {
		return
	}
	if row.SourceID == "" && lf.SessionID != "" {
		row.SourceID = lf.SessionID
	}
	if row.Cwd == "" && lf.Cwd != "" {
		row.Cwd = lf.Cwd
	}
	if row.Created.IsZero() && !lf.Timestamp.IsZero() {
		row.Created = lf.Timestamp
	}
	if row.Model == "" && lf.Model != "" {
		row.Model = lf.Model
	}
	switch lf.Role {
	case LineRoleUser, LineRoleAssistant, LineRoleTool:
		row.MessageCount++
	case LineRoleNone, LineRoleSkip:
	}
	// A user line with no extractable text still counts, but cannot preview:
	// Claude emits every tool result as a user line.
	if lf.Role == LineRoleUser && row.FirstUserMessage == "" {
		row.FirstUserMessage = TruncateOneLine(lf.UserText, sourcePreviewRunes)
		row.FullFirstUserMessage = lf.UserText
	}
}

// FindJSONLSession returns the path of the file holding sourceID, from the
// cache in one stat or else by a walk that stops at each file's first
// identity line.
func FindJSONLSession(ctx context.Context, schema JSONLSchema, fsys fs.FS, cache SourceCache, sourceID string) (string, error) {
	if sourceID == "" {
		return "", fmt.Errorf("find %v session: empty session id", schema.SourceName())
	}
	root := schema.Root()
	if root == "" {
		return "", fmt.Errorf("%v root not configured", schema.SourceName())
	}
	if p, ok := cachedSessionPath(schema, fsys, cache, sourceID); ok {
		return p, nil
	}
	var found string
	var readErr error
	err := WalkJSONLFiles(ctx, root, schema.SkipDirs(), func(absPath string) bool {
		identity, idErr := jsonlFileIdentity(schema, fsys, absPath)
		// Walking on would answer with a file the walk would not have chosen.
		if idErr != nil {
			readErr = fmt.Errorf("read %q: %w", absPath, idErr)
			return true
		}
		if identity == sourceID {
			found = absPath
			return true
		}
		return false
	})
	if err != nil {
		return "", fmt.Errorf("find %v session: %w", schema.SourceName(), err)
	}
	if readErr != nil {
		return "", fmt.Errorf("find %v session: %w", schema.SourceName(), readErr)
	}
	if found == "" {
		return "", fmt.Errorf("%v session %q not found", schema.SourceName(), sourceID)
	}
	return found, nil
}

// cachedSessionPath never writes: an identity is not a discovered row.
func cachedSessionPath(schema JSONLSchema, fsys fs.FS, cache SourceCache, sourceID string) (string, bool) {
	if cache == nil {
		return "", false
	}
	absPath, ok := cache.Locate(schema.SourceName(), sourceID)
	if !ok || absPath == "" {
		return "", false
	}
	info, err := StatAbs(fsys, absPath)
	if err != nil {
		return "", false
	}
	row, ok := cache.Lookup(absPath, info)
	if !ok || row.SourceID != sourceID {
		return "", false
	}
	return absPath, true
}

// A non-nil error means the file could not be read as far as its identity
// line; ("", nil) means it was, and names no session. A refused open stays a
// skip, as the walk has always tolerated (D17).
func jsonlFileIdentity(schema JSONLSchema, fsys fs.FS, absPath string) (string, error) {
	f, err := OpenAbs(fsys, absPath)
	if err != nil {
		return "", nil
	}
	defer f.Close()

	identity := ""
	scanErr := scanJSONLRawLines(f, ReadMaxToken, func(line []byte) bool {
		lf := schema.Fields(line)
		if lf.Role == LineRoleSkip || lf.SessionID == "" {
			return true
		}
		identity = lf.SessionID
		return false
	})
	if identity != "" {
		return identity, nil
	}
	return "", scanErr
}

// The raw line is valid only until fn returns, and fn returns false to stop.
// The scanner error is returned so callers can judge a truncated scan.
func scanJSONLRawLines(r io.Reader, maxToken int, fn func(line []byte) bool) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), maxToken)
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) == 0 {
			continue
		}
		if !fn(line) {
			return nil
		}
	}
	return s.Err()
}
