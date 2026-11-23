package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/diag"
)

// ConversationPuller pulls one conversation from the cloud store; the cloud copy keeps the ID. Engine implements it.
type ConversationPuller interface {
	PullConversation(ctx context.Context, id string) (ConversationPull, error)
}

type ConversationPull struct {
	Outcome   ConversationOutcome
	ID        string // the pulled conversation
	TrashPath string // where the old local file went, under cloud/trash/, when it was moved
	ForkID    string // LocalForked: the ID of fork L
}

type ConversationOutcome int

const (
	ConversationUnchanged ConversationOutcome = iota + 1
	ConversationAdopted
	ConversationReplaced
	ConversationLocalForked
)

type ConversationNotFoundError struct{ ID string }

func (e *ConversationNotFoundError) Error() string {
	return fmt.Sprintf("conversation %s is not in the account's conversation store", e.ID)
}

type InvalidConversationIDError struct{ ID string }

func (e *InvalidConversationIDError) Error() string {
	return fmt.Sprintf("%q cannot name a conversation in the account's conversation store", e.ID)
}

type MalformedConversationError struct {
	ID    string
	Cause error
}

func (e *MalformedConversationError) Error() string {
	return fmt.Sprintf("the cloud copy of conversation %s is malformed: %v", e.ID, e.Cause)
}
func (e *MalformedConversationError) Unwrap() error { return e.Cause }

type LocalConversationError struct {
	ID    string
	Cause error
}

func (e *LocalConversationError) Error() string {
	return fmt.Sprintf("the local copy of conversation %s cannot be read: %v", e.ID, e.Cause)
}
func (e *LocalConversationError) Unwrap() error { return e.Cause }

type MissingBlobError struct{ Path, SHA256 string }

func (e *MissingBlobError) Error() string {
	if e.SHA256 == "" {
		return fmt.Sprintf("%s is not in the account's conversation store", e.Path)
	}
	return fmt.Sprintf("the server lacks the blob %s of %s", e.SHA256, e.Path)
}

type LinkedConversationError struct{ Path string }

func (e *LinkedConversationError) Error() string {
	return fmt.Sprintf("%s reaches through a symbolic link, so it is left alone", e.Path)
}

// RemoteFile is one file of the store with its bytes.
type RemoteFile struct {
	Entry RemoteEntry
	Bytes []byte
}

// cloudConversation is every file of one conversation as the store holds it.
type cloudConversation struct {
	ID     string
	Chat   RemoteFile   // conversations/<id>.json
	Notes  []RemoteFile // conversations/reasoning/<id>/*
	Images []RemoteFile // referenced images this machine lacks
	// Held are the referenced images this machine already has.
	Held []RemoteEntry
}

const (
	reasoningDir = "conversations/reasoning/"
	assetsDir    = "conversations/assets/"
)

func conversationPath(id string) string { return "conversations/" + id + ".json" }

// PullConversation brings conversation id from the store and settles it against the local copy.
func (e *Engine) PullConversation(ctx context.Context, id string) (ConversationPull, error) {
	if !Allowed(StoreConversations, conversationPath(id)) {
		return ConversationPull{}, &InvalidConversationIDError{ID: id}
	}
	st, err := loadState(e.ConfDir, StoreConversations)
	if err != nil {
		return ConversationPull{}, err
	}
	c, err := e.fetchConversation(ctx, id)
	if err != nil {
		return ConversationPull{}, err
	}
	if err := ctx.Err(); err != nil {
		return ConversationPull{}, err
	}
	if err := e.checkLinks(c); err != nil {
		return ConversationPull{}, err
	}
	local, err := e.readLocal(id)
	if err != nil {
		return ConversationPull{}, err
	}
	outcome := ConversationAdopted
	if local != nil {
		outcome = settle(local, c.Chat.Bytes)
	}
	res, err := e.putAside(id, local, outcome, c)
	if err != nil {
		return ConversationPull{}, err
	}
	written, err := e.writeCloud(c)
	if err != nil {
		return ConversationPull{}, err
	}
	if err := e.recordInSync(st, c, written); err != nil {
		return ConversationPull{}, err
	}
	hooked := []string{conversationPath(id)}
	if res.ForkID != "" {
		hooked = append(hooked, conversationPath(res.ForkID))
	}
	return res, errors.Join(e.pruneTrash(), e.hookPulled(ctx, hooked, nil))
}

func (e *Engine) fetchConversation(ctx context.Context, id string) (cloudConversation, error) {
	entries, _, err := e.api().allChanges(ctx, StoreConversations, 0)
	if err != nil {
		return cloudConversation{}, err
	}
	live := map[string]RemoteEntry{}
	for _, entry := range entries {
		if !entry.Deleted {
			live[entry.Path] = entry
		}
	}
	chatEntry, ok := live[conversationPath(id)]
	if !ok {
		return cloudConversation{}, &ConversationNotFoundError{ID: id}
	}
	c := cloudConversation{ID: id}
	if c.Chat, err = e.fetchEntry(ctx, chatEntry); err != nil {
		return cloudConversation{}, err
	}
	refs, err := cloudRefs(c.Chat.Bytes)
	if err != nil {
		return cloudConversation{}, &MalformedConversationError{ID: id, Cause: err}
	}
	for _, p := range slices.Sorted(maps.Keys(live)) {
		if strings.HasPrefix(p, reasoningDir+id+"/") && Allowed(StoreConversations, p) {
			note, err := e.fetchEntry(ctx, live[p])
			if err != nil {
				return cloudConversation{}, err
			}
			c.Notes = append(c.Notes, note)
		}
	}
	for _, ref := range refs {
		p := assetsDir + ref
		if !Allowed(StoreConversations, p) {
			return cloudConversation{}, &MalformedConversationError{ID: id, Cause: fmt.Errorf("image %q is not a name the store allows", ref)}
		}
		entry, ok := live[p]
		if !ok {
			return cloudConversation{}, &MissingBlobError{Path: p}
		}
		if _, err := os.Stat(e.abs(p)); err == nil {
			c.Held = append(c.Held, entry)
			continue
		}
		img, err := e.fetchEntry(ctx, entry)
		if err != nil {
			return cloudConversation{}, err
		}
		c.Images = append(c.Images, img)
	}
	return c, nil
}

func cloudRefs(chat []byte) ([]string, error) {
	var shape chatShape
	if err := json.Unmarshal(chat, &shape); err != nil {
		return nil, err
	}
	return asset.RefsIn(chat)
}

func (e *Engine) fetchEntry(ctx context.Context, entry RemoteEntry) (RemoteFile, error) {
	b, err := e.fetch(ctx, entry.Path, entry.SHA256)
	if apiErr, ok := cloud.AsAPIError(err); ok && apiErr.Status == 404 {
		return RemoteFile{}, &MissingBlobError{Path: entry.Path, SHA256: entry.SHA256}
	}
	if err != nil {
		return RemoteFile{}, err
	}
	return RemoteFile{Entry: entry, Bytes: b}, nil
}

func (e *Engine) checkLinks(c cloudConversation) error {
	paths := []string{c.Chat.Entry.Path, reasoningDir + c.ID}
	for _, f := range append(slices.Clone(c.Notes), c.Images...) {
		paths = append(paths, f.Entry.Path)
	}
	for _, p := range paths {
		linked, err := e.linkage(p)
		if err != nil {
			return err
		}
		if linked == Linked {
			return &LinkedConversationError{Path: p}
		}
	}
	return nil
}

// readLocal answers the local chat file's bytes, or nil when there is none.
func (e *Engine) readLocal(id string) ([]byte, error) {
	b, err := os.ReadFile(e.abs(conversationPath(id)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &LocalConversationError{ID: id, Cause: err}
	}
	var shape chatShape
	if err := json.Unmarshal(b, &shape); err != nil {
		return nil, &LocalConversationError{ID: id, Cause: err}
	}
	return b, nil
}

// settle is mergeChats, but a local copy with a message the cloud lacks is forked.
func settle(local, cloud []byte) ConversationOutcome {
	var l, c chatShape
	_ = json.Unmarshal(local, &l)
	_ = json.Unmarshal(cloud, &c)
	switch mergeChats(local, cloud) {
	case keepBoth:
		return ConversationLocalForked
	case keepLocal:
		if len(l.Messages) > len(c.Messages) {
			return ConversationLocalForked
		}
	case keepRemote:
		if len(l.Messages) < len(c.Messages) {
			return ConversationReplaced
		}
	}
	return ConversationUnchanged
}

func (e *Engine) putAside(id string, local []byte, outcome ConversationOutcome, c cloudConversation) (ConversationPull, error) {
	res := ConversationPull{Outcome: outcome, ID: id}
	switch outcome {
	case ConversationUnchanged, ConversationReplaced:
		if bytes.Equal(local, c.Chat.Bytes) {
			return res, nil
		}
		where, err := e.trash(conversationPath(id))
		if err != nil {
			return ConversationPull{}, err
		}
		res.TrashPath = where
	case ConversationLocalForked:
		forkID, err := e.forkLocal(id, local, c.Chat.Bytes)
		if err != nil {
			return ConversationPull{}, err
		}
		res.ForkID = forkID
	}
	return res, nil
}

func (e *Engine) forkLocal(id string, local, cloudChat []byte) (string, error) {
	mint := cmpNewID(e.newID)
	forkID, err := mint()
	if err != nil {
		return "", diag.Errorf("make an id for the fork of %s: %w", id, err)
	}
	stamp := forkStamp{Created: e.forkCreated(local, cloudChat)}
	rel, b, _, err := forkCopy(conversationPath(id), local, forkID, stamp)
	if err != nil {
		return "", err
	}
	sidecars, err := e.sidecarFiles(id)
	if err != nil {
		return "", err
	}
	for name, content := range sidecars {
		if err := writeFile(e.abs(reasoningDir+forkID+"/"+name), content); err != nil {
			return "", err
		}
	}
	return forkID, writeFile(e.abs(rel), b)
}

func cmpNewID(f func() (string, error)) func() (string, error) {
	if f != nil {
		return f
	}
	return chatid.New
}

func (e *Engine) sidecarFiles(id string) (map[string][]byte, error) {
	entries, err := os.ReadDir(e.abs(reasoningDir + id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(e.abs(reasoningDir + id + "/" + entry.Name()))
		if err != nil {
			return nil, err
		}
		out[entry.Name()] = b
	}
	return out, nil
}

type queryKey struct {
	At    time.Time `json:"created_at"`
	Model string    `json:"model"`
}

// forkCreated is the time of the first local query the cloud copy lacks, or now.
func (e *Engine) forkCreated(local, cloudChat []byte) time.Time {
	var l, c struct {
		Queries []queryKey `json:"queries"`
	}
	_ = json.Unmarshal(local, &l)
	_ = json.Unmarshal(cloudChat, &c)
	for _, q := range l.Queries {
		known := slices.ContainsFunc(c.Queries, func(o queryKey) bool { return o.At.Equal(q.At) && o.Model == q.Model })
		if !known && !q.At.IsZero() {
			return q.At
		}
	}
	return e.now()
}

func (e *Engine) writeCloud(c cloudConversation) ([]string, error) {
	var written []string
	for _, f := range c.Images {
		if err := writeFile(e.abs(f.Entry.Path), f.Bytes); err != nil {
			return nil, err
		}
		written = append(written, f.Entry.Path)
	}
	if err := e.removeStaleNotes(c); err != nil {
		return nil, err
	}
	for _, f := range c.Notes {
		if err := writeFile(e.abs(f.Entry.Path), f.Bytes); err != nil {
			return nil, err
		}
		written = append(written, f.Entry.Path)
	}
	if err := writeFile(e.abs(c.Chat.Entry.Path), c.Chat.Bytes); err != nil {
		return nil, err
	}
	return append(written, c.Chat.Entry.Path), nil
}

func (e *Engine) removeStaleNotes(c cloudConversation) error {
	keep := map[string]bool{}
	for _, f := range c.Notes {
		keep[path.Base(f.Entry.Path)] = true
	}
	local, err := e.sidecarFiles(c.ID)
	if err != nil {
		return err
	}
	for name := range local {
		if !keep[name] {
			if err := removeFile(e.abs(reasoningDir + c.ID + "/" + name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) recordInSync(st *state, c cloudConversation, written []string) error {
	entries := []RemoteEntry{c.Chat.Entry}
	for _, f := range c.Notes {
		entries = append(entries, f.Entry)
	}
	for _, f := range c.Images {
		entries = append(entries, f.Entry)
	}
	entries = append(entries, c.Held...)
	for _, entry := range entries {
		delete(st.Conflicts, entry.Path)
		if err := removeFile(TheirsPath(e.ConfDir, entry.Path)); err != nil {
			return err
		}
		base := baseEntry{Rev: entry.Rev, SHA256: entry.SHA256}
		if err := e.setBase(StoreConversations, st, entry.Path, base, nil); err != nil {
			return err
		}
	}
	for _, p := range written {
		delete(st.Hashes, p)
	}
	prefix := reasoningDir + c.ID + "/"
	for p := range st.Entries {
		if strings.HasPrefix(p, prefix) && !slices.ContainsFunc(c.Notes, func(f RemoteFile) bool { return f.Entry.Path == p }) {
			delete(st.Entries, p)
			delete(st.Hashes, p)
		}
	}
	return st.save(e.ConfDir, StoreConversations)
}
