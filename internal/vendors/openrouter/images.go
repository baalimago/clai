package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/pkg/claierr"
)

const imagesBaseURL = "https://openrouter.ai"

// ImageGenerator is the OpenRouter /api/v1/images backend.
type ImageGenerator struct {
	model, base, apiKey string
	client              *http.Client
}

func NewImageGenerator(cfg imagegen.GeneratorConfig, apiKey string) *ImageGenerator {
	base := cfg.BaseURL
	if base == "" {
		base = imagesBaseURL
	}
	return &ImageGenerator{model: strings.TrimPrefix(cfg.Model, "or:"), base: base, apiKey: apiKey, client: cfg.Client}
}

type imagesRequest struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	InputReferences []string `json:"input_references,omitempty"`
}

type imagesResponse struct {
	Data []struct {
		B64JSON   string `json:"b64_json"`
		MediaType string `json:"media_type"`
	} `json:"data"`
}

func (g *ImageGenerator) Generate(ctx context.Context, req imagegen.ImageRequest) ([]imagegen.GeneratedImage, error) {
	body := imagesRequest{Model: g.model, Prompt: req.Prompt}
	for _, img := range req.Inputs {
		body.InputReferences = append(body.InputReferences, "data:"+img.MIME+";base64,"+base64.StdEncoding.EncodeToString(img.Data))
	}
	httpReq, err := imagegen.NewJSONRequest(ctx, g.base+"/api/v1/images", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	httpReq.Header.Set("HTTP-Referer", "clai")
	httpReq.Header.Set("X-OpenRouter-Title", "clai")
	resp, err := imagegen.Exchange[imagesResponse](g.client, httpReq, imageFailure)
	if err != nil {
		return nil, fmt.Errorf("openrouter images: %w", err)
	}
	var images []imagegen.GeneratedImage
	for _, d := range resp.Data {
		if d.B64JSON == "" {
			continue
		}
		part, err := generic.GeneratedImage(d.B64JSON, d.MediaType)
		if err != nil {
			return nil, fmt.Errorf("openrouter images: %w", err)
		}
		images = append(images, imagegen.GeneratedImage{Image: asset.Image{Data: part.Data, MIME: part.MIME}})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("openrouter images: %w", imagegen.ErrNoImage)
	}
	return images, nil
}

func imageFailure(status int, body []byte) error {
	return generic.ResponseError(status, body, decodeImageError)
}

// decodeImageError is decodeError plus OpenRouter's moderation refusal, which names
// its reasons in error.metadata.
func decodeImageError(status int, body []byte) error {
	if err := decodeError(status, body); err != nil {
		return err
	}
	var probe struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				Reasons []string `json:"reasons"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || len(probe.Error.Metadata.Reasons) == 0 {
		return nil
	}
	return claierr.NewContentFiltered(&claierr.APIError{
		StatusCode:   status,
		ProviderCode: strings.Join(probe.Error.Metadata.Reasons, ","),
		Message:      probe.Error.Message,
		Body:         string(body),
	})
}
