package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type chatDirInfo struct {
	Scope                string           `json:"scope"`
	ChatID               string           `json:"chat_id"`
	Profile              string           `json:"profile,omitempty"`
	Updated              string           `json:"updated,omitempty"`
	ConversationCreated  string           `json:"conversation_created,omitempty"`
	RepliesByRole        map[string]int   `json:"replies_by_role"`
	InputTokens          int              `json:"input_tokens"`
	NonCachedInputTokens int              `json:"non_cached_input_tokens"`
	CachedTokens         int              `json:"cached_tokens"`
	OutputTokens         int              `json:"output_tokens"`
	CostUSD              float64          `json:"cost_usd,omitempty"`
	Cost                 string           `json:"cost,omitempty"`
	Price                chatDirPriceInfo `json:"price,omitempty"`
	initialMessage       string           `json:"-"`
}

type chatDirPriceInfo struct {
	Input  string `json:"input,omitempty"`
	Cached string `json:"cached,omitempty"`
	Output string `json:"output,omitempty"`
	Total  string `json:"total,omitempty"`
}

func (cdi chatDirInfo) initialPrompt() string {
	truncPrompt, err := utils.WidthAppropriateStringTrunc(cdi.initialMessage, "", 30)
	if err != nil {
		return "failed to get initial prompt"
	}
	return truncPrompt
}

const prettyDirInfoFormat = `scope: %v
chat id: %v
prompt: %v%v
replies by role:
%v
total cost: %v
tokens used:
	input: %v
	cached: %v
	output: %v
price details:
	input: %v
	cached: %v
	output: %v
	total: %v
`

func (cq *ChatHandler) dirInfo() error {
	info, err := cq.resolveChatDirInfo()
	if err != nil {
		return fmt.Errorf("resolve chat dir info: %w", err)
	}

	if cq.raw {
		b, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("marshal chat dir info: %w", err)
		}
		fmt.Fprintln(cq.out, string(b))
		return nil
	}

	profileOut := ""
	if info.Profile != "" {
		profileOut = fmt.Sprintf("\nprofile: %v", info.Profile)
	}
	roles := make([]string, 0, len(info.RepliesByRole))
	for r := range info.RepliesByRole {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	rolesOut := strings.Builder{}
	for i, r := range roles {
		fmt.Fprintf(&rolesOut, "  %s: %v", r, info.RepliesByRole[r])
		if i != len(roles)-1 {
			fmt.Fprintf(&rolesOut, "\n")
		}
	}
	fmt.Fprintf(cq.out, prettyDirInfoFormat,
		info.Scope,
		info.ChatID,
		info.initialPrompt(),
		profileOut,
		rolesOut.String(),
		info.costString(),
		info.InputTokens,
		info.CachedTokens,
		info.OutputTokens,
		info.Price.Input,
		info.Price.Cached,
		info.Price.Output,
		info.priceTotalString(),
	)
	return nil
}

func (cdi chatDirInfo) costString() string {
	if cdi.Cost != "" {
		return cdi.Cost
	}
	return "N/A"
}

func (cdi chatDirInfo) priceTotalString() string {
	if cdi.Price.Total != "" {
		return cdi.Price.Total
	}
	return cdi.costString()
}

func (cq *ChatHandler) resolveChatDirInfo() (chatDirInfo, error) {
	// 1) Dir scope
	ds, err := cq.LoadDirScope("")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return chatDirInfo{}, fmt.Errorf("load dir scope: %w", err)
		}
	} else {
		c, err := FromPath(path.Join(cq.convDir, ds.ChatID+".json"))
		if err == nil {
			info := cq.infoFromChat("dir", ds.ChatID, c)
			info.Updated = ds.Updated
			info.ConversationCreated = c.Created.Format("2006-01-02T15:04:05Z07:00")
			// Error could be that there is no initial user message, which is very weird and
			// wont ever happen ofc ofc
			initialMsg, _ := c.FirstUserMessage()
			info.initialMessage = initialMsg.String()
			return info, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return chatDirInfo{}, fmt.Errorf("load bound dir chat: %w", err)
		}
	}

	// 2) Global scope
	prev, err := FromPath(path.Join(cq.convDir, globalScopeFile))
	if err == nil {
		info := cq.infoFromChat("global", globalScopeChatID, prev)
		info.ConversationCreated = prev.Created.Format("2006-01-02T15:04:05Z07:00")
		initialMsg, _ := prev.FirstUserMessage()
		info.initialMessage = initialMsg.String()
		return info, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return chatDirInfo{}, fmt.Errorf("load globalScope: %w", err)
	}

	return chatDirInfo{}, nil
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

	cdi := chatDirInfo{
		Scope:         scope,
		ChatID:        chatID,
		Profile:       c.Profile,
		RepliesByRole: repliesByRole,
	}

	if c.TokenUsage != nil {
		cdi.InputTokens = c.TokenUsage.PromptTokens
		cdi.CachedTokens = c.TokenUsage.PromptTokensDetails.CachedTokens
		cdi.NonCachedInputTokens = max(c.TokenUsage.PromptTokens-cdi.CachedTokens, 0)
		cdi.OutputTokens = c.TokenUsage.CompletionTokens
	}
	if c.HasCostEstimates() {
		cdi.CostUSD = c.TotalCostUSD()
		cdi.Cost = cost.FormatUSD(c.TotalCostUSD())
		lastQuery := c.Queries[len(c.Queries)-1]
		cachedTokens := lastQuery.Usage.PromptTokensDetails.CachedTokens
		nonCachedTokens := max(lastQuery.Usage.PromptTokens-cachedTokens, 0)
		totalCost := lastQuery.CostUSD
		inputCost := 0.0
		cachedCost := 0.0
		outputCost := 0.0
		if totalTokens := nonCachedTokens + cachedTokens + lastQuery.Usage.CompletionTokens; totalTokens > 0 && totalCost > 0 {
			inputCost = totalCost * float64(nonCachedTokens) / float64(totalTokens)
			cachedCost = totalCost * float64(cachedTokens) / float64(totalTokens)
			outputCost = totalCost * float64(lastQuery.Usage.CompletionTokens) / float64(totalTokens)
		}
		cdi.Price = chatDirPriceInfo{
			Input:  cost.FormatUSD(inputCost),
			Cached: cost.FormatUSD(cachedCost),
			Output: cost.FormatUSD(outputCost),
			Total:  cost.FormatUSD(totalCost),
		}
	}

	return cdi
}
