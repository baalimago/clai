package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/baalimago/clai/internal"
	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
)

type Agent struct {
	name         string
	model        string
	prompt       string
	tools        []models.LLMTool
	mcpServers   []models.McpServer
	cfgDir       string
	maxToolCalls *int

	querierCreator func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error)

	out io.Writer

	querier priv_models.ChatQuerier
}

var defaultConf = Agent{
	model:          "gpt-5.2",
	prompt:         "Uh-oh. Something is not quite right. Please ask the user to overlook his agentic setup, and to update the prompt.",
	tools:          make([]models.LLMTool, 0),
	mcpServers:     make([]models.McpServer, 0),
	querierCreator: internal.CreateTextQuerier,
	out:            os.Stdout,
}

type Option func(*Agent)

func New(options ...Option) Agent {
	conf := defaultConf
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	conf.cfgDir = path.Join(home, ".config", "clai")

	for _, o := range options {
		o(&conf)
	}
	return conf
}

func WithConfigDir(cfgDir string) Option {
	return func(a *Agent) {
		if !strings.HasSuffix(cfgDir, "clai") {
			cfgDir = path.Join(cfgDir, "clai")
		}
		a.cfgDir = cfgDir
	}
}

func WithMaxToolCalls(am int) Option {
	return func(a *Agent) {
		a.maxToolCalls = &am
	}
}

func WithModel(model string) Option {
	return func(a *Agent) {
		a.model = model
	}
}

func WithPrompt(prompt string) Option {
	return func(a *Agent) {
		a.prompt = prompt
	}
}

func WithTools(tools []models.LLMTool) Option {
	return func(a *Agent) {
		a.tools = tools
	}
}

func WithMcpServers(mcpServers []models.McpServer) Option {
	return func(a *Agent) {
		a.mcpServers = mcpServers
	}
}

func WithOutputTo(out io.Writer) Option {
	return func(a *Agent) {
		a.out = out
	}
}

func (a *Agent) asInternalConfig() text.Configurations {
	return text.Configurations{
		Model:           a.model,
		SystemPrompt:    a.prompt,
		ConfigDir:       a.cfgDir,
		UseTools:        true,
		SaveReplyAsConv: true,
		McpServers:      a.mcpServers,
		Tools:           a.tools,
		MaxToolCalls:    a.maxToolCalls,
	}
}

func (a *Agent) Setup(ctx context.Context) error {
	if _, err := os.Stat(a.cfgDir); os.IsNotExist(err) {
		os.Mkdir(a.cfgDir, 0o755)
	}
	mcpServersDir := path.Join(a.cfgDir, "mcpServers")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(mcpServersDir, 0o755)
	}
	conversationsDir := path.Join(a.cfgDir, "conversations")
	if _, err := os.Stat(conversationsDir); os.IsNotExist(err) {
		os.Mkdir(conversationsDir, 0o755)
	}
	querier, err := a.querierCreator(ctx, a.asInternalConfig())
	if err != nil {
		return fmt.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %v", err)
	}
	tq, isChatQuerier := querier.(priv_models.ChatQuerier)
	if !isChatQuerier {
		return fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed", a.model)
	}
	a.querier = tq
	return nil
}
