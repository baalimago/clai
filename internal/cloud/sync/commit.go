package sync

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
)

// ConflictsOpenError is a commit refused because pull left conflicts that
// resolve has not settled.
type ConflictsOpenError struct {
	Store Store
	Paths []string
}

func (e *ConflictsOpenError) Error() string {
	return fmt.Sprintf("%d unresolved conflicts in %s: run clai cloud resolve <path> --mine|--theirs first", len(e.Paths), e.Store)
}

// BehindError is a commit the server refused because files it would change
// moved on the server since the last pull. Nothing was uploaded to the
// store.
type BehindError struct {
	Store Store
	Paths []RemoteEntry
}

func (e *BehindError) Error() string {
	return fmt.Sprintf("the server changed %d of your files since your last pull: run clai cloud pull, then commit again", len(e.Paths))
}

// CommitOptions narrows and labels a commit.
type CommitOptions struct {
	Message string
	PlanOptions
}

// CommitResult is one store's commit.
type CommitResult struct {
	Store    Store         `json:"store"`
	Commit   int64         `json:"commit"`
	Files    int           `json:"files"`
	Bytes    int64         `json:"bytes"`
	Uploaded int           `json:"uploaded"`
	Imported int           `json:"imported"`
	Excluded []Excluded    `json:"excluded"`
	Changes  []LocalChange `json:"changes"`
}

// Commit uploads this machine's changes to one store as one commit. It
// refuses while conflicts are open, and the server refuses it, whole, when
// any file moved on the server since the last pull.
func (e *Engine) Commit(ctx context.Context, store Store, opts CommitOptions) (CommitResult, error) {
	res := CommitResult{Store: store, Excluded: []Excluded{}, Changes: []LocalChange{}}
	if store == StoreConversations && opts.Imports && e.Hooks.Export != nil {
		e.report(Progress{Store: store, Phase: PhaseExporting})
		exported := func(done, total int) {
			e.report(Progress{Store: store, Phase: PhaseExporting, Done: done, Total: total})
		}
		if err := e.Hooks.Export(ctx, e.ConfDir, exported); err != nil {
			return res, fmt.Errorf("export imported conversations: %w", err)
		}
	}
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return res, err
	}
	plan, err := e.plan(store, st, opts.PlanOptions)
	if err != nil {
		return res, err
	}
	// The hashes are worth keeping whatever happens next: a refused or
	// interrupted commit must not cost the next one the whole read again.
	if err := st.save(e.ConfDir, store); err != nil {
		return res, err
	}
	res.Excluded = plan.Excluded
	if len(plan.Conflicts) > 0 {
		return res, &ConflictsOpenError{Store: store, Paths: plan.Conflicts}
	}
	if len(plan.Changes) == 0 {
		return res, st.save(e.ConfDir, store)
	}
	if err := e.checkBilling(ctx, store, plan.Changes); err != nil {
		return res, err
	}
	uploaded, err := e.upload(ctx, store, plan.Changes)
	if err != nil {
		return res, err
	}
	res.Uploaded = uploaded
	for start := 0; start < len(plan.Changes); start += commitBatch {
		e.report(Progress{Store: store, Phase: PhaseCommitting, Done: start, Total: len(plan.Changes)})
		batch := plan.Changes[start:min(start+commitBatch, len(plan.Changes))]
		msg := opts.Message
		if start > 0 {
			msg = fmt.Sprintf("%s (part %d)", msg, start/commitBatch+1)
		}
		if err := e.commitBatch(ctx, store, st, msg, batch, &res); err != nil {
			return res, err
		}
	}
	return res, st.save(e.ConfDir, store)
}

func (e *Engine) commitBatch(ctx context.Context, store Store, st *state, msg string, batch []LocalChange, res *CommitResult) error {
	wire := make([]change, len(batch))
	for i, c := range batch {
		wire[i] = change{Path: c.Path, BaseRev: c.BaseRev, SHA256: c.SHA256, Size: c.Size, Deleted: c.Kind == Deleted}
	}
	ans, err := e.api().commit(ctx, store, msg, wire)
	if err != nil {
		return err
	}
	if len(ans.Conflicts) > 0 {
		behind := &BehindError{Store: store}
		for _, c := range ans.Conflicts {
			entry := RemoteEntry{Path: c.Path}
			if c.Current != nil {
				entry = *c.Current
			}
			behind.Paths = append(behind.Paths, entry)
		}
		slices.SortFunc(behind.Paths, func(a, b RemoteEntry) int { return strings.Compare(a.Path, b.Path) })
		return behind
	}
	byPath := map[string]LocalChange{}
	for _, c := range batch {
		byPath[c.Path] = c
	}
	for _, a := range ans.Applied {
		c := byPath[a.Path]
		var b []byte
		if c.Kind != Deleted && store == StoreConfig {
			if b, err = os.ReadFile(e.abs(c.Path)); err != nil || hashBytes(b) != c.SHA256 {
				b = nil
			}
		}
		if err := e.setBase(store, st, a.Path, baseEntry{Rev: a.Rev, SHA256: c.SHA256, Deleted: c.Kind == Deleted}, b); err != nil {
			return err
		}
		res.Files++
		res.Bytes += c.Size
		if isImport(c.Path) {
			res.Imported++
		}
		res.Changes = append(res.Changes, c)
	}
	res.Commit = ans.Commit
	if ans.Commit > st.Commit && st.Commit == ans.Commit-1 {
		// Nobody else committed in between, so nothing was skipped.
		st.Commit = ans.Commit
	}
	return st.save(e.ConfDir, store)
}

// upload sends every blob the server is missing. A file that changed since
// the plan was read stops the commit: committing the new bytes under the
// old hash would be wrong, and the user should see what they commit.
func (e *Engine) upload(ctx context.Context, store Store, changes []LocalChange) (int, error) {
	sizeBySHA := map[string]int64{}
	pathBySHA := map[string]string{}
	var hashes []string
	for _, c := range changes {
		if c.Kind == Deleted {
			continue
		}
		if _, ok := pathBySHA[c.SHA256]; !ok {
			hashes = append(hashes, c.SHA256)
			pathBySHA[c.SHA256] = c.Path
			sizeBySHA[c.SHA256] = c.Size
		}
	}
	var jobs []uploadJob
	for start := 0; start < len(hashes); start += missingBatch {
		missing, err := e.api().missing(ctx, hashes[start:min(start+missingBatch, len(hashes))])
		if err != nil {
			return 0, err
		}
		for _, sha := range missing {
			if p, ok := pathBySHA[sha]; ok {
				jobs = append(jobs, uploadJob{sha: sha, path: p, size: sizeBySHA[sha]})
			}
		}
	}
	return e.uploadMissing(ctx, store, jobs)
}

// ChangedDuringCommitError is a file written to while it was being
// committed.
type ChangedDuringCommitError struct{ Path string }

func (e *ChangedDuringCommitError) Error() string {
	return e.Path + " changed while it was being committed: commit again"
}
