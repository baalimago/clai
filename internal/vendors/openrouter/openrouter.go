package openrouter

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	chatPath = "/api/v1/chat/completions"
	ChatURL  = "https://openrouter.ai" + chatPath

	vendorName    = "openrouter"
	lookupTimeout = 30 * time.Second
)

var Default = OpenRouter{
	Model:       "openai/gpt-4.1-mini",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type OpenRouter struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"`
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
	// Modalities reports the model's output modalities; nil reads the catalog.
	Modalities ModalityLookup `json:"-"`

	lookedUp *modalityAnswer
}

type modalityAnswer struct {
	modalities []string
	err        error
}

func (o *OpenRouter) Setup() error {
	err := o.StreamCompleter.Setup("OPENROUTER_API_KEY", ChatURL, "DEBUG_OPENROUTER")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}

	// Vendor decoder for both decode points (non-OK body and error frame at
	// HTTP 200), per worklog 2026-09-05-error-propagation D11/D12.
	o.StreamCompleter.DecodeError = decodeError
	o.StreamCompleter.DecodeChunk = decodeImageChunk

	o.StreamCompleter.Model = o.modelName()
	o.StreamCompleter.FrequencyPenalty = &o.FrequencyPenalty
	o.StreamCompleter.MaxTokens = o.MaxTokens
	o.StreamCompleter.PresencePenalty = &o.PresencePenalty
	o.StreamCompleter.Temperature = &o.Temperature
	o.StreamCompleter.TopP = &o.TopP
	o.StreamCompleter.ExtraHeaders = map[string]string{
		"HTTP-Referer":       "clai",
		"X-OpenRouter-Title": "clai",
	}
	toolChoice := "auto"
	o.ToolChoice = &toolChoice
	o.StreamCompleter.Vendor = vendorName
	o.StreamCompleter.Images = o.ImageCapabilities()
	return nil
}

func (o *OpenRouter) modelName() string {
	return strings.TrimPrefix(o.Model, "or:")
}

func (o *OpenRouter) RegisterTool(tool pub_models.LLMTool) {
	o.InternalRegisterTool(tool)
}

// ImageCapabilities is the openrouter row of Local fact P5-1 (worklog 2026-10-21-image-support).
func (o *OpenRouter) ImageCapabilities() models.ImageCapabilities {
	return models.ImageCapabilities{
		Input:      models.ImageInputInline,
		ToolResult: models.ToolResultNative,
		MIMEs:      []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
	}
}

// StreamCompletions asks image-output models for images on every request. A chat the
// route refuses fails before the modality lookup, so it sends no request (E4, E5).
//
//	O1. Each choices[].delta.images[] data URL is decoded and emitted as one
//	    models.ImageEvent, in stream order, before StopEvent.
//	O2. MIME is asset.Sniff(Data). When it disagrees with the data-URL type, the stream
//	    emits a terminal *asset.MIMEMismatchError and stops reading.
//	O3. Base64 that does not decode emits a terminal
//	    *claierr.UnexpectedProviderResponseError and stops reading.
//	O6. reasoning_details blobs are neither emitted nor retained.
//	O8. When the modality lookup reports "image" for the model, every request carries
//	    "modalities":["image","text"]. When the lookup fails, returns
//	    ErrModelNotInCatalog or reports no image, the request is sent exactly as before
//	    (I10). The lookup runs lazily at the first StreamCompletions.
//	O9. A stream whose request carries the image modalities emits one
//	    models.ImageProgressEvent as its first event. Any other stream emits none.
func (o *OpenRouter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	if err := models.CheckImageParts(o.ImageCapabilities(), vendorName, o.modelName(), chat.Messages); err != nil {
		return nil, err
	}
	o.StreamCompleter.OutputModalities = nil
	if o.producesImages(ctx) == nil {
		o.StreamCompleter.OutputModalities = []string{"image", "text"}
	}
	return o.StreamCompleter.StreamCompletions(ctx, chat)
}

func (o *OpenRouter) producesImages(ctx context.Context) error {
	modalities, err := o.outputModalities(ctx)
	if err != nil {
		return fmt.Errorf("openrouter: look up the output modalities of %q: %w", o.modelName(), err)
	}
	if !slices.Contains(modalities, "image") {
		return &claierr.ImageUnsupportedError{Vendor: vendorName, Model: o.modelName(), Reason: "model has no image output modality"}
	}
	return nil
}

// outputModalities runs the lookup once per completer.
func (o *OpenRouter) outputModalities(ctx context.Context) ([]string, error) {
	if o.lookedUp == nil {
		lookup := o.Modalities
		if lookup == nil {
			lookup = o.catalogLookup()
		}
		ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		modalities, err := lookup(ctx, o.modelName())
		o.lookedUp = &modalityAnswer{modalities: modalities, err: err}
	}
	return o.lookedUp.modalities, o.lookedUp.err
}
