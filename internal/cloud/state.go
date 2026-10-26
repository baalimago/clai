package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/utils"
)

// baseEntry is what this machine last agreed with the server about one
// path: the revision and the bytes. A change is local when the file's hash
// differs from it, and is sent with base_rev = Rev.
type baseEntry struct {
	Rev     int64  `json:"rev"`
	SHA256  string `json:"sha256,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// state is one store's sync state on this machine.
type state struct {
	Cursor  int64                `json:"cursor"`
	Entries map[string]baseEntry `json:"entries"`
	Hashes  map[string]hashEntry `json:"hashes"`
}

func statePath(confDir string, store Store) string {
	return filepath.Join(cloudDir(confDir), "state", string(store)+".json")
}

func loadState(confDir string, store Store) (*state, error) {
	st := &state{Entries: map[string]baseEntry{}, Hashes: map[string]hashEntry{}}
	b, err := os.ReadFile(statePath(confDir, store))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		// A torn state file costs a full resync, never the sync itself.
		return &state{Entries: map[string]baseEntry{}, Hashes: map[string]hashEntry{}}, nil
	}
	if st.Entries == nil {
		st.Entries = map[string]baseEntry{}
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

func resetState(confDir string) error {
	err := os.RemoveAll(filepath.Join(cloudDir(confDir), "state"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
