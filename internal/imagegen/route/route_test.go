package route_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
)

type refusingTransport struct{ t *testing.T }

func (rt refusingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.t.Errorf("routing made a request to %s", r.URL)
	return nil, errors.New("routing must not make a request")
}

func TestNewGenerator_Routing(t *testing.T) {
	for _, env := range []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(env, testKey)
	}
	client := &http.Client{Transport: refusingTransport{t}}
	build := func(model string) (imagegen.ImageGenerator, error) {
		return route.NewGenerator(imagegen.GeneratorConfig{Model: model, ConfigDir: t.TempDir(), Client: client})
	}
	for model, backend := range map[string]string{
		"gpt-image-2":                        "openai",
		"dall-e-3":                           "openai",
		"chatgpt-image-latest":               "openai",
		"or:google/x":                        "openrouter",
		"or:google/gemini-2.5-flash-image":   "openrouter",
		"nano-banana-pro":                    "gemini",
		openai.DefaultImageModel:             "openai",
		gemini.DefaultImageModel:             "gemini",
		"or:" + openrouter.DefaultImageModel: "openrouter",
	} {
		g, err := build(model)
		if err != nil {
			t.Errorf("%s: %v", model, err)
			continue
		}
		var got string
		switch g.(type) {
		case *openai.ImageGenerator:
			got = "openai"
		case *openrouter.ImageGenerator:
			got = "openrouter"
		case *gemini.ImageGenerator:
			got = "gemini"
		}
		if got != backend {
			t.Errorf("%s routed to %T, want the %s backend", model, g, backend)
		}
	}

	for _, model := range []string{"claude-x", "", "gpt-5.4-mini"} {
		g, err := build(model)
		var unknown *imagegen.UnknownImageModelError
		if !errors.As(err, &unknown) || unknown.Model != model || g != nil {
			t.Errorf("%q: (%v, %v), want *UnknownImageModelError{%q}", model, g, err, model)
		}
	}

	for model, env := range map[string]string{
		"gpt-image-2":            "OPENAI_API_KEY",
		"or:google/x":            "OPENROUTER_API_KEY",
		"gemini-3.1-flash-image": "GEMINI_API_KEY",
	} {
		t.Setenv(env, "")
		g, err := build(model)
		var missing *imagegen.MissingAPIKeyError
		if !errors.As(err, &missing) || missing.Env != env || g != nil {
			t.Errorf("%s without %s: (%v, %v), want *MissingAPIKeyError{%s}", model, env, g, err, env)
		}
	}
}

// Proves ImageModelFor.1–2: the chat vendor's image backend wins unless the photo
// model already routes there; other vendors keep the photo model.
func TestImageModelFor(t *testing.T) {
	for _, tc := range []struct {
		vendor      route.ChatVendor
		photo, want string
	}{
		{route.OpenAI, "gpt-image-1-mini", "gpt-image-1-mini"},
		{route.OpenAI, "or:google/x-image", openai.DefaultImageModel},
		{route.OpenAI, "", openai.DefaultImageModel},
		{route.Gemini, "gemini-2.5-flash-image", "gemini-2.5-flash-image"},
		{route.Gemini, "gpt-image-1", gemini.DefaultImageModel},
		{route.OpenRouter, "or:openai/gpt-5-image-mini", "or:openai/gpt-5-image-mini"},
		{route.OpenRouter, "gpt-image-1", "or:" + openrouter.DefaultImageModel},
		{route.OtherVendor, "gpt-image-1", "gpt-image-1"},
		{route.OtherVendor, "or:google/x-image", "or:google/x-image"},
		{route.OtherVendor, "", ""},
	} {
		if got := route.ImageModelFor(tc.vendor, tc.photo); got != tc.want {
			t.Errorf("ImageModelFor(%q, %q) = %q, want %q", tc.vendor, tc.photo, got, tc.want)
		}
	}
}
