package sync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
)

// Mode says what a pull does with a file both sides changed.
type Mode string

const (
	// ModeConflicts keeps the local file and saves the server's copy to
	// cloud/theirs/<path>, for resolve.
	ModeConflicts Mode = "conflicts"
	// ModeTheirs takes the server's copy; the local one goes to the trash.
	ModeTheirs Mode = "theirs"
	// ModeMine keeps the local copy as a change to commit.
	ModeMine Mode = "mine"
)

// FirstPull is what a store's first pull on this machine found.
type FirstPull struct {
	Remote     int      `json:"remote"`
	Local      int      `json:"local"`
	OnlyRemote int      `json:"only_remote"`
	Same       int      `json:"same"`
	Differ     []string `json:"differ"`
}

// FirstPullError is a first pull that found files on both sides that
// differ and was given no way to choose: no flag and no terminal.
type FirstPullError struct{ First FirstPull }

func (e *FirstPullError) Error() string {
	return fmt.Sprintf("%d files differ between this machine and the account: pull again with --theirs or --mine", len(e.First.Differ))
}

// PullOptions says how a pull settles a file both sides changed.
type PullOptions struct {
	// Mode is the answer given up front (--theirs, --mine). Empty means
	// conflicts, except on a first pull with differing files, which asks
	// Choose.
	Mode Mode
	// Choose answers a first pull's question; nil means there is nobody to
	// ask, and the pull stops with a *FirstPullError.
	Choose func(FirstPull) (Mode, error)
}

// PullResult is what a pull did to one store.
type PullResult struct {
	Store     Store    `json:"store"`
	Commit    int64    `json:"pulled_to"`
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Removed   int      `json:"removed"`
	Merged    []string `json:"merged"`
	Forks     []Fork   `json:"forks"`
	Conflicts []string `json:"conflicts"`
	Trashed   []string `json:"trashed"`
	// Linked are server paths left alone because they reach through a
	// symbolic link here.
	Linked  []string   `json:"linked"`
	First   *FirstPull `json:"first,omitempty"`
	written []string
	removed []string
}

// Pull brings the server's changes to this machine. A file changed only on
// the server is updated in place; a file changed on both sides is settled
// by opts. Local files are never overwritten without going to the trash.
func (e *Engine) Pull(ctx context.Context, store Store, opts PullOptions) (PullResult, error) {
	res := PullResult{Store: store, Merged: []string{}, Forks: []Fork{}, Conflicts: []string{}, Trashed: []string{}, Linked: []string{}}
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return res, err
	}
	entries, cursor, err := e.api().allChanges(ctx, store, st.Cursor)
	if err != nil {
		return res, err
	}
	local, err := scan(e.ConfDir, store, st.Hashes, e.scanned(store))
	if err != nil {
		return res, err
	}
	mode := opts.Mode
	if mode == "" {
		mode = ModeConflicts
	}
	if !st.Pulled {
		first := classifyFirst(entries, local, st)
		res.First = &first
		if len(first.Differ) > 0 && opts.Mode == "" {
			if opts.Choose == nil {
				return res, &FirstPullError{First: first}
			}
			if mode, err = opts.Choose(first); err != nil {
				return res, err
			}
		}
	}
	applyErr := e.applyAll(ctx, store, st, local, entries, mode, &res)
	if applyErr == nil {
		st.Cursor = cursor
		st.Pulled = true
	}
	res.Commit = st.Commit
	slices.Sort(res.Conflicts)
	errs := []error{applyErr, st.save(e.ConfDir, store), e.pruneTrash()}
	if store == StoreConversations {
		errs = append(errs, e.hookPulled(res.written, res.removed))
	}
	return res, errors.Join(errs...)
}

// applyAll downloads what the entries need in bundles, a window at a time,
// and applies each window in order while the next one downloads.
func (e *Engine) applyAll(ctx context.Context, store Store, st *state, local map[string]LocalFile, entries []RemoteEntry, mode Mode, res *PullResult) error {
	windows, d := pullWindows(store, st, local, entries, cmp.Or(e.window, pullWindow))
	e.cache = &blobCache{blobs: map[string][]byte{}, uses: map[string]int{}, downloads: d}
	defer func() { e.cache = nil }()
	e.report(d.progress())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// prefetch reads local and st while apply writes them, so it is given
	// what each window needs before apply starts on the one before.
	start := func(w []RemoteEntry) chan error {
		need := e.needed(w, st, local, d)
		done := make(chan error, 1)
		go func() { done <- e.prefetch(ctx, need, d) }()
		return done
	}
	next := start(windows[0])
	for i, w := range windows {
		if err := <-next; err != nil {
			return err
		}
		if i+1 < len(windows) {
			next = start(windows[i+1])
		}
		for _, remote := range w {
			if !Allowed(store, remote.Path) {
				continue
			}
			linked, err := e.linkage(remote.Path)
			if err != nil {
				return err
			}
			if linked == Linked {
				res.Linked = append(res.Linked, remote.Path)
				continue
			}
			if err := e.apply(ctx, store, st, local, remote, mode, res); err != nil {
				cancel()
				if i+1 < len(windows) {
					<-next
				}
				return err
			}
			if remote.Commit > st.Commit {
				st.Commit = remote.Commit
			}
		}
	}
	return nil
}

func classifyFirst(entries []RemoteEntry, local map[string]LocalFile, st *state) FirstPull {
	f := FirstPull{Local: len(local), Differ: []string{}}
	for _, r := range entries {
		if r.Deleted {
			continue
		}
		f.Remote++
		loc, ok := local[r.Path]
		switch {
		case !ok:
			f.OnlyRemote++
		case loc.SHA256 == r.SHA256:
			f.Same++
		default:
			// A path this machine already agreed on is a plain conflict,
			// not the first pull's question.
			if _, known := st.Entries[r.Path]; known {
				continue
			}
			f.Differ = append(f.Differ, r.Path)
		}
	}
	slices.Sort(f.Differ)
	return f
}

// apply brings one remote entry into the config dir.
func (e *Engine) apply(ctx context.Context, store Store, st *state, local map[string]LocalFile, remote RemoteEntry, mode Mode, res *PullResult) error {
	base, hasBase := st.Entries[remote.Path]
	if hasBase && base.Rev >= remote.Rev {
		// This machine's own commit coming back through the feed.
		return nil
	}
	loc, hasLocal := local[remote.Path]
	clean := (hasLocal && hasBase && !base.Deleted && loc.SHA256 == base.SHA256) ||
		(!hasLocal && (!hasBase || base.Deleted))
	agreed := baseEntry{Rev: remote.Rev, SHA256: remote.SHA256, Deleted: remote.Deleted}
	switch {
	case (remote.Deleted && !hasLocal) || (hasLocal && !remote.Deleted && loc.SHA256 == remote.SHA256):
		delete(st.Conflicts, remote.Path)
		var b []byte
		if !remote.Deleted && store == StoreConfig {
			b, _ = os.ReadFile(e.abs(remote.Path))
		}
		return e.setBase(store, st, remote.Path, agreed, b)
	case clean:
		return e.take(ctx, store, st, local, remote, res)
	}
	// Both sides changed the path.
	if store == StoreConversations && chatFile.MatchString(remote.Path) && hasLocal && !remote.Deleted {
		return e.mergeChat(ctx, st, local, remote, res)
	}
	switch mode {
	case ModeTheirs:
		where, err := e.trash(remote.Path)
		if err != nil {
			return err
		}
		if where != "" {
			res.Trashed = append(res.Trashed, remote.Path)
		}
		delete(local, remote.Path)
		return e.take(ctx, store, st, local, remote, res)
	case ModeMine:
		delete(st.Conflicts, remote.Path)
		return e.setBase(store, st, remote.Path, agreed, nil)
	}
	return e.markConflict(ctx, st, remote, res)
}

// take makes the local file the server's, for a path this machine did not
// change.
func (e *Engine) take(ctx context.Context, store Store, st *state, local map[string]LocalFile, remote RemoteEntry, res *PullResult) error {
	delete(st.Conflicts, remote.Path)
	if err := removeFile(TheirsPath(e.ConfDir, remote.Path)); err != nil {
		return err
	}
	_, existed := local[remote.Path]
	if remote.Deleted {
		if err := removeFile(e.abs(remote.Path)); err != nil {
			return err
		}
		delete(local, remote.Path)
		if existed {
			res.Removed++
			res.noteChat(remote.Path, false)
		}
		return e.setBase(store, st, remote.Path, baseEntry{Rev: remote.Rev, Deleted: true}, nil)
	}
	b, err := e.fetch(ctx, remote.Path, remote.SHA256)
	if err != nil {
		return err
	}
	if err := writeFile(e.abs(remote.Path), b); err != nil {
		return err
	}
	delete(st.Hashes, remote.Path)
	local[remote.Path] = LocalFile{SHA256: remote.SHA256, Size: int64(len(b))}
	if existed {
		res.Updated++
	} else {
		res.Added++
	}
	res.noteChat(remote.Path, true)
	return e.setBase(store, st, remote.Path, baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}, b)
}

// markConflict keeps the local file and saves the server's side for
// resolve.
func (e *Engine) markConflict(ctx context.Context, st *state, remote RemoteEntry, res *PullResult) error {
	theirs := TheirsPath(e.ConfDir, remote.Path)
	if remote.Deleted {
		if err := removeFile(theirs); err != nil {
			return err
		}
	} else {
		b, err := e.fetch(ctx, remote.Path, remote.SHA256)
		if err != nil {
			return err
		}
		if err := writeFile(theirs, b); err != nil {
			return err
		}
	}
	st.Conflicts[remote.Path] = conflict{Rev: remote.Rev, SHA256: remote.SHA256, Deleted: remote.Deleted, Commit: remote.Commit, Device: remote.Device}
	res.Conflicts = append(res.Conflicts, remote.Path)
	return nil
}

// mergeChat settles a conversation both machines changed: the longer copy
// wins when one only added messages; otherwise the server's copy becomes a
// fork beside this machine's.
func (e *Engine) mergeChat(ctx context.Context, st *state, local map[string]LocalFile, remote RemoteEntry, res *PullResult) error {
	mine, err := os.ReadFile(e.abs(remote.Path))
	if err != nil {
		return err
	}
	theirs, err := e.fetch(ctx, remote.Path, remote.SHA256)
	if err != nil {
		return err
	}
	agreed := baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}
	outcome := mergeChats(mine, theirs)
	switch outcome {
	case keepLocal:
		// Ours goes up with the next commit, on top of theirs.
		res.Merged = append(res.Merged, remote.Path)
		return e.setBase(StoreConversations, st, remote.Path, agreed, nil)
	case keepRemote, replaceLocal:
		res.Merged = append(res.Merged, remote.Path)
		if outcome == replaceLocal {
			where, err := e.trash(remote.Path)
			if err != nil {
				return err
			}
			if where != "" {
				res.Trashed = append(res.Trashed, remote.Path)
			}
		}
		if err := writeFile(e.abs(remote.Path), theirs); err != nil {
			return err
		}
		delete(st.Hashes, remote.Path)
		local[remote.Path] = LocalFile{SHA256: remote.SHA256, Size: int64(len(theirs))}
		res.noteChat(remote.Path, true)
		return e.setBase(StoreConversations, st, remote.Path, agreed, nil)
	}
	forked, b, title, err := forkTheirs(remote.Path, theirs, remote.Device, e.now())
	if err != nil {
		// The server's copy is no chat clai can fork; resolve settles it.
		return e.markConflict(ctx, st, remote, res)
	}
	if err := writeFile(e.abs(forked), b); err != nil {
		return err
	}
	res.Forks = append(res.Forks, Fork{Path: remote.Path, Forked: forked, Title: title})
	res.noteChat(forked, true)
	return e.setBase(StoreConversations, st, remote.Path, agreed, nil)
}

func (r *PullResult) noteChat(rel string, written bool) {
	if !chatFile.MatchString(rel) {
		return
	}
	if written {
		r.written = append(r.written, rel)
	} else {
		r.removed = append(r.removed, rel)
	}
}
