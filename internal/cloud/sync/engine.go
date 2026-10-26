package sync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/utils"
)

// Hooks are the conversation-side collaborators internal/cli injects, so
// this package never imports internal/chat.
type Hooks struct {
	// Export writes the conversations clai can import from other tools
	// under conversations/imported, telling progress how many are written.
	// Only commit --imports runs it.
	Export func(ctx context.Context, confDir string, progress func(done, total int)) error
	// Pending lists what Export would write, reading and writing nothing.
	// status --imports shows it.
	Pending func(ctx context.Context, confDir string) ([]PendingImport, error)
	// Pulled is told which conversation files a pull wrote or removed, so
	// the chat index can follow, telling progress how many it has indexed.
	Pulled func(confDir string, written, removed []string, progress func(done, total int)) error
}

// ImportChange is how a conversation in another tool differs from its
// last export.
type ImportChange string

const (
	ImportNew   ImportChange = "new"
	ImportGrown ImportChange = "grown"
)

// PendingImport is one conversation in another tool that commit --imports
// would export and commit.
type PendingImport struct {
	Source   string       `json:"source"`
	ID       string       `json:"id"`
	Change   ImportChange `json:"change"`
	Messages int          `json:"messages"`
	// RawBytes is the size of the tool's own file, 0 when it keeps none.
	RawBytes int64 `json:"raw_bytes"`
}

// Engine syncs the stores of one config dir with one account.
type Engine struct {
	Client  *cloud.Client
	ConfDir string
	Hooks   Hooks
	Now     func() time.Time
	// Progress, when set, is told of each step of a pull or commit. It may
	// be called from several goroutines at once.
	Progress func(Progress)
	// cache holds a pull's bundled downloads until they are written.
	cache *blobCache
	// window overrides pullWindow (tests).
	window int64
}

func (e *Engine) api() api { return api{c: e.Client} }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) abs(rel string) string { return filepath.Join(e.ConfDir, filepath.FromSlash(rel)) }

// ChecksumError is a downloaded blob whose bytes do not hash to its name.
type ChecksumError struct{ Path, Want, Got string }

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s: downloaded bytes hash to %s, want %s", e.Path, e.Got, e.Want)
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return utils.WriteFileAtomic(p, b, 0o644)
}

func removeFile(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// fetch downloads a blob and checks it.
func (e *Engine) fetch(ctx context.Context, rel, sha string) ([]byte, error) {
	if b, ok := e.cache.take(sha); ok {
		return b, nil
	}
	b, err := e.api().getBlob(ctx, sha)
	if err != nil {
		return nil, err
	}
	if got := hashBytes(b); got != sha {
		return nil, &ChecksumError{Path: rel, Want: sha, Got: got}
	}
	return b, nil
}

// setBase records what this machine and the server agree on, and keeps a
// copy of a config file's bytes for diff.
func (e *Engine) setBase(store Store, st *state, rel string, entry baseEntry, b []byte) error {
	st.Entries[rel] = entry
	if store != StoreConfig {
		return nil
	}
	if entry.Deleted || b == nil {
		return removeFile(baseCopy(e.ConfDir, rel))
	}
	return writeFile(baseCopy(e.ConfDir, rel), b)
}

// trashRetention is how long trash/ keeps a replaced file.
const trashRetention = 30 * 24 * time.Hour

// trash moves a local file that is about to be replaced into
// cloud/trash/<path>.<UTC time>, and answers where it went.
func (e *Engine) trash(rel string) (string, error) {
	src := e.abs(rel)
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	dst := filepath.Join(trashDir(e.ConfDir), filepath.FromSlash(rel)+"."+e.now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	// Retention counts from the move, not the file's last edit.
	now := e.now()
	return dst, os.Chtimes(dst, now, now)
}

// pruneTrash deletes trash older than trashRetention.
func (e *Engine) pruneTrash() error {
	root := trashDir(e.ConfDir)
	cutoff := e.now().Add(-trashRetention)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			return os.Remove(p)
		}
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (e *Engine) hookPulled(written, removed []string) error {
	if e.Hooks.Pulled == nil || (len(written) == 0 && len(removed) == 0) {
		return nil
	}
	return e.Hooks.Pulled(e.ConfDir, written, removed, func(done, total int) {
		e.report(Progress{Store: StoreConversations, Phase: PhaseIndexing, Done: done, Total: total})
	})
}
