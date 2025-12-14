package text

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal"
	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
)

// FullResponse text querier, as opposed to returning a stream or something
type FullResponse interface {
	Setup(context.Context) error

	// Query the underlying llm with some prompt. Will cancel on context cancel.
	Query(context.Context, models.Chat) (models.Chat, error)
}

type publicQuerier struct {
	conf       text.Configurations
	querier    priv_models.ChatQuerier
	llmTools   []models.LLMTool
	mcpServers []models.McpServer
}

// Option configures a publicQuerier.
type Option func(*publicQuerier)

// WithLLMTools injects concrete LLM tools that will be registered with the
// internal text querier during Setup.
func WithLLMTools(tools ...models.LLMTool) Option {
	return func(pq *publicQuerier) {
		pq.llmTools = append(pq.llmTools, tools...)
	}
}

// WithMcpServers configures MCP servers that will be passed to the internal
// text querier implementation if it supports it.
func WithMcpServers(servers ...models.McpServer) Option {
	return func(pq *publicQuerier) {
		pq.mcpServers = append(pq.mcpServers, servers...)
	}
}

// NewFullResponseQuerier constructs a FullResponse using a default
// configuration plus optional functional options.
//
// Default configuration:
//   - Model:        "gpt-5.2"
//   - SystemPrompt: ""
//   - ConfigDir:    "$HOME/.config/clai"
//   - Use of tools is enabled; internal tools list is empty unless provided
//     via options.
func NewFullResponseQuerier(opts ...Option) FullResponse {
	pq := &publicQuerier{
		conf: defaultInternalConfig(),
	}

	for _, opt := range opts {
		opt(pq)
	}

	return pq
}

// defaultInternalConfig builds a sane default internal text configuration
// for public callers that do not want to provide a custom configuration.
func defaultInternalConfig() text.Configurations {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	cfgDir := path.Join(home, ".config", "clai")

	return text.Configurations{
		Model:               "gpt-5.2",
		SystemPrompt:        "",
		UseTools:            true,
		ConfigDir:           cfgDir,
		TokenWarnLimit:      300000,
		ToolOutputRuneLimit: 30000,
		SaveReplyAsConv:     true,
		Stream:              true,
		UseProfile:          "",
		ProfilePath:         "",
		Tools:               nil,
	}
}

func internalToolsToString(in []models.ToolName) (ret []string) {
	for _, s := range in {
		ret = append(ret, string(s))
	}
	return
}

// pubConfigToInternal is kept for compatibility with existing internal tests
// and code paths; it is no longer used by the exported constructor but may be
// useful for future extension or direct internal usage.
func pubConfigToInternal(c models.Configurations) text.Configurations {
	claiDir := path.Join(c.ConfigDir, "clai")

	return text.Configurations{
		Model:               c.Model,
		SystemPrompt:        c.SystemPrompt,
		UseTools:            true,
		ConfigDir:           claiDir,
		TokenWarnLimit:      300000,
		ToolOutputRuneLimit: 30000,
		SaveReplyAsConv:     true,
		Stream:              true,
		UseProfile:          "",
		ProfilePath:         "",
		Tools:               internalToolsToString(c.InternalTools),
	}
}

// Setup the public querier by creating a config dir + supportive directories, then by initiating
// the querier following the config
func (pq *publicQuerier) Setup(ctx context.Context) error {
	if _, err := os.Stat(pq.conf.ConfigDir); os.IsNotExist(err) {
		os.Mkdir(pq.conf.ConfigDir, 0o755)
	}
	mcpServersDir := path.Join(pq.conf.ConfigDir, "mcpServers")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(mcpServersDir, 0o755)
	}
	conversationsDir := path.Join(pq.conf.ConfigDir, "conversations")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(conversationsDir, 0o755)
	}
	querier, err := internal.CreateTextQuerier(ctx, pq.conf)
	if err != nil {
		return fmt.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %v", err)
	}
	// If the underlying querier knows how to accept injected tools, do so
	// here. This keeps the public API stable while enabling custom tools
	// for advanced users.
	if len(pq.llmTools) > 0 {
		if registrar, ok := querier.(interface{ RegisterLLMTools(...models.LLMTool) }); ok {
			registrar.RegisterLLMTools(pq.llmTools...)
		}
	}

	// Pass any MCP server configuration through, if supported by the
	// internal querier implementation.
	if len(pq.mcpServers) > 0 {
		if mcpCfg, ok := querier.(interface{ RegisterMcpServers([]models.McpServer) }); ok {
			mcpCfg.RegisterMcpServers(pq.mcpServers)
		}
	}

	tq, isChatQuerier := querier.(priv_models.ChatQuerier)
	if !isChatQuerier {
		return fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed", pq.conf.Model)
	}
	pq.querier = tq
	return nil
}

// Query the model with some input chat. Will return a chat containing updated responses. The returning chat may
// append multiple messages to the chat, if the querier is configured to be agentic (use tools)
func (pq *publicQuerier) Query(ctx context.Context, inpChat models.Chat) (models.Chat, error) {
	err := pq.Setup(ctx)
	if err != nil {
		return models.Chat{}, fmt.Errorf("pq.Query failed to Setup clone: %v", err)
	}
	return pq.querier.TextQuery(ctx, inpChat)
}
