package openai

import "testing"

func TestIsOpenAIImageModel(t *testing.T) {
	for model, want := range map[string]bool{
		"gpt-image-1": true, "gpt-image-1-mini": true, "dall-e-3": true, "chatgpt-image-latest": true,
		"gpt-4.1-mini": false, "or:openai/gpt-image-1": false, "": false, "GPT-IMAGE-1": false,
	} {
		if got := isOpenAIImageModel(model); got != want {
			t.Errorf("isOpenAIImageModel(%q) = %v, want %v", model, got, want)
		}
	}
}
