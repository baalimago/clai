// Package imagegen turns a prompt, and optional input images, into image bytes. It holds
// the types and the interface; the backends live in their vendor packages and the router
// in imagegen/route.
package imagegen

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/pkg/claierr"
)

type ImageRequest struct {
	Prompt string
	Inputs []asset.Image // edit sources and references; empty generates from text alone
	Size   string        // "" or one of the tools parameter "Image sizes a caller may request"
}

type GeneratedImage struct {
	Image         asset.Image // MIME is asset.Sniff(Data)
	RevisedPrompt string
}

type ImageGenerator interface {
	// Generate returns at least one image, or an error.
	//  1. Every image has MIME == asset.Sniff(Data). When the response's declared type
	//     disagrees, Generate fails with *asset.MIMEMismatchError.
	//  2. A response with no image fails with ErrNoImage.
	//  3. HTTP failures are decoded by the vendor's existing decoder into the claierr
	//     vocabulary: auth, credits, rate limit, content filtered, model not found.
	//  4. Cancelling ctx aborts the request and returns ctx.Err(), wrapped.
	//  5. Generate has no side effects beyond the HTTP request: no stdout, no files,
	//     no animation.
	//  6. Input bytes are base64-encoded only into the request body, and output base64
	//     is decoded only from the response body (I5).
	// OpenAI (the README parameter "OpenAI image model names"):
	//  O1. With no Inputs: POST <base>/v1/images/generations, decoding data[0].b64_json.
	//  O2. With Inputs: multipart POST <base>/v1/images/edits with one image[] per input.
	//  O3. Defaults for quality, size and n come from <ConfigDir>/openai_dalle_<model>.json,
	//      as today. Request.Size overrides size.
	// OpenRouter ("or:" prefix, stripped on the wire):
	//  R1. POST <base>/api/v1/images {model, prompt, input_references: [data URLs]}. It
	//      decodes data[].b64_json and checks data[].media_type against the sniff (1).
	// Gemini (contains "gemini" or "nano-banana"):
	//  G1. POST <base>/v1beta/models/<model>:generateContent with the prompt and one
	//      inlineData part per input. It decodes the candidate's first inlineData part
	//      and checks its mimeType against the sniff (1).
	Generate(ctx context.Context, req ImageRequest) ([]GeneratedImage, error)
}

type GeneratorConfig struct {
	Model     string
	ConfigDir string       // for per-model defaults (O3)
	BaseURL   string       // "" means the vendor's production URL
	Client    *http.Client // nil means http.DefaultClient
}

// NewGeneratorFunc builds a generator. It is the only way any phase obtains one, and
// the composition root injects it (I9).
type NewGeneratorFunc func(cfg GeneratorConfig) (ImageGenerator, error)

var ErrNoImage = errors.New("image generator returned no image")

type UnknownImageModelError struct{ Model string }

func (e *UnknownImageModelError) Error() string {
	return fmt.Sprintf("no image generator for model %q: expected gpt-image-*, dall-e-*, chatgpt-image-*, or:<model>, or a model containing gemini or nano-banana", e.Model)
}

// Unwrap links UnknownImageModelError to ErrModelNotFound so a model fallback
// notice prints the one-phrase meaning while the final failure keeps the full
// routing hint above (architecture/model-aliases.md).
func (e *UnknownImageModelError) Unwrap() error { return claierr.ErrModelNotFound }

type MissingAPIKeyError struct{ Env string }

func (e *MissingAPIKeyError) Error() string {
	return fmt.Sprintf("image generation needs the environment variable %s", e.Env)
}
