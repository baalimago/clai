package summary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const purposeSummary = "summary"

type agentSummarizer struct {
	confDir      string
	configModel  string
	summaryModel string
	newQuerier   func(context.Context, text.Configurations) (models.ChatQuerier, error)
}

func createChatQuerier(ctx context.Context, conf text.Configurations) (models.ChatQuerier, error) {
	q, err := text.CreateQuerier(ctx, conf)
	if err != nil {
		return nil, err
	}
	return asChatQuerier(q)
}

func asChatQuerier(q models.Querier) (models.ChatQuerier, error) {
	cq, ok := q.(models.ChatQuerier)
	if !ok {
		return nil, fmt.Errorf("%T is not a ChatQuerier", q)
	}
	return cq, nil
}

// NewAgentSummarizer loads textConfig.json once for the model ladder and
// returns the querier-backed Summarizer.
func NewAgentSummarizer(confDir string) (models.Summarizer, error) {
	if err := allowed(); err != nil {
		return nil, err
	}
	conf, added, err := utils.LoadConfigFromFileCollect(confDir, "textConfig.json", text.MigrateOldChatConfig, &text.Default)
	if err != nil {
		return nil, fmt.Errorf("load text config for summarizer: %w", err)
	}
	// The batch path makes this the first load in the process, so an
	// upgrade rewrite would otherwise go unannounced (R2-04).
	if len(added) > 0 {
		ancli.PrintOK(utils.ConfigUpgradeMessage("textConfig.json", added) + "\n")
	}
	return &agentSummarizer{
		confDir:      confDir,
		configModel:  conf.Model,
		summaryModel: conf.SummaryModel,
		newQuerier:   createChatQuerier,
	}, nil
}

func (a *agentSummarizer) Summarize(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
	model, err := resolveModel(req.Model, a.summaryModel, req.Chat, a.configModel)
	if err != nil {
		return models.Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return models.Summary{}, fmt.Errorf("summarize: %w", err)
	}
	// The runner cancels whatever sits under ContextCancelKey on StopEvent;
	// owning that key on a child keeps the caller's context untouched (D27).
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runCtx = context.WithValue(runCtx, utils.ContextCancelKey, cancel)

	holder := &submission{}
	usage := &usageRecorder{}
	maxCalls := MaxToolCalls
	conf := text.Configurations{
		Model:                 model,
		SystemPrompt:          systemPrompt,
		UseTools:              true,
		Tools:                 []pub_models.LLMTool{newSubmitSummaryTool(holder)},
		SkipAmbientMcpServers: true,
		SaveReplyAsConv:       false,
		MaxToolCalls:          &maxCalls,
		Raw:                   true,
		Out:                   io.Discard,
		ErrOut:                io.Discard,
		ConfigDir:             a.confDir,
		AgentSettings:         &text.AgentSettings{UsageRecorder: usage},
		CostWarnf:             traceCostWarnf,
	}
	cq, err := a.newQuerier(runCtx, conf)
	if err != nil {
		return models.Summary{}, fmt.Errorf("summarizer querier for model %q: %w", model, err)
	}
	chat, err := cq.TextQuery(runCtx, pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: renderTranscript(req.Chat)}}})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return models.Summary{}, fmt.Errorf("summarize: %w", ctxErr)
	}
	title, summary, ok := holder.get()
	if !ok {
		if err != nil {
			return models.Summary{}, fmt.Errorf("summarize with %q: %w", model, err)
		}
		return models.Summary{}, errors.New("no summary submitted")
	}
	if err != nil {
		tracef("summarize with %q: keeping the held submission over the querier error: %v", model, err)
	}
	return models.Summary{
		Title:       title,
		Summary:     summary,
		Model:       model,
		Queries:     summaryQueries(chat.Queries, model, usage.total()),
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// traceCostWarnf keeps the summarizer querier's cost warnings off the
// process streams unless DEBUG_SUMMARY is on (R2-03).
func traceCostWarnf(format string, a ...any) {
	tracef("cost: "+format, a...)
}

// tracef writes to stderr so a trace never lands in a piped -r answer (R3-17).
func tracef(format string, a ...any) {
	if !debugflags.Enabled("SUMMARY") {
		return
	}
	fmt.Fprintf(os.Stderr, "[DEBUG_SUMMARY] "+format+"\n", a...)
}

// resolveModel applies the D22 ladder: explicit → config summary-model →
// the conversation's last recorded model → text config model.
func resolveModel(explicit, summaryModel string, chat pub_models.Chat, configModel string) (string, error) {
	for _, rung := range []string{explicit, summaryModel, lastRecordedModel(chat), configModel} {
		if m := strings.TrimSpace(rung); m != "" {
			return m, nil
		}
	}
	return "", errors.New("summarize: no model on any rung (flag, summary-model, conversation history, model)")
}

func lastRecordedModel(chat pub_models.Chat) string {
	for i := len(chat.Queries) - 1; i >= 0; i-- {
		if chat.Queries[i].Model == "" || chat.Queries[i].Purpose != "" {
			continue
		}
		return chat.Queries[i].Model
	}
	return ""
}

func summaryQueries(enriched []pub_models.QueryCost, model string, recorded pub_models.Usage) []pub_models.QueryCost {
	if len(enriched) == 0 {
		return []pub_models.QueryCost{{CreatedAt: time.Now(), Model: model, Usage: recorded, Purpose: purposeSummary}}
	}
	rows := make([]pub_models.QueryCost, len(enriched))
	copy(rows, enriched)
	for i := range rows {
		rows[i].Purpose = purposeSummary
	}
	return rows
}

type usageRecorder struct {
	mu    sync.Mutex
	calls []pub_models.CompletedModelCall
}

func (r *usageRecorder) Record(_ context.Context, call pub_models.CompletedModelCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	return nil
}

func (r *usageRecorder) total() pub_models.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sum pub_models.Usage
	for _, call := range r.calls {
		if call.Usage == nil {
			continue
		}
		sum.PromptTokens += call.Usage.PromptTokens
		sum.CompletionTokens += call.Usage.CompletionTokens
		sum.TotalTokens += call.Usage.TotalTokens
	}
	return sum
}
