package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const chatIndexFileName = "chat_index.cache"

type chatIndexRow struct {
	ID               string    `json:"id"`
	Created          time.Time `json:"created"`
	Profile          string    `json:"profile,omitempty"`
	MessageCount     int       `json:"message_count"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	TotalCostUSD     float64   `json:"total_cost_usd,omitempty"`
	FirstUserMessage string    `json:"first_user_message,omitempty"`
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

func chatIndexRowFromChat(chat pub_models.Chat) chatIndexRow {
	row := chatIndexRow{
		ID:           chat.ID,
		Created:      chat.Created,
		Profile:      chat.Profile,
		MessageCount: len(chat.Messages),
		TotalCostUSD: chat.TotalCostUSD(),
	}
	if chat.TokenUsage != nil {
		row.TotalTokens = chat.TokenUsage.TotalTokens
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
			rows, rebuildErr := rebuildChatIndex(convDir)
			if rebuildErr != nil {
				return nil, fmt.Errorf("failed to rebuild missing chat index: %w", rebuildErr)
			}
			return rows, nil
		}
		return nil, fmt.Errorf("failed to read chat index: %w", err)
	}
	var rows []chatIndexRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("failed to decode chat index JSON: %w", err)
	}
	slices.SortFunc(rows, func(a, b chatIndexRow) int {
		return b.Created.Compare(a.Created)
	})
	return rows, nil
}

func rebuildChatIndex(convDir string) ([]chatIndexRow, error) {
	files, err := os.ReadDir(convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations for index rebuild: %w", err)
	}
	rows := make([]chatIndexRow, 0, len(files))
	for _, file := range files {
		if file.IsDir() || file.Name() == chatIndexFileName {
			continue
		}
		chatPath := path.Join(convDir, file.Name())
		chat, err := FromPath(chatPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load chat %q while rebuilding index: %w", chatPath, err)
		}
		rows = append(rows, chatIndexRowFromChat(chat))
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
	b, err := json.Marshal(rows)
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
