package sync

import (
	"context"
	"maps"
	"net/http"
	stdsync "sync"

	"github.com/baalimago/clai/internal/cloud"
)

const (
	// fetchMaxBlobs and fetchMaxBytes stay under clai-cloud's
	// FETCH_MAX_BLOBS and FETCH_MAX_BYTES (16 MiB).
	fetchMaxBlobs = 100
	fetchMaxBytes = 12 << 20
	// pullWindow bounds the bytes a pull holds in memory between fetching
	// them and writing them out.
	pullWindow      = 64 << 20
	downloadWorkers = 8
)

// blobCache holds bundled downloads until apply writes them; fetch takes
// from it first.
type blobCache struct {
	mu    stdsync.Mutex
	blobs map[string][]byte
	// off is set when the server has no bundles: every fetch goes alone.
	off bool
}

func (c *blobCache) take(sha string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.blobs[sha]
	delete(c.blobs, sha)
	return b, ok
}

func (c *blobCache) put(blobs map[string][]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	maps.Copy(c.blobs, blobs)
}

type pullDownloads struct {
	store      Store
	total      int
	totalBytes int64
	done       int
	bytes      int64
	pathBySHA  map[string]string
}

// needsBlob is whether applying remote may read its bytes: a file this
// machine has not got in that version.
func needsBlob(store Store, st *state, local map[string]LocalFile, remote RemoteEntry) bool {
	if remote.Deleted || !Allowed(store, remote.Path) {
		return false
	}
	if base, ok := st.Entries[remote.Path]; ok && base.Rev >= remote.Rev {
		return false
	}
	loc, ok := local[remote.Path]
	return !ok || loc.SHA256 != remote.SHA256
}

// pullWindows splits entries, in order, into runs whose needed bytes stay
// under pullWindow, and counts what the pull will download.
func pullWindows(store Store, st *state, local map[string]LocalFile, entries []RemoteEntry) ([][]RemoteEntry, *pullDownloads) {
	d := &pullDownloads{store: store, pathBySHA: map[string]string{}}
	var windows [][]RemoteEntry
	start := 0
	var held int64
	for i, r := range entries {
		if !needsBlob(store, st, local, r) {
			continue
		}
		if _, seen := d.pathBySHA[r.SHA256]; seen {
			continue
		}
		d.pathBySHA[r.SHA256] = r.Path
		d.total++
		d.totalBytes += r.Size
		if held > 0 && held+r.Size > pullWindow {
			windows = append(windows, entries[start:i])
			start, held = i, 0
		}
		held += r.Size
	}
	return append(windows, entries[start:]), d
}

// prefetch downloads the blobs a window needs, in bundles, on
// downloadWorkers goroutines. A blob too large for a bundle is left for
// fetch to get alone.
func (e *Engine) prefetch(ctx context.Context, window []RemoteEntry, st *state, local map[string]LocalFile, d *pullDownloads) error {
	if e.cache.off {
		return nil
	}
	var packs [][]RemoteEntry
	var cur []RemoteEntry
	var curBytes int64
	seen := map[string]bool{}
	for _, r := range window {
		if !needsBlob(d.store, st, local, r) || seen[r.SHA256] || r.Size > fetchMaxBytes {
			continue
		}
		seen[r.SHA256] = true
		if len(cur) == fetchMaxBlobs || (len(cur) > 0 && curBytes+r.Size > fetchMaxBytes) {
			packs = append(packs, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, r)
		curBytes += r.Size
	}
	if len(cur) > 0 {
		packs = append(packs, cur)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan []RemoteEntry)
	errs := make(chan error, downloadWorkers)
	var mu stdsync.Mutex
	var wg stdsync.WaitGroup
	for range min(downloadWorkers, len(packs)) {
		wg.Go(func() {
			for pack := range queue {
				if err := e.fetchPack(ctx, pack); err != nil {
					errs <- err
					cancel()
					return
				}
				mu.Lock()
				for _, r := range pack {
					d.done++
					d.bytes += r.Size
				}
				e.report(Progress{Store: d.store, Phase: PhaseDownloading, Done: d.done, Total: d.total, Bytes: d.bytes, TotalBytes: d.totalBytes})
				mu.Unlock()
			}
		})
	}
feed:
	for _, p := range packs {
		select {
		case queue <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	close(errs)
	if err, ok := <-errs; ok {
		return err
	}
	return ctx.Err()
}

func (e *Engine) fetchPack(ctx context.Context, pack []RemoteEntry) error {
	hashes := make([]string, 0, len(pack))
	for _, r := range pack {
		hashes = append(hashes, r.SHA256)
	}
	blobs, err := e.api().fetchBlobs(ctx, hashes)
	if apiErr, ok := cloud.AsAPIError(err); ok && apiErr.Status == http.StatusNotFound && len(apiErr.Missing) == 0 {
		// A server from before bundles: fetch gets each file alone.
		e.cache.mu.Lock()
		e.cache.off = true
		e.cache.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	for _, r := range pack {
		b, ok := blobs[r.SHA256]
		if !ok {
			return &BundleError{Reason: "it lacks " + r.Path}
		}
		if got := hashBytes(b); got != r.SHA256 {
			return &ChecksumError{Path: r.Path, Want: r.SHA256, Got: got}
		}
	}
	e.cache.put(blobs)
	return nil
}
