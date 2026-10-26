package sync

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/cloud"
)

const (
	missingBatch = 1000
	// commitBatch mirrors clai-cloud's MAX_COMMIT: one server commit.
	commitBatch = 1000
)

// RemoteEntry is one path's state on the server, with the commit that last
// changed it.
type RemoteEntry struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Rev       int64  `json:"rev"`
	Deleted   bool   `json:"deleted"`
	Device    string `json:"device"`
	Commit    int64  `json:"commit"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updated_at"`
}

type changesPage struct {
	Entries []RemoteEntry `json:"entries"`
	Cursor  int64         `json:"cursor"`
	More    bool          `json:"more"`
}

type api struct{ c *cloud.Client }

func storePath(store Store, tail string) string {
	return "/api/v1/stores/" + url.PathEscape(string(store)) + "/" + tail
}

func (a api) changes(ctx context.Context, store Store, since int64) (changesPage, error) {
	var out changesPage
	err := a.c.Do(ctx, http.MethodGet, storePath(store, "changes?since="+strconv.FormatInt(since, 10)), nil, "", &out)
	return out, err
}

// allChanges pages through everything after since and keeps the latest
// entry per path.
func (a api) allChanges(ctx context.Context, store Store, since int64) ([]RemoteEntry, int64, error) {
	latest := map[string]int{}
	var out []RemoteEntry
	cursor := since
	for {
		page, err := a.changes(ctx, store, cursor)
		if err != nil {
			return nil, since, err
		}
		for _, e := range page.Entries {
			if i, ok := latest[e.Path]; ok {
				out[i] = e
				continue
			}
			latest[e.Path] = len(out)
			out = append(out, e)
		}
		cursor = page.Cursor
		if !page.More {
			return out, cursor, nil
		}
	}
}

func (a api) missing(ctx context.Context, hashes []string) ([]string, error) {
	var out struct {
		Missing []string `json:"missing"`
	}
	err := a.c.PostJSON(ctx, "/api/v1/blobs/missing", map[string][]string{"hashes": hashes}, &out)
	return out.Missing, err
}

func (a api) putBlob(ctx context.Context, sha string, b []byte) error {
	return a.c.Do(ctx, http.MethodPut, "/api/v1/blobs/"+sha, bytes.NewReader(b), "application/octet-stream", nil)
}

type batchBlob struct {
	sha  string
	data []byte
}

// putBlobs sends blobs in one multipart request, each part named by its
// hash, and returns the server's answer per hash.
func (a api) putBlobs(ctx context.Context, blobs []batchBlob) (map[string]string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, b := range blobs {
		part, err := w.CreateFormFile(b.sha, b.sha)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(b.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	if _, err := zw.Write(body.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Content-Type", w.FormDataContentType())
	header.Set(encodingHeader, "gzip")
	var out struct {
		Results map[string]string `json:"results"`
	}
	err := a.c.DoHeader(ctx, http.MethodPost, "/api/v1/blobs/batch", &zipped, header, &out)
	return out.Results, err
}

// encodingHeader is clai-cloud's own content coding for batches and
// bundles; a standard Content-Encoding would be changed on the way.
const encodingHeader = "X-Clai-Encoding"

// BundleError is a fetch bundle that does not parse.
type BundleError struct{ Reason string }

func (e *BundleError) Error() string { return "read a download bundle: " + e.Reason }

// fetchBlobs downloads blobs in one bundle: gzipped "<sha256> <size>\n"
// frames, each followed by its bytes.
func (a api) fetchBlobs(ctx context.Context, hashes []string) (map[string][]byte, error) {
	in, err := json.Marshal(map[string][]string{"hashes": hashes})
	if err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	if err := a.c.Do(ctx, http.MethodPost, "/api/v1/blobs/fetch", bytes.NewReader(in), "application/json", &raw); err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(&raw)
	if err != nil {
		return nil, &BundleError{Reason: err.Error()}
	}
	r := bufio.NewReader(zr)
	out := map[string][]byte{}
	for {
		line, err := r.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			return out, nil
		}
		if err != nil {
			return nil, &BundleError{Reason: err.Error()}
		}
		sha, sizeText, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		size, convErr := strconv.ParseInt(sizeText, 10, 64)
		if !ok || convErr != nil || size < 0 {
			return nil, &BundleError{Reason: fmt.Sprintf("bad frame %q", line)}
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, &BundleError{Reason: "blob " + sha + ": " + err.Error()}
		}
		out[sha] = b
	}
}

func (a api) getBlob(ctx context.Context, sha string) ([]byte, error) {
	var buf bytes.Buffer
	if err := a.c.Do(ctx, http.MethodGet, "/api/v1/blobs/"+sha, nil, "", &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// change is one path in a commit: a new content hash, or a deletion.
type change struct {
	Path    string `json:"path"`
	BaseRev int64  `json:"base_rev"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	Deleted bool   `json:"deleted,omitempty"`
}

type commitAnswer struct {
	Commit  int64 `json:"commit"`
	Rev     int64 `json:"rev"`
	Applied []struct {
		Path string `json:"path"`
		Rev  int64  `json:"rev"`
	} `json:"applied"`
	Conflicts []struct {
		Path    string       `json:"path"`
		Current *RemoteEntry `json:"current"`
	} `json:"conflicts"`
}

// commit is all or nothing: one stale base and the server applies none.
func (a api) commit(ctx context.Context, store Store, message string, changes []change) (commitAnswer, error) {
	var out commitAnswer
	err := a.c.PostJSON(ctx, storePath(store, "commit"), map[string]any{"message": message, "changes": changes}, &out)
	return out, err
}

// LogEntry is one commit in a store's history.
type LogEntry struct {
	Commit    int64  `json:"commit"`
	Message   string `json:"message"`
	Device    string `json:"device"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
}

func (a api) log(ctx context.Context, store Store, limit int) ([]LogEntry, error) {
	var out struct {
		Commits []LogEntry `json:"commits"`
	}
	err := a.c.Do(ctx, http.MethodGet, storePath(store, "log?limit="+strconv.Itoa(limit)), nil, "", &out)
	return out.Commits, err
}
