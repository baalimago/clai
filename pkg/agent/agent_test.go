package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path"
	"reflect"
	"testing"
	"time"

	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
)

type mockTool struct{}

func (m *mockTool) Call(i models.Input) (string, error) { return "", nil }
func (m *mockTool) Specification() models.Specification {
	return models.Specification{Name: "mockTool"}
}

type mockChatQuerier struct {
	priv_models.ChatQuerier
	textQueryCalled bool
	lastChat        models.Chat
}

func (m *mockChatQuerier) Query(ctx context.Context) error { return nil }
func (m *mockChatQuerier) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	m.textQueryCalled = true
	m.lastChat = chat
	return chat, nil
}

func TestNew(t *testing.T) {
	t.Run("it should create an agent with default values", func(t *testing.T) {
		a := New()
		if a.model != "gpt-5.2" {
			t.Errorf("expected default model to be gpt-5.2, got %v", a.model)
		}
	})

	t.Run("it should apply options", func(t *testing.T) {
		model := "test-model"
		prompt := "test-prompt"
		tools := []models.LLMTool{&mockTool{}}
		mcpServers := []models.McpServer{{Name: "test-mcp"}}
		toolGlobs := []string{"mcp_test_*", "cat"}

		a := New(
			WithModel(model),
			WithPrompt(prompt),
			WithTools(tools),
			WithMcpServers(mcpServers),
			WithToolGlobs(toolGlobs...),
		)

		if a.model != model {
			t.Errorf("expected model %v, got %v", model, a.model)
		}
		if a.prompt != prompt {
			t.Errorf("expected prompt %v, got %v", prompt, a.prompt)
		}
		if !reflect.DeepEqual(a.tools, tools) {
			t.Errorf("expected tools %v, got %v", tools, a.tools)
		}
		if !reflect.DeepEqual(a.mcpServers, mcpServers) {
			t.Errorf("expected mcpServers %v, got %v", mcpServers, a.mcpServers)
		}
		if !reflect.DeepEqual(a.toolGlobs, toolGlobs) {
			t.Errorf("expected toolGlobs %v, got %v", toolGlobs, a.toolGlobs)
		}
	})

	t.Run("it should NOT persist options across calls", func(t *testing.T) {
		_ = New(WithModel("changed"))
		a := New()
		if a.model == "changed" {
			t.Errorf("global state was mutated, model is still 'changed'")
		}
	})

	t.Run("it should default the slog channel", func(t *testing.T) {
		// The slog channel is disabled by default (nil logger); the level
		// and rune cap ship enabled so a caller attaching only a logger gets
		// Debug-level, 200-rune records (worklog 2026-08-15-agent-slog-output, D2, D3).
		a := New()
		if a.logger != nil {
			t.Errorf("expected nil logger by default, got %v", a.logger)
		}
		if a.slogLevel != slog.LevelDebug {
			t.Errorf("expected default slog level Debug, got %v", a.slogLevel)
		}
		if a.slogRuneLimit != 200 {
			t.Errorf("expected default slog rune limit 200, got %d", a.slogRuneLimit)
		}
	})

	t.Run("it should apply the slog options", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		a := New(
			WithLogger(logger),
			WithSlogLevel(slog.LevelWarn),
			WithSlogRuneLimit(5),
		)
		if a.logger != logger {
			t.Errorf("expected logger to be applied, got %v", a.logger)
		}
		if a.slogLevel != slog.LevelWarn {
			t.Errorf("expected slog level Warn, got %v", a.slogLevel)
		}
		if a.slogRuneLimit != 5 {
			t.Errorf("expected slog rune limit 5, got %d", a.slogRuneLimit)
		}
	})
}

func TestAgent_Setup(t *testing.T) {
	t.Run("it should successfully setup the agent", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "clai-test-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		mockQuerier := &mockChatQuerier{}

		a := New()
		a.cfgDir = tmpDir
		a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
			return mockQuerier, nil
		}

		ctx := context.Background()
		err = a.Setup(ctx)
		if err != nil {
			t.Fatalf("Setup failed: %v", err)
		}

		if a.querier != mockQuerier {
			t.Errorf("expected querier to be set")
		}

		// Check if directories were created
		dirs := []string{
			tmpDir,
			path.Join(tmpDir, "mcpServers"),
			path.Join(tmpDir, "conversations"),
		}
		for _, d := range dirs {
			if _, err := os.Stat(d); os.IsNotExist(err) {
				t.Errorf("expected directory %v to exist", d)
			}
		}
	})

	t.Run("it should return error if querierCreator fails", func(t *testing.T) {
		a := New()
		a.cfgDir = t.TempDir()
		expectedErr := errors.New("creation failed")
		a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
			return nil, expectedErr
		}

		err := a.Setup(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !reflect.DeepEqual(err.Error(), "publicQuerier.Setup failed to CreateTextQuerier: creation failed") {
			t.Errorf("expected error message to contain %v, got %v", expectedErr, err)
		}
	})

	t.Run("it should return error if querier is not a ChatQuerier", func(t *testing.T) {
		a := New()
		a.cfgDir = t.TempDir()
		// Returning a mock that only implements Querier but NOT ChatQuerier
		type simpleQuerier struct{ priv_models.Querier }
		a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
			return &simpleQuerier{}, nil
		}

		err := a.Setup(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestAgent_Setup_receives_io_Discard(t *testing.T) {
	// Library mode is silent on stdout (worklog 2026-08-15-agent-slog-output, D4): regardless of options, the
	// querierCreator must receive Configurations.Out == io.Discard so
	// embedded use never writes raw terminal output.
	var capturedOut io.Writer

	a := New()
	a.cfgDir = t.TempDir()
	a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
		capturedOut = conf.Out
		return &mockChatQuerier{}, nil
	}

	err := a.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if capturedOut != io.Discard {
		t.Errorf("expected Configurations.Out to be io.Discard, got %v", capturedOut)
	}
}

func TestAgent_asInternalConfig(t *testing.T) {
	tools := []models.LLMTool{&mockTool{}}
	mcpServers := []models.McpServer{{Name: "test-mcp"}}
	toolGlobs := []string{"mcp_*", "cat"}
	a := New(
		WithModel("test-model"),
		WithPrompt("test-prompt"),
		WithTools(tools),
		WithMcpServers(mcpServers),
		WithToolGlobs(toolGlobs...),
	)
	a.cfgDir = "/tmp/test"

	conf := a.asInternalConfig()

	if conf.Model != "test-model" {
		t.Errorf("expected model test-model, got %v", conf.Model)
	}
	if conf.SystemPrompt != "test-prompt" {
		t.Errorf("expected prompt test-prompt, got %v", conf.SystemPrompt)
	}
	if conf.ConfigDir != "/tmp/test" {
		t.Errorf("expected configDir /tmp/test, got %v", conf.ConfigDir)
	}
	if !reflect.DeepEqual(conf.Tools, tools) {
		t.Errorf("expected tools %v, got %v", tools, conf.Tools)
	}
	if !reflect.DeepEqual(conf.McpServers, mcpServers) {
		t.Errorf("expected mcpServers %v, got %v", mcpServers, conf.McpServers)
	}
	if !reflect.DeepEqual(conf.RequestedToolGlobs, toolGlobs) {
		t.Errorf("expected RequestedToolGlobs %v, got %v", toolGlobs, conf.RequestedToolGlobs)
	}
	if !conf.UseTools {
		t.Error("expected UseTools to be true")
	}
	if !conf.SaveReplyAsConv {
		t.Error("expected SaveReplyAsConv to be true")
	}
	// Verify Out is io.Discard: library mode is silent on stdout and the
	// slog logger is the sole embedded output channel (worklog 2026-08-15-agent-slog-output, D4).
	if conf.Out != io.Discard {
		t.Errorf("expected Out to be io.Discard, got %v", conf.Out)
	}
}

func TestAgent_WithLogger_propagates(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(WithLogger(logger))
	conf := a.asInternalConfig()
	if conf.AgentSettings == nil {
		t.Fatal("expected AgentSettings in internal config")
	}
	if conf.AgentSettings.Logger != logger {
		t.Errorf("expected AgentSettings.Logger to be the attached logger, got %v", conf.AgentSettings.Logger)
	}
}

func TestAgent_WithSlogLevel_propagates(t *testing.T) {
	a := New(WithSlogLevel(slog.LevelWarn))
	conf := a.asInternalConfig()
	if conf.AgentSettings == nil {
		t.Fatal("expected AgentSettings in internal config")
	}
	if conf.AgentSettings.Level != slog.LevelWarn {
		t.Errorf("expected AgentSettings.Level Warn, got %v", conf.AgentSettings.Level)
	}
}

func TestAgent_WithSlogRuneLimit_propagates(t *testing.T) {
	a := New(WithSlogRuneLimit(42))
	conf := a.asInternalConfig()
	if conf.AgentSettings == nil {
		t.Fatal("expected AgentSettings in internal config")
	}
	if conf.AgentSettings.RuneLimit != 42 {
		t.Errorf("expected AgentSettings.RuneLimit 42, got %d", conf.AgentSettings.RuneLimit)
	}
}

func TestAgent_WithLogger_nil_disables(t *testing.T) {
	// WithLogger(nil) must leave the channel disabled: the grouped pointer
	// still exists, but Logger stays nil (the error coverage contract).
	a := New(WithLogger(nil))
	conf := a.asInternalConfig()
	if conf.AgentSettings == nil {
		t.Fatal("expected AgentSettings in internal config")
	}
	if conf.AgentSettings.Logger != nil {
		t.Errorf("expected nil AgentSettings.Logger, got %v", conf.AgentSettings.Logger)
	}
}

func TestAgent_WithSlogRuneLimit_nonpositive_carried(t *testing.T) {
	// WithSlogRuneLimit(0) and negative values are carried verbatim;
	// worklog 2026-08-15-agent-slog-output D5 makes truncation treat <= 0 as
	// no cap (the querier side, Phase 2).
	for _, n := range []int{0, -7} {
		a := New(WithSlogRuneLimit(n))
		conf := a.asInternalConfig()
		if conf.AgentSettings == nil {
			t.Fatal("expected AgentSettings in internal config")
		}
		if conf.AgentSettings.RuneLimit != n {
			t.Errorf("expected AgentSettings.RuneLimit %d, got %d", n, conf.AgentSettings.RuneLimit)
		}
	}
}

func TestAgent_WithStoploss(t *testing.T) {
	a := New(WithStoploss(Stoploss{MaxTokens: 50, MaxTokensHandoverMsg: "m", MaxToolCallsAfterHandover: 3}))
	conf := a.asInternalConfig()
	if conf.Stoploss == nil {
		t.Fatal("expected Stoploss in internal config")
	}
	if conf.Stoploss.MaxTokens != 50 {
		t.Fatalf("expected MaxTokens 50, got %d", conf.Stoploss.MaxTokens)
	}
	if conf.Stoploss.MaxTokensHandoverMsg != "m" {
		t.Fatalf("expected handover message 'm', got %q", conf.Stoploss.MaxTokensHandoverMsg)
	}
	if conf.Stoploss.MaxToolCallsAfterHandover != 3 {
		t.Fatalf("expected MaxToolCallsAfterHandover 3, got %d", conf.Stoploss.MaxToolCallsAfterHandover)
	}
}

func TestAgent_WithStoploss_ZeroValueDisabled(t *testing.T) {
	// A zero-value Stoploss must not create a non-nil internal pointer: the
	// agent default stays unlimited.
	a := New(WithStoploss(Stoploss{}))
	if a.stoploss != (Stoploss{}) {
		t.Fatalf("expected zero-value stoploss stored, got %+v", a.stoploss)
	}
	if conf := a.asInternalConfig(); conf.Stoploss != nil {
		t.Fatalf("expected nil internal Stoploss for zero-value option, got %+v", conf.Stoploss)
	}
}

func TestAgent_WithMaxToolCalls_Zero(t *testing.T) {
	a := New(WithMaxToolCalls(0))
	conf := a.asInternalConfig()
	if conf.MaxToolCalls == nil {
		t.Fatal("expected non-nil MaxToolCalls")
	}
	if *conf.MaxToolCalls != 0 {
		t.Fatalf("expected 0, got %d", *conf.MaxToolCalls)
	}
}

func TestAgent_WithCmdBanList(t *testing.T) {
	a := New(WithCmdBanList("rm", "sudo"))
	if !reflect.DeepEqual(a.cmdBan, []string{"rm", "sudo"}) {
		t.Fatalf("expected cmdBan [rm sudo], got %v", a.cmdBan)
	}

	// The default must stay nil so an agent without the option is permissive.
	plain := New()
	if plain.cmdBan != nil {
		t.Fatalf("expected nil cmdBan by default, got %v", plain.cmdBan)
	}
}

func TestAgent_WithCmdBanList_PropagatesToInternalConfig(t *testing.T) {
	a := New(WithCmdBanList("rm", "sudo"))
	conf := a.asInternalConfig()
	if !reflect.DeepEqual(conf.CmdBan, []string{"rm", "sudo"}) {
		t.Fatalf("expected conf.CmdBan [rm sudo], got %v", conf.CmdBan)
	}

	plain := New()
	if plain.asInternalConfig().CmdBan != nil {
		t.Fatalf("expected nil CmdBan in internal config by default")
	}
}

func TestAgent_Run(t *testing.T) {
	mockQuerier := &mockChatQuerier{}
	a := &Agent{
		name:    "test-agent",
		prompt:  "test-prompt",
		querier: mockQuerier,
	}
	a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
		return mockQuerier, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	t.Logf("is this nil: %v", a)
	a.Run(ctx)

	if !mockQuerier.textQueryCalled {
		t.Error("expected TextQuery to be called")
	}

	if mockQuerier.lastChat.Messages[0].Content != "test-prompt" {
		t.Errorf("expected prompt in message, got %v", mockQuerier.lastChat.Messages[0].Content)
	}
}
