package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// staleLock is how old a lock may get before the next sync assumes its
// holder died.
const staleLock = 10 * time.Minute

// LockedError is a sync that found another one running; it left the dirty
// marker, so the running one goes round once more.
type LockedError struct{ Path string }

func (e *LockedError) Error() string { return "another clai cloud sync is running (" + e.Path + ")" }

// runDir holds the lock, the markers and the log. It lives in the cache
// dir: nothing in it is worth keeping.
func runDir(cacheDir string) string { return filepath.Join(cacheDir, "cloud") }

func lockPath(cacheDir string) string  { return filepath.Join(runDir(cacheDir), "sync.lock") }
func dirtyPath(cacheDir string) string { return filepath.Join(runDir(cacheDir), "sync.dirty") }

// LastPath is touched each time a sync is started, background or not.
func lastPath(cacheDir string) string { return filepath.Join(runDir(cacheDir), "last-sync") }

// LogPath is where a background sync writes.
func LogPath(cacheDir string) string { return filepath.Join(runDir(cacheDir), "sync.log") }

// acquire takes the sync lock, or marks the store dirty and answers a
// *LockedError.
func acquire(cacheDir string, now time.Time) (func(), error) {
	if err := os.MkdirAll(runDir(cacheDir), 0o700); err != nil {
		return nil, err
	}
	p := lockPath(cacheDir)
	for range 2 {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			// Staleness is judged on the caller's clock.
			_ = os.Chtimes(p, now, now)
			return func() { _ = os.Remove(p) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(p)
		if statErr == nil && now.Sub(info.ModTime()) > staleLock {
			_ = os.Remove(p)
			continue
		}
		break
	}
	_ = os.WriteFile(dirtyPath(cacheDir), nil, 0o600)
	return nil, &LockedError{Path: p}
}

// takeDirty reports and clears the marker a blocked sync left.
func takeDirty(cacheDir string) bool {
	return os.Remove(dirtyPath(cacheDir)) == nil
}

func touch(p string, now time.Time) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	if err := os.Chtimes(p, now, now); errors.Is(err, fs.ErrNotExist) {
		_ = os.WriteFile(p, nil, 0o600)
		_ = os.Chtimes(p, now, now)
	}
}

// LastSync is when a sync last started on this machine; zero if never.
func LastSync(cacheDir string) time.Time {
	info, err := os.Stat(lastPath(cacheDir))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
