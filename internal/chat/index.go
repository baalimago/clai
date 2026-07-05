package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const chatIndexFileName = "chat_index.cache"

// chatIndexVersion is bumped when the cache schema changes incompatibly.
//
//	2 — GroupKey field added (content-hash grouping)
const chatIndexVersion = 2

type chatIndexCache struct {
	Version int            `json:"version"`
	Rows    []chatIndexRow `json:"rows"`
}

type chatIndexRow struct {
	ID               string    `json:"id"`
	Created          time.Time `json:"created"`
	Source           string    `json:"source,omitempty"`
	SourceID         string    `json:"source_id,omitempty"`
	Profile          string    `json:"profile,omitempty"`
	Model            string    `json:"model,omitempty"`
	MessageCount     int       `json:"message_count"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	TotalCostUSD     float64   `json:"total_cost_usd,omitempty"`
	FirstUserMessage string    `json:"first_user_message,omitempty"`
	// OriginDir mirrors Chat.OriginDir so directory-anchored search can filter
	// candidates from the index without opening every conversation file.
	OriginDir string `json:"origin_dir,omitempty"`
	// GroupKey mirrors Chat.GroupKey; see Chat.GroupKey for semantics.
	GroupKey string `json:"group_key,omitempty"`
}

type ChatIndexPaginator struct {
	rows []chatIndexRow
}

func (cp *ChatIndexPaginator) Len() int {
	return len(cp.rows)
}

func (cp *ChatIndexPaginator) Page(start, offset int) ([]chatIndexRow, error) {
	if start < 0 {
		return nil, fmt.Errorf("start index %d below zero", start)
	}
	if offset < 0 {
		return nil, fmt.Errorf("offset %d below zero", offset)
	}
	if start >= len(cp.rows) {
		return []chatIndexRow{}, nil
	}
	end := min(start+offset, len(cp.rows))
	return cp.rows[start:end], nil
}

func chatIndexPath(convDir string) string {
	return path.Join(convDir, chatIndexFileName)
}

func aggregateQueryTotalTokens(queries []pub_models.QueryCost) int {
	total := 0
	for _, query := range queries {
		total += query.Usage.TotalTokens
	}
	return total
}

func chatIndexRowFromChat(chat pub_models.Chat) chatIndexRow {
	row := chatIndexRow{
		ID:           chat.ID,
		Created:      chat.Created,
		Source:       chat.Source,
		SourceID:     chat.SourceID,
		Profile:      chat.Profile,
		MessageCount: len(chat.Messages),
		TotalCostUSD: chat.TotalCostUSD(),
		OriginDir:    chat.OriginDir,
		GroupKey:     chat.GroupKey,
	}
	if len(chat.Queries) > 0 {
		row.TotalTokens = aggregateQueryTotalTokens(chat.Queries)
	}
	if row.TotalTokens == 0 && chat.TokenUsage != nil {
		row.TotalTokens = chat.TokenUsage.TotalTokens
	}
	for i := len(chat.Queries) - 1; i >= 0; i-- {
		if chat.Queries[i].Model == "" {
			continue
		}
		row.Model = chat.Queries[i].Model
		break
	}
	if msg, err := chat.FirstUserMessage(); err == nil {
		row.FirstUserMessage = msg.Content
	}
	return row
}

func readChatIndex(convDir string) ([]chatIndexRow, error) {
	b, err := os.ReadFile(chatIndexPath(convDir))
	if err != nil {
		if os.IsNotExist(err) {
			rows, rebuildErr := rebuildChatIndex(convDir, 0, "cache missing")
			if rebuildErr != nil {
				return nil, fmt.Errorf("failed to rebuild missing chat index: %w", rebuildErr)
			}
			return rows, nil
		}
		return nil, fmt.Errorf("failed to read chat index: %w", err)
	}

	// Try versioned wrapper first; fall back to legacy unwrapped array.
	var cache chatIndexCache
	if err := json.Unmarshal(b, &cache); err != nil || cache.Version == 0 {
		// Legacy format: plain array of rows, treated as v1.
		var rows []chatIndexRow
		if err := json.Unmarshal(b, &rows); err != nil {
			// Both formats failed — cache is corrupted; rebuild from scratch.
			utils.ClearLine(os.Stderr)
			rows, rebuildErr := rebuildChatIndex(convDir, 0, "corrupted cache")
			if rebuildErr != nil {
				return nil, fmt.Errorf("failed to rebuild corrupted chat index: %w", rebuildErr)
			}
			return rows, nil
		}
		rows, rebuildErr := rebuildChatIndex(convDir, 1, "legacy cache (no version) → v2: GroupKey hashing")
		if rebuildErr != nil {
			return nil, fmt.Errorf("failed to rebuild chat index for migration: %w", rebuildErr)
		}
		return rows, nil
	}

	if cache.Version < chatIndexVersion {
		reason := fmt.Sprintf("v%d → v%d: schema upgrade", cache.Version, chatIndexVersion)
		rows, rebuildErr := rebuildChatIndex(convDir, cache.Version, reason)
		if rebuildErr != nil {
			return nil, fmt.Errorf("failed to rebuild chat index for version upgrade: %w", rebuildErr)
		}
		return rows, nil
	}

	slices.SortFunc(cache.Rows, func(a, b chatIndexRow) int {
		return b.Created.Compare(a.Created)
	})
	return cache.Rows, nil
}

func rebuildChatIndex(convDir string, fromVersion int, reason string) ([]chatIndexRow, error) {
	files, err := os.ReadDir(convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations for index rebuild: %w", err)
	}
	total := 0
	for _, f := range files {
		if !f.IsDir() && f.Name() != chatIndexFileName {
			total++
		}
	}

	// Build the header line: explain why we're rebuilding.
	if fromVersion == 0 {
		fmt.Fprintf(os.Stderr, "Building cache index v%d (%s):\n", chatIndexVersion, reason)
	} else {
		fmt.Fprintf(os.Stderr, "Rebuilding cache index %s:\n", reason)
	}

	rows := make([]chatIndexRow, 0, total)
	processed := 0
	batchStart := time.Now()
	batchSize := 100

	for _, file := range files {
		if file.IsDir() || file.Name() == chatIndexFileName {
			continue
		}
		chatPath := path.Join(convDir, file.Name())
		chat, err := FromPath(chatPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load chat %q while rebuilding index: %w", chatPath, err)
		}
		// Stamp GroupKey for pre-existing conversations that lack it
		// (saved before the GroupKey feature was added).
		if chat.GroupKey == "" {
			chat.GroupKey = ComputeGroupKey(chat)
		}
		rows = append(rows, chatIndexRowFromChat(chat))
		processed++
		if processed%batchSize == 0 || processed == total {
			elapsed := time.Since(batchStart)
			itemsPerSec := float64(batchSize) / elapsed.Seconds()
			remaining := total - processed
			estSec := float64(remaining) / itemsPerSec
			est := time.Duration(estSec) * time.Second
			pct := float64(processed) / float64(total) * 100
			utils.ClearLine(os.Stderr)
			fmt.Fprintf(os.Stderr, "  %d/%d (%.0f%%, est. left: %v)", processed, total, pct, est.Truncate(time.Second))
			batchStart = time.Now()
		}
	}
	if total > 0 {
		fmt.Fprint(os.Stderr, "\n")
	}
	if err := writeChatIndex(convDir, rows); err != nil {
		return nil, fmt.Errorf("failed to persist rebuilt chat index: %w", err)
	}
	return rows, nil
}

func writeChatIndex(convDir string, rows []chatIndexRow) error {
	slices.SortFunc(rows, func(a, b chatIndexRow) int {
		return b.Created.Compare(a.Created)
	})
	cache := chatIndexCache{Version: chatIndexVersion, Rows: rows}
	b, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("failed to encode chat index JSON: %w", err)
	}
	if err := os.WriteFile(chatIndexPath(convDir), b, 0o644); err != nil {
		return fmt.Errorf("failed to write chat index: %w", err)
	}
	return nil
}

func upsertChatIndex(convDir string, chat pub_models.Chat) error {
	rows, err := readChatIndex(convDir)
	if err != nil {
		return fmt.Errorf("failed to read chat index for upsert: %w", err)
	}
	row := chatIndexRowFromChat(chat)
	replaced := false
	for i := range rows {
		if rows[i].ID == chat.ID {
			rows[i] = row
			replaced = true
			break
		}
	}
	if !replaced {
		rows = append(rows, row)
	}
	if err := writeChatIndex(convDir, rows); err != nil {
		return fmt.Errorf("failed to persist chat index: %w", err)
	}
	return nil
}

func NewChatIndexPaginator(convDir string) (*ChatIndexPaginator, error) {
	rows, err := readChatIndex(convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read chat index paginator rows: %w", err)
	}
	return &ChatIndexPaginator{rows: rows}, nil
}
