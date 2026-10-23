package photo

import (
	"testing"

	"github.com/baalimago/clai/internal/vendors/openai"
)

func TestValidateOutputType(t *testing.T) {
	valid := []OutputType{LOCAL, URL, UNSET}
	for _, v := range valid {
		if err := ValidateOutputType(v); err != nil {
			t.Errorf("expected no error for %v", v)
		}
	}
	if err := ValidateOutputType(OutputType("bad")); err == nil {
		t.Error("expected error for invalid output type")
	}
}

// The default photo model is the OpenAI backend's default image model, one source.
func TestDefaultModelIsOpenAIDefault(t *testing.T) {
	if DEFAULT.Model != openai.DefaultImageModel || !openai.IsImageModel(DEFAULT.Model) {
		t.Fatalf("DEFAULT.Model = %q, want openai.DefaultImageModel %q", DEFAULT.Model, openai.DefaultImageModel)
	}
}
