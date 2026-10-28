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
	fetchMaxBlobs = 250
	fetchMaxBytes = 12 << 20
	// fetchAlone is the largest file that shares a bundle; a larger one
	// downloads in a bundle of its own beside the small ones.
	fetchAlone = 4 << 20
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
	// uses is how many files still want a cached blob's bytes.
	uses map[string]int
	// off is set when the server has no bundles: every fetch goes alone.
	off bool
	// downloads counts the pull's progress, including blobs fetched alone.
	downloads *pullDownloads
}

func (c *blobCache) take(sha string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.blobs[sha]
	if c.uses[sha]--; c.uses[sha] <= 0 {
		delete(c.blobs, sha)
		delete(c.uses, sha)
	}
	return b, ok
}

func (c *blobCache) put(blobs map[string][]byte, uses map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	maps.Copy(c.blobs, blobs)
	for sha := range blobs {
		c.uses[sha] = uses[sha]
	}
}

type pullDownloads struct {
	mu         stdsync.Mutex
	store      Store
	total      int
	totalBytes int64
	have       int
	copies     int
	done       int
	bytes      int64
	uses       map[string]int
	fetched    map[string]bool
	// counted are the blobs whose download is in done and bytes.
	counted map[string]bool
}

// arrived counts a downloaded blob once, however many files share it.
func (d *pullDownloads) arrived(sha string, size int64) (Progress, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counted[sha] {
		return Progress{}, false
	}
	d.counted[sha] = true
	d.done++
	d.bytes += size
	return d.progress(), true
}

func (d *pullDownloads) progress() Progress {
	return Progress{Store: d.store, Phase: PhaseDownloading, Done: d.done, Total: d.total, Bytes: d.bytes, TotalBytes: d.totalBytes, Have: d.have, Copies: d.copies}
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
// under window, and counts what the pull will download.
func pullWindows(store Store, st *state, local map[string]LocalFile, entries []RemoteEntry, window int64) ([][]RemoteEntry, *pullDownloads) {
	d := &pullDownloads{store: store, uses: map[string]int{}, fetched: map[string]bool{}, counted: map[string]bool{}}
	var windows [][]RemoteEntry
	start := 0
	var held int64
	for i, r := range entries {
		if !needsBlob(store, st, local, r) {
			if !r.Deleted && Allowed(store, r.Path) {
				d.have++
			}
			continue
		}
		d.uses[r.SHA256]++
		if d.uses[r.SHA256] > 1 {
			d.copies++
			continue
		}
		d.total++
		d.totalBytes += r.Size
		if held > 0 && held+r.Size > window {
			windows = append(windows, entries[start:i])
			start, held = i, 0
		}
		held += r.Size
	}
	return append(windows, entries[start:]), d
}

// needed lists the blobs a window will read that no earlier window fetched.
func (e *Engine) needed(window []RemoteEntry, st *state, local map[string]LocalFile, d *pullDownloads) []RemoteEntry {
	var want []RemoteEntry
	for _, r := range window {
		if !needsBlob(d.store, st, local, r) || d.fetched[r.SHA256] || r.Size > e.limits.fetch().bytes {
			continue
		}
		d.fetched[r.SHA256] = true
		want = append(want, r)
	}
	return want
}

// prefetch downloads want, packed by size, on downloadWorkers goroutines.
// A blob too large for a bundle is left for fetch to get alone.
func (e *Engine) prefetch(ctx context.Context, want []RemoteEntry, d *pullDownloads) error {
	e.cache.mu.Lock()
	off := e.cache.off
	e.cache.mu.Unlock()
	if off {
		return nil
	}
	packs := pack(want, func(r RemoteEntry) int64 { return r.Size }, e.limits.fetch())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan []RemoteEntry)
	errs := make(chan error, downloadWorkers)
	var wg stdsync.WaitGroup
	for range min(downloadWorkers, len(packs)) {
		wg.Go(func() {
			for pack := range queue {
				got, err := e.fetchPack(ctx, pack, d.uses)
				if err != nil {
					errs <- err
					cancel()
					return
				}
				if got == packUnserved {
					continue
				}
				for _, r := range pack {
					if p, ok := d.arrived(r.SHA256, r.Size); ok {
						e.report(p)
					}
				}
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

// packOutcome says whether a bundle request brought its blobs.
type packOutcome int

const (
	packFetched packOutcome = iota
	// packUnserved is a server without bundles; fetch gets each file alone.
	packUnserved
)

func (e *Engine) fetchPack(ctx context.Context, pack []RemoteEntry, uses map[string]int) (packOutcome, error) {
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
		return packUnserved, nil
	}
	if err != nil {
		return packFetched, err
	}
	// Only the checked blobs this pack asked for reach the cache.
	checked := make(map[string][]byte, len(pack))
	for _, r := range pack {
		b, ok := blobs[r.SHA256]
		if !ok {
			return packFetched, &BundleError{Reason: "it lacks " + r.Path}
		}
		if got := hashBytes(b); got != r.SHA256 {
			return packFetched, &ChecksumError{Path: r.Path, Want: r.SHA256, Got: got}
		}
		checked[r.SHA256] = b
	}
	e.cache.put(checked, uses)
	return packFetched, nil
}
