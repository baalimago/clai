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
	chatIndexJournalCompactBytes = 256 << 10
)

func chatIndexJournalPath(convDir string) string {
	return path.Join(convDir, chatIndexJournalName)
}

func chatIndexCompactingPath(convDir string) string {
	return path.Join(convDir, chatIndexCompactingName)
}

func isIndexFile(name string) bool {
	return name == chatIndexFileName || name == chatIndexJournalName || name == chatIndexCompactingName
}

// errCorruptJournal reports a journal line that does not decode.
var errCorruptJournal = errors.New("corrupt chat index journal")

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
	return compactIndexJournal(convDir)
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
// replayed by the next read instead of being lost.
func compactIndexJournal(convDir string) error {
	if err := os.Rename(chatIndexJournalPath(convDir), chatIndexCompactingPath(convDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move chat index journal aside: %w", err)
	}
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
