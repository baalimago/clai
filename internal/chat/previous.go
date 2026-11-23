package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	// previousQueryVersion is the record's schema version; a record written by
	// another version is refused rather than misread.
	previousQueryVersion = 1

	// legacyGlobalScopeFile and legacyPrevQueryFile are the full mirrors older
	// versions wrote: a copy of the latest conversation that carried the
	// sentinel id instead of the id of the file holding it. MigratePreviousQuery
	// reads them once; nothing else does.
	legacyGlobalScopeFile = "globalScope.json"
	legacyPrevQueryFile   = "prevQuery.json"
	// legacyGlobalScopeChatID is the sentinel id those mirrors carried.
	legacyGlobalScopeChatID = "globalScope"
)

var (
	// ErrNoPreviousQuery reports that no run has written a previous-query
	// record yet, so there is nothing to reply to.
	ErrNoPreviousQuery = errors.New("no previous query")
	// ErrPreviousQueryGone reports that the record names a conversation whose
	// file no longer exists.
	ErrPreviousQueryGone = errors.New("the previous query's conversation file is gone")
)

// previousQuery is this machine's record of the conversation its latest run
// wrote. It is machine-local state, not a conversation: it is never indexed,
// synced, swept or listed, and its only content is the pointer.
type previousQuery struct {
	Version int       `json:"version"`
	ChatID  string    `json:"chat_id"`
	Updated time.Time `json:"updated"`
}

// PreviousQuery is the record resolved to the conversation it names. Updated is
// when the record was written, which is when that conversation was last the
// latest one.
type PreviousQuery struct {
	Chat    pub_models.Chat
	Updated time.Time
}

// SavePreviousQuery records chatID as the conversation the latest run wrote.
// The record must be written after that conversation's own file, so a crash
// between the two leaves a pointer at a real conversation, never a dangling one.
func SavePreviousQuery(ctx context.Context, confDir, chatID string, now time.Time) error {
	if !safeChatID(chatID) || chatID == legacyGlobalScopeChatID {
		return diag.Errorf("save previous query record: invalid chat id %q", chatID)
	}
	dir := previousQueryStateDir(confDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return diag.Errorf("create previous query state dir %q: %w", dir, err)
	}
	b, err := json.Marshal(previousQuery{Version: previousQueryVersion, ChatID: chatID, Updated: now})
	if err != nil {
		return diag.Errorf("encode previous query record: %w", err)
	}
	path := PreviousQueryPath(confDir)
	if err := utils.WriteFileAtomic(path, append(b, '\n'), 0o644); err != nil {
		return diag.Errorf("write previous query record %q: %w", path, err)
	}
	traceChatf(ctx, "save previous query record path=%q chat_id=%q updated=%q", path, chatID, now.Format(time.RFC3339Nano))
	return nil
}

// LoadPreviousQuery resolves the record to the conversation it names.
//
//   - ErrNoPreviousQuery when no record exists.
//   - ErrPreviousQueryGone when the record names a conversation file that is
//     no longer there.
func LoadPreviousQuery(ctx context.Context, confDir string) (PreviousQuery, error) {
	record, err := readPreviousQuery(ctx, confDir)
	if err != nil {
		return PreviousQuery{}, err
	}
	chat, err := loadConversation(ctx, confDir, conversationPath(confDir, record.ChatID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PreviousQuery{}, diag.Errorf("%w: chat %q", ErrPreviousQueryGone, record.ChatID)
		}
		return PreviousQuery{}, diag.Errorf("load previous query conversation %q: %w", record.ChatID, err)
	}
	traceChatf(ctx, "load previous query record chat_id=%q messages=%d", chat.ID, len(chat.Messages))
	return PreviousQuery{Chat: chat, Updated: record.Updated}, nil
}

func readPreviousQuery(ctx context.Context, confDir string) (previousQuery, error) {
	path := PreviousQueryPath(confDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return previousQuery{}, ErrNoPreviousQuery
		}
		return previousQuery{}, diag.Errorf("read previous query record %q: %w", path, err)
	}
	var record previousQuery
	if err := json.Unmarshal(b, &record); err != nil {
		return previousQuery{}, diag.Errorf("decode previous query record %q: %w", path, err)
	}
	if record.Version != previousQueryVersion {
		return previousQuery{}, diag.Errorf("previous query record %q has version %d, want %d", path, record.Version, previousQueryVersion)
	}
	if !safeChatID(record.ChatID) {
		return previousQuery{}, diag.Errorf("previous query record %q names an invalid chat id %q", path, record.ChatID)
	}
	traceChatf(ctx, "read previous query record path=%q chat_id=%q", path, record.ChatID)
	return record, nil
}

// MigratePreviousQuery writes the previous-query record from the full mirror
// that pre-record versions wrote, then removes that mirror. It is idempotent: an
// existing record, or no legacy mirror, is a no-op.
//
// The mirror's own id is the sentinel, so the conversation it mirrored is
// identified by content: promote copied the conversation's exact message slice,
// so the file whose messages match it is the one the mirror held. A mirror with
// no match (its conversation was deleted, or a pre-uuid id scheme) is left in
// place and reported as a warning; nothing is destroyed to guess.
func MigratePreviousQuery(ctx context.Context, confDir string) error {
	if _, err := readPreviousQuery(ctx, confDir); err == nil {
		return nil
	} else if !errors.Is(err, ErrNoPreviousQuery) {
		return err
	}
	legacyPath, found, err := findLegacyMirror(ctx, confDir)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	mirror, err := loadConversation(ctx, confDir, legacyPath)
	if err != nil {
		return diag.Errorf("read legacy previous query mirror %q: %w", legacyPath, err)
	}
	chatID, matched, err := matchMirrorConversation(ctx, confDir, mirror.Messages)
	if err != nil {
		return err
	}
	if !matched {
		runenv.Warnf(ctx, runenv.SubsystemChat, "previous query record not migrated: no conversation matches %q\n", legacyPath)
		return nil
	}
	updated := mirror.Created
	if updated.IsZero() {
		updated = time.Now()
	}
	if err := SavePreviousQuery(ctx, confDir, chatID, updated); err != nil {
		return diag.Errorf("migrate previous query record: %w", err)
	}
	if err := os.Remove(legacyPath); err != nil {
		return diag.Errorf("remove migrated legacy mirror %q: %w", legacyPath, err)
	}
	traceChatf(ctx, "migrate previous query record legacy=%q chat_id=%q", legacyPath, chatID)
	return nil
}

// findLegacyMirror reports the legacy mirror to migrate: the pre-globalScope
// name first, since the old loader let it overwrite globalScope.json.
func findLegacyMirror(ctx context.Context, confDir string) (string, bool, error) {
	for _, name := range []string{legacyPrevQueryFile, legacyGlobalScopeFile} {
		path := filepath.Join(conversationsDir(confDir), name)
		_, err := os.Stat(path)
		if err == nil {
			return path, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", false, diag.Errorf("stat legacy previous query mirror %q: %w", path, err)
		}
	}
	return "", false, nil
}

// matchMirrorConversation returns the id of the conversation whose messages are
// the mirror's. Among identical candidates (a fork and its parent) the newest
// file wins, which is the one the mirror was written from.
func matchMirrorConversation(ctx context.Context, confDir string, mirrorMessages []pub_models.Message) (string, bool, error) {
	convDir := conversationsDir(confDir)
	entries, err := os.ReadDir(convDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, diag.Errorf("list conversations for previous query migration: %w", err)
	}
	bestID := ""
	var bestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || isLegacyMirrorFile(entry.Name()) {
			continue
		}
		candidate, err := loadConversation(ctx, confDir, filepath.Join(convDir, entry.Name()))
		if err != nil {
			continue
		}
		if !sameMessages(candidate.Messages, mirrorMessages) {
			continue
		}
		mod := entryModTime(entry)
		if bestID == "" || mod.After(bestMod) {
			bestID, bestMod = candidate.ID, mod
		}
	}
	return bestID, bestID != "", nil
}

// sameMessages reports whether two message chains are the same conversation.
// Reasoning items are out-of-band sidecar data keyed by chat id, so the mirror
// never carries them and they are excluded from the comparison.
func sameMessages(a, b []pub_models.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, right := a[i], b[i]
		left.ReasoningItems, right.ReasoningItems = nil, nil
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func isLegacyMirrorFile(name string) bool {
	return name == legacyGlobalScopeFile || name == legacyPrevQueryFile
}

// clearPreviousQueryRecord removes a record that names chatID, so a deleted
// conversation is not reported as a dangling pointer by the next reply.
func clearPreviousQueryRecord(ctx context.Context, confDir, chatID string) error {
	record, err := readPreviousQuery(ctx, confDir)
	if err != nil {
		if errors.Is(err, ErrNoPreviousQuery) {
			return nil
		}
		return err
	}
	if record.ChatID != chatID {
		return nil
	}
	path := PreviousQueryPath(confDir)
	if err := os.Remove(path); err != nil {
		return diag.Errorf("remove previous query record %q: %w", path, err)
	}
	traceChatf(ctx, "clear previous query record path=%q chat_id=%q", path, chatID)
	return nil
}
