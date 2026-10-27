package sync

import (
	"context"
	"fmt"
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
	// Uploaded lists the local changes whose bytes the server already
	// holds, from an upload whose commit never landed: the next commit
	// sends only the rest.
	Uploaded []string `json:"uploaded"`
}

// Status is one store's local plan and, when the server answered, its side.
type Status struct {
	Plan
	Remote *RemoteStatus `json:"remote,omitempty"`
	// RemoteError is why the server's side is missing, such as being
	// offline; the local part is still right.
	RemoteError error `json:"-"`
	// Imports is what commit --imports would add from other tools; nil
	// unless status was asked with Imports for conversations.
	Imports *ImportPreview `json:"imports,omitempty"`
}

// ImportPreview is the conversations other tools hold that are not yet
// exported, or whose export is behind.
type ImportPreview struct {
	Conversations []PendingImport `json:"conversations"`
}

// Status reads the local side, then asks the server what it has that this
// machine has not pulled. Nothing is applied.
func (e *Engine) Status(ctx context.Context, store Store, opts PlanOptions) (Status, error) {
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return Status{}, err
	}
	plan, err := e.plan(ctx, store, st, opts)
	if err != nil {
		return Status{}, err
	}
	if err := st.save(e.ConfDir, store); err != nil {
		return Status{}, err
	}
	out := Status{Plan: plan}
	if store == StoreConversations && opts.Imports && e.Hooks.Pending != nil {
		e.report(Progress{Store: store, Phase: PhaseDiscovering})
		pending, err := e.Hooks.Pending(ctx, e.ConfDir)
		if err != nil {
			return Status{}, fmt.Errorf("look for conversations in other tools: %w", err)
		}
		out.Imports = &ImportPreview{Conversations: pending}
	}
	entries, _, err := e.api().allChanges(ctx, store, st.Cursor)
	if err != nil {
		out.RemoteError = err
		return out, nil
	}
	changed := map[string]bool{}
	for _, c := range plan.Changes {
		changed[c.Path] = true
	}
	remote := &RemoteStatus{Changed: []RemoteEntry{}, Both: []string{}, Uploaded: []string{}}
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
	uploaded, err := e.uploaded(ctx, plan.Changes)
	if err != nil {
		out.RemoteError = err
		return out, nil
	}
	remote.Uploaded = uploaded
	out.Remote = remote
	return out, nil
}

// uploaded asks the server which changes' bytes it already holds.
func (e *Engine) uploaded(ctx context.Context, changes []LocalChange) ([]string, error) {
	pathsBySHA := map[string][]string{}
	var hashes []string
	for _, c := range changes {
		if c.Kind == Deleted {
			continue
		}
		if _, ok := pathsBySHA[c.SHA256]; !ok {
			hashes = append(hashes, c.SHA256)
		}
		pathsBySHA[c.SHA256] = append(pathsBySHA[c.SHA256], c.Path)
	}
	missing := map[string]bool{}
	for start := 0; start < len(hashes); start += missingBatch {
		batch, err := e.api().missing(ctx, hashes[start:min(start+missingBatch, len(hashes))])
		if err != nil {
			return nil, err
		}
		for _, sha := range batch {
			missing[sha] = true
		}
	}
	out := []string{}
	for _, sha := range hashes {
		if !missing[sha] {
			out = append(out, pathsBySHA[sha]...)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Log lists a store's recent commits, newest first.
func (e *Engine) Log(ctx context.Context, store Store, limit int) ([]LogEntry, error) {
	return e.api().log(ctx, store, limit)
}
