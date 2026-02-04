package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type chatDirInfo struct {
	Scope               string         `json:"scope"`
	ChatID              string         `json:"chat_id"`
	Profile             string         `json:"profile,omitempty"`
	Updated             string         `json:"updated,omitempty"`
	ConversationCreated string         `json:"conversation_created,omitempty"`
	RepliesByRole       map[string]int `json:"replies_by_role"`
	TokensTotal         int            `json:"tokens_total"`
}

func (cq *ChatHandler) dirInfo() error {
	info, ok, err := cq.resolveChatDirInfo()
	if err != nil {
		return fmt.Errorf("resolve chat dir info: %w", err)
	}
	if !ok {
		if cq.raw {
			_, _ = fmt.Fprintln(cq.out, "{}")
			return nil
		}
		_, _ = fmt.Fprintln(cq.out, "no dir-scoped chat and no global chat")
		return nil
	}

	if cq.raw {
		b, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("marshal chat dir info: %w", err)
		}
		_, _ = fmt.Fprintln(cq.out, string(b))
		return nil
	}

	_, _ = fmt.Fprintf(cq.out, "scope: %s\n", info.Scope)
	_, _ = fmt.Fprintf(cq.out, "chat_id: %s\n", info.ChatID)
	if info.Profile != "" {
		_, _ = fmt.Fprintf(cq.out, "profile: %s\n", info.Profile)
	}
	_, _ = fmt.Fprintln(cq.out, "replies_by_role:")
	roles := make([]string, 0, len(info.RepliesByRole))
	for r := range info.RepliesByRole {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		_, _ = fmt.Fprintf(cq.out, "  %s: %d\n", r, info.RepliesByRole[r])
	}
	_, _ = fmt.Fprintf(cq.out, "tokens_total: %d\n", info.TokensTotal)
	return nil
}

func (cq *ChatHandler) resolveChatDirInfo() (chatDirInfo, bool, error) {
	// 1) Dir scope
	ds, ok, err := cq.LoadDirScope("")
	if err != nil {
		return chatDirInfo{}, false, fmt.Errorf("load dir scope: %w", err)
	}
	if ok {
		c, err := FromPath(path.Join(cq.convDir, ds.ChatID+".json"))
		if err == nil {
			info := cq.infoFromChat("dir", ds.ChatID, c)
			info.Updated = ds.Updated
			info.ConversationCreated = c.Created.Format("2006-01-02T15:04:05Z07:00")
			return info, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return chatDirInfo{}, false, fmt.Errorf("load bound dir chat: %w", err)
		}
	}

	// 2) Global prevQuery
	prev, err := FromPath(path.Join(cq.convDir, "prevQuery.json"))
	if err == nil {
		info := cq.infoFromChat("global", "prevQuery", prev)
		info.ConversationCreated = prev.Created.Format("2006-01-02T15:04:05Z07:00")
		return info, true, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return chatDirInfo{}, false, fmt.Errorf("load prevQuery: %w", err)
	}

	return chatDirInfo{}, false, nil
}

func (cq *ChatHandler) infoFromChat(scope, chatID string, c pub_models.Chat) chatDirInfo {
	repliesByRole := map[string]int{}
	for _, m := range c.Messages {
		if strings.TrimSpace(m.Content) == "" {
			// avoids counting marker messages (used by dir-reply bridge)
			continue
		}
		repliesByRole[m.Role]++
	}

	tokensTotal := 0
	// best-effort: sum known fields if present
	if c.TokenUsage != nil {
		tokensTotal += c.TokenUsage.TotalTokens
		// Some vendors only populate prompt+completion.
		tokensTotal += c.TokenUsage.PromptTokens + c.TokenUsage.CompletionTokens
	}

	return chatDirInfo{
		Scope:         scope,
		ChatID:        chatID,
		Profile:       c.Profile,
		RepliesByRole: repliesByRole,
		TokensTotal:   tokensTotal,
	}
}
