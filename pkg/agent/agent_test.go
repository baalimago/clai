package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"reflect"
	"strings"
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

func TestAgent_Setup_receives_Out_in_config(t *testing.T) {
	// When WithOutputTo is used, the custom writer must reach
	// the querierCreator via Configurations.Out.
	custom := new(stringsBuilderWriter)
	var capturedOut io.Writer

	a := New(WithOutputTo(custom))
	a.cfgDir = t.TempDir()
	a.querierCreator = func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error) {
		capturedOut = conf.Out
		return &mockChatQuerier{}, nil
	}

	err := a.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if capturedOut != custom {
		t.Errorf("expected Configurations.Out to be the custom writer, got %v", capturedOut)
	}
}

func TestAgent_Setup_receives_stdout_when_no_WithOutputTo(t *testing.T) {
	// The defaultConf sets out: os.Stdout. Verify that when no
	// WithOutputTo is passed, the querierCreator receives os.Stdout
	// (not nil), preserving backward compatibility.
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

	if capturedOut != os.Stdout {
		t.Errorf("expected Configurations.Out to be os.Stdout by default, got %v", capturedOut)
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
	// Verify Out is os.Stdout by default (the Agent's defaultConf sets out: os.Stdout).
	// This preserves backward-compatible behavior: with no WithOutputTo,
	// output goes to stdout.
	if conf.Out != os.Stdout {
		t.Errorf("expected Out to be os.Stdout by default, got %v", conf.Out)
	}
}

func TestAgent_WithOutputTo_propagates(t *testing.T) {
	// Verify that WithOutputTo propagates the custom writer to
	// the internal Configurations so the querier can use it.
	custom := new(stringsBuilderWriter)
	a := New(WithOutputTo(custom))
	conf := a.asInternalConfig()
	if conf.Out != custom {
		t.Errorf("expected Out to be the custom writer, got %v", conf.Out)
	}
}

// stringsBuilderWriter is a minimal io.Writer backed by a strings.Builder for tests.
type stringsBuilderWriter struct{ strings.Builder }

func (w *stringsBuilderWriter) Write(p []byte) (int, error) {
	return w.Builder.Write(p)
}

func (w *stringsBuilderWriter) String() string { return w.Builder.String() }

func TestTypedQuerier_WithOutputTo_suppresses_stdout(t *testing.T) {
	// A TypedQuerier with WithOutputTo(customWriter) must route its
	// agent's output to that writer, never to os.Stdout.
	custom := new(stringsBuilderWriter)
	tq := NewTyped[struct{}](
		WithOutputTo(custom),
	).agent
	conf := tq.asInternalConfig()
	if conf.Out != custom {
		t.Errorf("TypedQuerier did not propagate Out to internal config: got %v", conf.Out)
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
