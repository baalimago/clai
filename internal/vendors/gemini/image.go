package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/pkg/claierr"
)

const imagesBaseURL = "https://generativelanguage.googleapis.com"

// DefaultImageModel is the image model generate_image uses beside a Gemini chat model
// when the photo model is not a Gemini one.
const DefaultImageModel = "gemini-3.1-flash-image"

// ImageGenerator is the Gemini generateContent image backend.
type ImageGenerator struct {
	model, base, apiKey string
	client              *http.Client
}

func NewImageGenerator(cfg imagegen.GeneratorConfig, apiKey string) *ImageGenerator {
	base := cfg.BaseURL
	if base == "" {
		base = imagesBaseURL
	}
	return &ImageGenerator{model: cfg.Model, base: base, apiKey: apiKey, client: cfg.Client}
}

type imageContent struct {
	Parts []imagePart `json:"parts"`
}

type imagePart struct {
	Text       string       `json:"text,omitempty"`
	InlineData *inlineImage `json:"inlineData,omitempty"`
}

type inlineImage struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type imageResponse struct {
	Candidates []struct {
		Content      imageContent `json:"content"`
		FinishReason string       `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

func (g *ImageGenerator) Generate(ctx context.Context, req imagegen.ImageRequest) ([]imagegen.GeneratedImage, error) {
	parts := []imagePart{{Text: req.Prompt}}
	for _, img := range req.Inputs {
		parts = append(parts, imagePart{InlineData: &inlineImage{MimeType: img.MIME, Data: base64.StdEncoding.EncodeToString(img.Data)}})
	}
	endpoint := g.base + "/v1beta/models/" + url.PathEscape(g.model) + ":generateContent"
	httpReq, err := imagegen.NewJSONRequest(ctx, endpoint, struct {
		Contents []imageContent `json:"contents"`
	}{Contents: []imageContent{{Parts: parts}}})
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-goog-api-key", g.apiKey)
	resp, err := imagegen.Exchange[imageResponse](g.client, httpReq, imageFailure)
	if err != nil {
		return nil, fmt.Errorf("gemini images: %w", err)
	}
	return resp.firstImage()
}

func (r imageResponse) firstImage() ([]imagegen.GeneratedImage, error) {
	for _, c := range r.Candidates {
		for _, p := range c.Content.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			part, err := generic.GeneratedImage(p.InlineData.Data, p.InlineData.MimeType)
			if err != nil {
				return nil, fmt.Errorf("gemini images: %w", err)
			}
			return []imagegen.GeneratedImage{{Image: asset.Image{Data: part.Data, MIME: part.MIME}}}, nil
		}
	}
	if reason := r.refusal(); reason != "" {
		filtered := claierr.NewContentFiltered(&claierr.APIError{StatusCode: http.StatusOK, ProviderCode: reason})
		return nil, fmt.Errorf("gemini images: %w", errors.Join(imagegen.ErrNoImage, filtered))
	}
	return nil, fmt.Errorf("gemini images: %w", imagegen.ErrNoImage)
}

func (r imageResponse) refusal() string {
	if r.PromptFeedback.BlockReason != "" {
		return r.PromptFeedback.BlockReason
	}
	for _, c := range r.Candidates {
		switch c.FinishReason {
		case "SAFETY", "IMAGE_SAFETY", "PROHIBITED_CONTENT", "IMAGE_PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
			return c.FinishReason
		}
	}
	return ""
}

func imageFailure(status int, body []byte) error {
	return generic.ResponseError(status, body, nil)
}
