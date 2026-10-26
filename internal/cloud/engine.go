package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

const (
	missingBatch = 1000
	commitBatch  = 200
	// syncRounds bounds pull, push, pull again when another device commits
	// between our pull and our push.
	syncRounds = 3
)

// chatFile matches a top-level conversation, the only kind of file a
// conflict merges instead of setting aside.
var chatFile = regexp.MustCompile(`^conversations/[^/]+\.json$`)

// Hooks are the conversation-side collaborators internal/cli injects, so
// this package never imports internal/chat.
type Hooks struct {
	// Export writes the conversations clai can import from other tools
	// under conversations/imported, before the conversation store scans.
	Export func(ctx context.Context, confDir string) error
	// Pulled is told which conversation files a pull wrote or removed, so
	// the chat index can follow.
	Pulled func(confDir string, written, removed []string) error
}

// Engine syncs the stores of one config dir with one account.
type Engine struct {
	Client  *Client
	ConfDir string
	Hooks   Hooks
	Now     func() time.Time
}

// Skip is a local file a push left out, and why.
type Skip struct {
	Path   string
	Reason string
}

// Report is what one store's sync did.
type Report struct {
	Store      Store
	Downloaded int
	Uploaded   int
	Removed    int
	// Conflicts names each path both sides changed, with where the local
	// copy went.
	Conflicts []string
	Skipped   []Skip
	// written and removed chat files, for Hooks.Pulled.
	written, removed []string
}

// SyncError wraps a failed sync with the store it was syncing.
type SyncError struct {
	Store Store
	Cause error
}

func (e *SyncError) Error() string { return fmt.Sprintf("sync %s: %v", e.Store, e.Cause) }
func (e *SyncError) Unwrap() error { return e.Cause }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Sync pulls the store, pushes local changes, and pulls again when a
// commit lost a path to another device.
func (e *Engine) Sync(ctx context.Context, store Store) (Report, error) {
	rep := Report{Store: store}
	if store == StoreConversations && e.Hooks.Export != nil {
		if err := e.Hooks.Export(ctx, e.ConfDir); err != nil {
			rep.Skipped = append(rep.Skipped, Skip{Path: "conversations/imported", Reason: err.Error()})
		}
	}
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return rep, &SyncError{Store: store, Cause: err}
	}
	for range syncRounds {
		if err := e.pull(ctx, store, st, &rep); err != nil {
			return rep, e.finish(store, st, &rep, err)
		}
		conflicted, err := e.push(ctx, store, st, &rep)
		if err != nil {
			return rep, e.finish(store, st, &rep, err)
		}
		if !conflicted {
			break
		}
	}
	return rep, e.finish(store, st, &rep, nil)
}

func (e *Engine) finish(store Store, st *state, rep *Report, cause error) error {
	errs := []error{cause}
	errs = append(errs, st.save(e.ConfDir, store))
	if store == StoreConversations && e.Hooks.Pulled != nil && (len(rep.written) > 0 || len(rep.removed) > 0) {
		errs = append(errs, e.Hooks.Pulled(e.ConfDir, rep.written, rep.removed))
	}
	if err := errors.Join(errs...); err != nil {
		return &SyncError{Store: store, Cause: err}
	}
	return nil
}

func (e *Engine) abs(rel string) string { return filepath.Join(e.ConfDir, filepath.FromSlash(rel)) }

func (e *Engine) pull(ctx context.Context, store Store, st *state, rep *Report) error {
	local, err := scan(e.ConfDir, store, st.Hashes)
	if err != nil {
		return err
	}
	for {
		page, err := e.Client.Changes(ctx, store, st.Cursor)
		if err != nil {
			return err
		}
		for _, remote := range page.Entries {
			if !Allowed(store, remote.Path) {
				continue
			}
			if err := e.apply(ctx, store, st, local, remote, rep); err != nil {
				return err
			}
		}
		st.Cursor = page.Cursor
		if err := st.save(e.ConfDir, store); err != nil {
			return err
		}
		if !page.More {
			return nil
		}
	}
}

// apply brings one remote entry into the config dir.
func (e *Engine) apply(ctx context.Context, store Store, st *state, local map[string]LocalFile, remote RemoteEntry, rep *Report) error {
	base, hasBase := st.Entries[remote.Path]
	if hasBase && base.Rev >= remote.Rev {
		// Our own commit coming back through the feed.
		return nil
	}
	loc, hasLocal := local[remote.Path]
	unchanged := hasLocal && hasBase && !base.Deleted && loc.SHA256 == base.SHA256
	switch {
	case remote.Deleted:
		if unchanged {
			if err := os.Remove(e.abs(remote.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			delete(local, remote.Path)
			rep.Removed++
			rep.noteChat(remote.Path, false)
		}
		// A file edited here after another device deleted it stays, and
		// the push recreates it.
		st.Entries[remote.Path] = baseEntry{Rev: remote.Rev, Deleted: true}
		return nil
	case hasLocal && loc.SHA256 == remote.SHA256:
		st.Entries[remote.Path] = baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}
		return nil
	case !hasLocal, unchanged:
		// Missing here (never had it, or deleted it while another device
		// changed it: keeping data beats losing it), or untouched here.
		return e.download(ctx, st, local, remote, rep)
	}
	return e.conflict(ctx, store, st, local, remote, rep)
}

func (e *Engine) download(ctx context.Context, st *state, local map[string]LocalFile, remote RemoteEntry, rep *Report) error {
	b, err := e.Client.GetBlob(ctx, remote.SHA256)
	if err != nil {
		return err
	}
	if err := e.write(remote.Path, b, remote.SHA256); err != nil {
		return err
	}
	st.Entries[remote.Path] = baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}
	delete(st.Hashes, remote.Path)
	local[remote.Path] = LocalFile{SHA256: remote.SHA256, Size: int64(len(b))}
	rep.Downloaded++
	rep.noteChat(remote.Path, true)
	return nil
}

// ChecksumError is a downloaded blob whose bytes do not hash to its name.
type ChecksumError struct{ Path, Want, Got string }

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s: downloaded bytes hash to %s, want %s", e.Path, e.Got, e.Want)
}

func (e *Engine) write(rel string, b []byte, want string) error {
	if got := hashBytes(b); want != "" && got != want {
		return &ChecksumError{Path: rel, Want: want, Got: got}
	}
	p := e.abs(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return utils.WriteFileAtomic(p, b, 0o644)
}

// conflict settles a path both sides changed. A conversation one side only
// extended keeps the longer copy; one that diverged keeps both, the local
// copy as a fork. Anything else takes the server's copy and sets the
// local one aside under cloud/conflicts.
func (e *Engine) conflict(ctx context.Context, store Store, st *state, local map[string]LocalFile, remote RemoteEntry, rep *Report) error {
	mine, err := os.ReadFile(e.abs(remote.Path))
	if err != nil {
		return err
	}
	if store == StoreConversations && chatFile.MatchString(remote.Path) {
		theirs, err := e.Client.GetBlob(ctx, remote.SHA256)
		if err != nil {
			return err
		}
		switch mergeChats(mine, theirs) {
		case keepLocal:
			// The push sends ours on top of theirs.
			st.Entries[remote.Path] = baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}
			return nil
		case keepRemote:
		case keepBoth:
			forked, err := e.fork(remote.Path, mine)
			if err != nil {
				return err
			}
			rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s: kept this machine's copy as %s", remote.Path, forked))
			rep.noteChat(forked, true)
		}
		if err := e.write(remote.Path, theirs, remote.SHA256); err != nil {
			return err
		}
		st.Entries[remote.Path] = baseEntry{Rev: remote.Rev, SHA256: remote.SHA256}
		delete(st.Hashes, remote.Path)
		local[remote.Path] = LocalFile{SHA256: remote.SHA256, Size: int64(len(theirs))}
		rep.Downloaded++
		rep.noteChat(remote.Path, true)
		return nil
	}
	aside := path.Join(DirName, "conflicts", remote.Path+"."+e.now().UTC().Format("20060102T150405Z"))
	if err := e.write(aside, mine, ""); err != nil {
		return err
	}
	rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s: took clai.chat's copy, this machine's is at %s", remote.Path, aside))
	return e.download(ctx, st, local, remote, rep)
}

// fork writes a diverged local conversation as a new chat that names the
// one it split from, the way clai marks any fork.
func (e *Engine) fork(rel string, mine []byte) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(mine, &doc); err != nil {
		return "", fmt.Errorf("fork %s: %w", rel, err)
	}
	id := newChatID(e.now())
	parent := strings.TrimSuffix(path.Base(rel), ".json")
	for k, v := range map[string]string{"id": id, "source": "clai", "source_id": parent} {
		b, _ := json.Marshal(v)
		doc[k] = b
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	forked := path.Join("conversations", id+".json")
	return forked, e.write(forked, b, "")
}

// newChatID matches internal/chat.NewChatID's shape.
func newChatID(now time.Time) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%s", now.Unix(), hex.EncodeToString(b))
}

func (r *Report) noteChat(rel string, written bool) {
	if !chatFile.MatchString(rel) {
		return
	}
	if written {
		r.written = append(r.written, rel)
	} else {
		r.removed = append(r.removed, rel)
	}
}

type mergeOutcome int

const (
	keepRemote mergeOutcome = iota
	keepLocal
	keepBoth
)

// mergeChats compares two copies of one conversation by their messages.
func mergeChats(mine, theirs []byte) mergeOutcome {
	var a, b struct {
		Messages []json.RawMessage `json:"messages"`
		Title    string            `json:"title"`
	}
	if json.Unmarshal(mine, &a) != nil || json.Unmarshal(theirs, &b) != nil {
		return keepBoth
	}
	shorter, longer := a.Messages, b.Messages
	if len(a.Messages) > len(b.Messages) {
		shorter, longer = b.Messages, a.Messages
	}
	for i := range shorter {
		if !jsonEqual(shorter[i], longer[i]) {
			return keepBoth
		}
	}
	switch {
	case len(a.Messages) > len(b.Messages):
		return keepLocal
	case len(a.Messages) < len(b.Messages):
		return keepRemote
	case a.Title != "" && b.Title == "":
		return keepLocal
	}
	return keepRemote
}

func jsonEqual(x, y json.RawMessage) bool {
	var bx, by bytes.Buffer
	if json.Compact(&bx, x) != nil || json.Compact(&by, y) != nil {
		return bytes.Equal(x, y)
	}
	return bytes.Equal(bx.Bytes(), by.Bytes())
}

// plan lists the changes a push sends: every file whose bytes differ from
// the last agreed ones, and every agreed file that is gone.
func (e *Engine) plan(store Store, st *state, local map[string]LocalFile, rep *Report) []Change {
	var out []Change
	quota := Quotas[store]
	paths := make([]string, 0, len(local))
	for p := range local {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		loc := local[p]
		base, ok := st.Entries[p]
		if ok && !base.Deleted && base.SHA256 == loc.SHA256 {
			continue
		}
		if loc.Size > quota.FileSize {
			rep.Skipped = append(rep.Skipped, Skip{Path: p, Reason: fmt.Sprintf("larger than the %d MB a file may be", quota.FileSize/1_000_000)})
			continue
		}
		if store == StoreConfig {
			b, err := os.ReadFile(e.abs(p))
			if err != nil {
				continue
			}
			if secret, field := holdsSecret(p, b); secret {
				rep.Skipped = append(rep.Skipped, Skip{Path: p, Reason: "holds a credential (" + field + "); keep keys in env vars or an envfile"})
				continue
			}
		}
		out = append(out, Change{Path: p, BaseRev: base.Rev, SHA256: loc.SHA256, Size: loc.Size})
	}
	gone := []string{}
	for p, base := range st.Entries {
		if _, ok := local[p]; !ok && !base.Deleted {
			gone = append(gone, p)
		}
	}
	slices.Sort(gone)
	for _, p := range gone {
		out = append(out, Change{Path: p, BaseRev: st.Entries[p].Rev, Deleted: true})
	}
	return out
}

// push uploads the bytes the server lacks and commits; it reports whether
// any path lost to another device, so the caller pulls again.
func (e *Engine) push(ctx context.Context, store Store, st *state, rep *Report) (bool, error) {
	local, err := scan(e.ConfDir, store, st.Hashes)
	if err != nil {
		return false, err
	}
	changes := e.plan(store, st, local, rep)
	if len(changes) == 0 {
		return false, nil
	}
	changes, err = e.upload(ctx, changes, rep)
	if err != nil {
		return false, err
	}
	conflicted := false
	for start := 0; start < len(changes); start += commitBatch {
		batch := changes[start:min(start+commitBatch, len(changes))]
		res, err := e.Client.Commit(ctx, store, batch)
		if err != nil {
			return conflicted, err
		}
		byPath := map[string]Change{}
		for _, c := range batch {
			byPath[c.Path] = c
		}
		for _, a := range res.Applied {
			c := byPath[a.Path]
			st.Entries[a.Path] = baseEntry{Rev: a.Rev, SHA256: c.SHA256, Deleted: c.Deleted}
		}
		conflicted = conflicted || len(res.Conflicts) > 0
		if err := st.save(e.ConfDir, store); err != nil {
			return conflicted, err
		}
	}
	return conflicted, nil
}

// upload sends every blob the server is missing. A file that changed
// since the scan is dropped from this push; the next sync sends it.
func (e *Engine) upload(ctx context.Context, changes []Change, rep *Report) ([]Change, error) {
	pathsBySHA := map[string][]string{}
	var hashes []string
	for _, c := range changes {
		if c.Deleted {
			continue
		}
		if _, ok := pathsBySHA[c.SHA256]; !ok {
			hashes = append(hashes, c.SHA256)
		}
		pathsBySHA[c.SHA256] = append(pathsBySHA[c.SHA256], c.Path)
	}
	dropped := map[string]bool{}
	for start := 0; start < len(hashes); start += missingBatch {
		missing, err := e.Client.Missing(ctx, hashes[start:min(start+missingBatch, len(hashes))])
		if err != nil {
			return nil, err
		}
		for _, sha := range missing {
			paths := pathsBySHA[sha]
			if len(paths) == 0 {
				continue
			}
			b, err := os.ReadFile(e.abs(paths[0]))
			if err != nil || hashBytes(b) != sha {
				for _, p := range paths {
					dropped[p] = true
				}
				continue
			}
			if err := e.Client.PutBlob(ctx, sha, b); err != nil {
				return nil, err
			}
			rep.Uploaded++
		}
	}
	if len(dropped) == 0 {
		return changes, nil
	}
	return slices.DeleteFunc(changes, func(c Change) bool { return dropped[c.Path] }), nil
}
