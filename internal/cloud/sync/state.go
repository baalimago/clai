package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/utils"
)

// baseEntry is what this machine last agreed with the server about one
// path: the revision and the bytes. A file whose hash differs from it is a
// local change, committed with base_rev = Rev.
type baseEntry struct {
	Rev     int64  `json:"rev"`
	SHA256  string `json:"sha256,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// conflict is a path both sides changed: the server's side, as pulled. Its
// bytes are at cloud/theirs/<path> unless the server deleted it.
type conflict struct {
	Rev     int64  `json:"rev"`
	SHA256  string `json:"sha256,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Commit  int64  `json:"commit,omitempty"`
	Device  string `json:"device,omitempty"`
}

// state is one store's sync state on this machine.
type state struct {
	// Cursor is the last server revision pulled; Commit the last commit.
	Cursor int64 `json:"cursor"`
	Commit int64 `json:"commit"`
	// Pulled is set by the first pull, which asks before it replaces a file.
	Pulled    bool                 `json:"pulled"`
	Entries   map[string]baseEntry `json:"entries"`
	Conflicts map[string]conflict  `json:"conflicts,omitempty"`
	Hashes    map[string]hashEntry `json:"hashes"`
}

func newState() *state {
	return &state{Entries: map[string]baseEntry{}, Conflicts: map[string]conflict{}, Hashes: map[string]hashEntry{}}
}

func statePath(confDir string, store Store) string {
	return filepath.Join(cloud.Dir(confDir), "state", string(store)+".json")
}

// StateError is a state file that exists but cannot be read; sync stops
// rather than start over and offer to replace every file.
type StateError struct {
	Path  string
	Cause error
}

func (e *StateError) Error() string {
	return fmt.Sprintf("sync state %s is unreadable (%v): move it aside and pull again", e.Path, e.Cause)
}
func (e *StateError) Unwrap() error { return e.Cause }

func loadState(confDir string, store Store) (*state, error) {
	p := statePath(confDir, store)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return nil, &StateError{Path: p, Cause: err}
	}
	st := newState()
	if err := json.Unmarshal(b, st); err != nil {
		return nil, &StateError{Path: p, Cause: err}
	}
	if st.Entries == nil {
		st.Entries = map[string]baseEntry{}
	}
	if st.Conflicts == nil {
		st.Conflicts = map[string]conflict{}
	}
	if st.Hashes == nil {
		st.Hashes = map[string]hashEntry{}
	}
	return st, nil
}

func (st *state) save(confDir string, store Store) error {
	p := statePath(confDir, store)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := utils.WriteFileAtomic(p, b, 0o600); err != nil {
		return fmt.Errorf("save sync state: %w", err)
	}
	return nil
}

// ResetState forgets every store's sync state, the base copies and the
// conflict copies, as a logout or a login to another account must. Local
// files and the trash stay.
func ResetState(confDir string) error {
	var errs []error
	for _, d := range []string{"state", "base", "theirs"} {
		errs = append(errs, os.RemoveAll(filepath.Join(cloud.Dir(confDir), d)))
	}
	return errors.Join(errs...)
}

// The per-machine directories under cloud/, each mirroring the config
// dir's layout.
func baseCopy(confDir, rel string) string {
	return filepath.Join(cloud.Dir(confDir), "base", filepath.FromSlash(rel))
}

// TheirsPath is where pull keeps the server's side of a conflict.
func TheirsPath(confDir, rel string) string {
	return filepath.Join(cloud.Dir(confDir), "theirs", filepath.FromSlash(rel))
}

func trashDir(confDir string) string { return filepath.Join(cloud.Dir(confDir), "trash") }
