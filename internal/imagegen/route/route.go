// Package route picks an image generation backend by model name.
package route

import (
	"os"
	"strings"

	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
)

// NewGenerator is the production NewGeneratorFunc.
//  1. It routes cfg.Model by name: "or:" to OpenRouter, an OpenAI image model name
//     (openai.IsImageModel) to OpenAI, and a name holding "gemini" or "nano-banana" to Gemini.
//  2. A model that matches no rule fails with *imagegen.UnknownImageModelError.
//  3. The backend's key variable (OPENAI_API_KEY, OPENROUTER_API_KEY or GEMINI_API_KEY)
//     must be set in the environment. When it is empty, NewGenerator fails with
//     *imagegen.MissingAPIKeyError{Env}.
func NewGenerator(cfg imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
	b, err := backendFor(cfg.Model)
	if err != nil {
		return nil, err
	}
	key := os.Getenv(b.env)
	if key == "" {
		return nil, &imagegen.MissingAPIKeyError{Env: b.env}
	}
	return b.build(cfg, key)
}

// ChatVendor names the vendor of the chat model that generate_image runs beside.
type ChatVendor string

const (
	OpenAI      ChatVendor = "openai"
	Gemini      ChatVendor = "gemini"
	OpenRouter  ChatVendor = "openrouter"
	OtherVendor ChatVendor = ""
)

// ImageModelFor is the image model generate_image uses beside a chat model of
// chatVendor, given the photoConfig model.
//  1. For OpenAI, Gemini and OpenRouter, when photoModel routes to another backend or
//     to none, it returns that vendor's DefaultImageModel.
//  2. Otherwise it returns photoModel.
func ImageModelFor(chatVendor ChatVendor, photoModel string) string {
	var env, fallback string
	switch chatVendor {
	case OpenAI:
		env, fallback = envOpenAI, openai.DefaultImageModel
	case Gemini:
		env, fallback = envGemini, gemini.DefaultImageModel
	case OpenRouter:
		env, fallback = envOpenRouter, "or:"+openrouter.DefaultImageModel
	default:
		return photoModel
	}
	if b, err := backendFor(photoModel); err == nil && b.env == env {
		return photoModel
	}
	return fallback
}

const (
	envOpenAI     = "OPENAI_API_KEY"
	envOpenRouter = "OPENROUTER_API_KEY"
	envGemini     = "GEMINI_API_KEY"
)

type backend struct {
	env   string
	build func(cfg imagegen.GeneratorConfig, apiKey string) (imagegen.ImageGenerator, error)
}

func backendFor(model string) (backend, error) {
	switch {
	case strings.HasPrefix(model, "or:"):
		return backend{env: envOpenRouter, build: func(cfg imagegen.GeneratorConfig, key string) (imagegen.ImageGenerator, error) {
			return openrouter.NewImageGenerator(cfg, key), nil
		}}, nil
	case openai.IsImageModel(model):
		return backend{env: envOpenAI, build: func(cfg imagegen.GeneratorConfig, key string) (imagegen.ImageGenerator, error) {
			g, err := openai.NewImageGenerator(cfg, key)
			if err != nil {
				return nil, err
			}
			return g, nil
		}}, nil
	case strings.Contains(model, "gemini"), strings.Contains(model, "nano-banana"):
		return backend{env: envGemini, build: func(cfg imagegen.GeneratorConfig, key string) (imagegen.ImageGenerator, error) {
			return gemini.NewImageGenerator(cfg, key), nil
		}}, nil
	}
	return backend{}, &imagegen.UnknownImageModelError{Model: model}
}
