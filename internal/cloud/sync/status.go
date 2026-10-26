package sync

import (
	"context"
	"slices"
)

// RemoteStatus is what the server has that this machine has not pulled.
type RemoteStatus struct {
	// Ahead counts the commits since this machine's last pull.
	Ahead int64 `json:"ahead"`
	// Changed lists the paths those commits touched.
	Changed []RemoteEntry `json:"changed"`
	// Both lists the paths changed here and there: the next pull's
	// conflicts, unless they are conversations one side only extended.
	Both []string `json:"both"`
}

// Status is one store's local plan and, when the server answered, its side.
type Status struct {
	Plan
	Remote *RemoteStatus `json:"remote,omitempty"`
	// RemoteError is why the server's side is missing, such as being
	// offline; the local part is still right.
	RemoteError error `json:"-"`
}

// Status reads the local side, then asks the server what it has that this
// machine has not pulled. Nothing is applied.
func (e *Engine) Status(ctx context.Context, store Store, opts PlanOptions) (Status, error) {
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return Status{}, err
	}
	plan, err := e.plan(store, st, opts)
	if err != nil {
		return Status{}, err
	}
	if err := st.save(e.ConfDir, store); err != nil {
		return Status{}, err
	}
	out := Status{Plan: plan}
	entries, _, err := e.api().allChanges(ctx, store, st.Cursor)
	if err != nil {
		out.RemoteError = err
		return out, nil
	}
	changed := map[string]bool{}
	for _, c := range plan.Changes {
		changed[c.Path] = true
	}
	remote := &RemoteStatus{Changed: []RemoteEntry{}, Both: []string{}}
	commits := map[int64]bool{}
	for _, r := range entries {
		if base, ok := st.Entries[r.Path]; ok && base.Rev >= r.Rev {
			continue
		}
		remote.Changed = append(remote.Changed, r)
		if r.Commit > st.Commit {
			commits[r.Commit] = true
		}
		if changed[r.Path] {
			remote.Both = append(remote.Both, r.Path)
		}
	}
	remote.Ahead = int64(len(commits))
	slices.Sort(remote.Both)
	out.Remote = remote
	return out, nil
}

// Log lists a store's recent commits, newest first.
func (e *Engine) Log(ctx context.Context, store Store, limit int) ([]LogEntry, error) {
	return e.api().log(ctx, store, limit)
}
