package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Resolution is how resolve settles a conflict.
type Resolution string

const (
	// ResolveMine keeps the local file, as it is now: edited by hand to
	// merge both sides, or simply preferred.
	ResolveMine Resolution = "mine"
	// ResolveTheirs takes the server's side; the local file goes to the
	// trash.
	ResolveTheirs Resolution = "theirs"
)

// NotConflictedError is a resolve for a path with no open conflict.
type NotConflictedError struct{ Path string }

func (e *NotConflictedError) Error() string {
	return fmt.Sprintf("%s has no conflict to resolve", e.Path)
}

// UnknownPathError is a path neither store holds.
type UnknownPathError struct{ Path string }

func (e *UnknownPathError) Error() string {
	return fmt.Sprintf("%s is not a synced path", e.Path)
}

// ResolveResult says how a conflict was settled.
type ResolveResult struct {
	Path    string     `json:"path"`
	Store   Store      `json:"store"`
	How     Resolution `json:"how"`
	Trashed string     `json:"trashed,omitempty"`
}

// Resolve settles one conflict. Afterwards the server's side is the base,
// so the local file, if kept, is a change for the next commit.
func (e *Engine) Resolve(rel string, how Resolution) (ResolveResult, error) {
	store, ok := StoreOf(rel)
	if !ok {
		return ResolveResult{}, &UnknownPathError{Path: rel}
	}
	res := ResolveResult{Path: rel, Store: store, How: how}
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return res, err
	}
	c, ok := st.Conflicts[rel]
	if !ok {
		return res, &NotConflictedError{Path: rel}
	}
	theirsPath := TheirsPath(e.ConfDir, rel)
	var theirs []byte
	if !c.Deleted {
		if theirs, err = os.ReadFile(theirsPath); err != nil {
			return res, fmt.Errorf("the server's copy of %s is gone (%w): pull again", rel, err)
		}
	}
	if how == ResolveTheirs {
		if res.Trashed, err = e.trash(rel); err != nil {
			return res, err
		}
		if !c.Deleted {
			if err := writeFile(e.abs(rel), theirs); err != nil {
				return res, err
			}
		}
		delete(st.Hashes, rel)
	}
	if err := e.setBase(store, st, rel, baseEntry{Rev: c.Rev, SHA256: c.SHA256, Deleted: c.Deleted}, theirs); err != nil {
		return res, err
	}
	delete(st.Conflicts, rel)
	if err := os.Remove(theirsPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	return res, st.save(e.ConfDir, store)
}
