package openai

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
)

// Proves ImageCapabilities.2.
func TestOpenAICapabilities_Route(t *testing.T) {
	responses := (&ChatGPT{Model: "gpt-4.1-mini"}).ImageCapabilities()
	if responses.ToolResult != models.ToolResultNative || responses.Input != models.ImageInputInline {
		t.Fatalf("Responses route = %+v, want inline and ToolResultNative", responses)
	}
	chat := (&ChatGPT{Model: "gpt-4.1-mini", URL: ChatURL}).ImageCapabilities()
	if chat.ToolResult != models.ToolResultFallback || chat.Input != models.ImageInputInline {
		t.Fatalf("chat-completions route = %+v, want inline and ToolResultFallback", chat)
	}
	if unset := (&ChatGPT{}).ImageCapabilities(); unset.ToolResult != models.ToolResultNative {
		t.Fatalf("the default model on the default URL uses Responses, got %+v", unset)
	}
	if codex := (&ChatGPT{Model: "gpt-5-codex", URL: ChatURL}).ImageCapabilities(); codex.ToolResult != models.ToolResultNative {
		t.Fatalf("codex is Responses-only, got %+v", codex)
	}
}
