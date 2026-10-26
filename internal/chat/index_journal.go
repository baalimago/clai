package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// The index journal makes a save cost one appended line instead of a rewrite
// of the whole cache. Readers replay it over the cache, and an upsert that
// grows it past chatIndexJournalCompactBytes folds it back into the cache.
const (
	chatIndexJournalName         = "chat_index.journal"
	chatIndexCompactingName      = "chat_index.journal.compacting"
	chatIndexCompactLockName     = "chat_index.compact.lock"
	chatIndexJournalCompactBytes = 256 << 10
	// A lock older than this belongs to a compactor that died.
	chatIndexCompactLockStale = time.Minute
)

func chatIndexJournalPath(convDir string) string {
	return path.Join(convDir, chatIndexJournalName)
}

func chatIndexCompactingPath(convDir string) string {
	return path.Join(convDir, chatIndexCompactingName)
}

func chatIndexCompactLockPath(convDir string) string {
	return path.Join(convDir, chatIndexCompactLockName)
}

func isIndexFile(name string) bool {
	switch name {
	case chatIndexFileName, chatIndexJournalName, chatIndexCompactingName, chatIndexCompactLockName:
		return true
	}
	return false
}

var (
	// errCorruptJournal reports a journal line that does not decode.
	errCorruptJournal = errors.New("corrupt chat index journal")
	// errCompactionHeld reports that another process is compacting.
	errCompactionHeld = errors.New("chat index compaction held by another process")
)

func appendIndexJournal(convDir string, chats []pub_models.Chat) error {
	now := time.Now().UTC()
	if _, err := os.Stat(chatIndexPath(convDir)); errors.Is(err, os.ErrNotExist) {
		return writeIndexWith(convDir, chats, now)
	}
	var buf bytes.Buffer
	for _, c := range chats {
		row := chatIndexRowFromChat(c)
		row.Updated = now
		line, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode chat index journal row: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(chatIndexJournalPath(convDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open chat index journal: %w", err)
	}
	_, writeErr := f.Write(buf.Bytes())
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("append chat index journal: %w", err)
	}
	info, err := os.Stat(chatIndexJournalPath(convDir))
	if err != nil {
		return fmt.Errorf("stat chat index journal: %w", err)
	}
	if info.Size() < chatIndexJournalCompactBytes {
		return nil
	}
	// The lock holder folds this save in, or the next save compacts.
	if err := compactIndexJournal(convDir); err != nil && !errors.Is(err, errCompactionHeld) {
		return err
	}
	return nil
}

// writeIndexWith builds the cache from scratch with chats upserted. It runs
// when no cache exists yet, so a journal never sits beside a missing cache.
func writeIndexWith(convDir string, chats []pub_models.Chat, now time.Time) error {
	rows, err := readChatIndex(convDir)
	if err != nil {
		return fmt.Errorf("read chat index: %w", err)
	}
	for _, c := range chats {
		row := chatIndexRowFromChat(c)
		row.Updated = now
		rows = replaceIndexRow(rows, row)
	}
	return writeChatIndex(convDir, rows)
}

// compactIndexJournal folds the journal into the cache. The journal is moved
// aside first, so a concurrent save appends to a fresh journal and is
// replayed by the next read instead of being lost. A compacting journal left
// by a crashed compactor is folded in before the move would replace it.
func compactIndexJournal(convDir string) (err error) {
	release, err := acquireCompactLock(convDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if _, err := os.Stat(chatIndexCompactingPath(convDir)); err == nil {
		if err := foldCompactingJournal(convDir); err != nil {
			return fmt.Errorf("recover compacting chat index journal: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat compacting chat index journal: %w", err)
	}
	if err := os.Rename(chatIndexJournalPath(convDir), chatIndexCompactingPath(convDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move chat index journal aside: %w", err)
	}
	return foldCompactingJournal(convDir)
}

func foldCompactingJournal(convDir string) error {
	rows, err := readChatIndex(convDir)
	if err != nil {
		return fmt.Errorf("read chat index for compaction: %w", err)
	}
	// Only the cache is written: the live journal may already hold a save
	// made after the read above.
	if err := writeChatIndexCache(convDir, rows); err != nil {
		return fmt.Errorf("compact chat index: %w", err)
	}
	if err := os.Remove(chatIndexCompactingPath(convDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove compacted chat index journal: %w", err)
	}
	return nil
}

// acquireCompactLock serializes compactors across processes. A stale lock is
// broken once; a live one returns errCompactionHeld.
func acquireCompactLock(convDir string) (release func() error, err error) {
	lock := chatIndexCompactLockPath(convDir)
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if err := f.Close(); err != nil {
				return nil, fmt.Errorf("close chat index compaction lock: %w", err)
			}
			return func() error {
				if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("release chat index compaction lock: %w", err)
				}
				return nil
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("take chat index compaction lock: %w", err)
		}
		info, statErr := os.Stat(lock)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("stat chat index compaction lock: %w", statErr)
		}
		if attempt > 0 || (statErr == nil && time.Since(info.ModTime()) < chatIndexCompactLockStale) {
			return nil, errCompactionHeld
		}
		if statErr == nil {
			if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("break stale chat index compaction lock: %w", err)
			}
		}
	}
}

// replayIndexJournal applies the compacting and live journals, in that
// order, over rows.
func replayIndexJournal(convDir string, rows []chatIndexRow) ([]chatIndexRow, error) {
	for _, p := range []string{chatIndexCompactingPath(convDir), chatIndexJournalPath(convDir)} {
		raw, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read chat index journal: %w", err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for scanner.Scan() {
			if len(scanner.Bytes()) == 0 {
				continue
			}
			var row chatIndexRow
			if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
				return nil, fmt.Errorf("%w %s: %w", errCorruptJournal, p, err)
			}
			rows = replaceIndexRow(rows, row)
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("%w %s: %w", errCorruptJournal, p, err)
		}
	}
	return rows, nil
}

func removeIndexJournals(convDir string) error {
	var errs []error
	for _, p := range []string{chatIndexCompactingPath(convDir), chatIndexJournalPath(convDir)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("remove chat index journal: %w", err)
	}
	return nil
}
