package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/pkg/claierr"
)

const imagesBaseURL = "https://api.openai.com"

// DefaultImageModel is clai's default photo model, and the image model generate_image
// uses beside an OpenAI chat model when the photo model is not an OpenAI one.
const DefaultImageModel = "gpt-image-2.5-flare"

// IsImageModel matches the README parameter "OpenAI image model names".
func IsImageModel(model string) bool { return isOpenAIImageModel(model) }

// ImageGenerator is the OpenAI images backend: generations without inputs, edits with them.
type ImageGenerator struct {
	model, base, apiKey string
	defaults            imageDefaults
	client              *http.Client
}

type imageDefaults struct {
	N       int    `json:"n"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
}

// NewImageGenerator reads the per-model defaults of cfg.ConfigDir without creating them.
func NewImageGenerator(cfg imagegen.GeneratorConfig, apiKey string) (*ImageGenerator, error) {
	defaults, err := loadImageDefaults(cfg.ConfigDir, cfg.Model)
	if err != nil {
		return nil, err
	}
	base := cfg.BaseURL
	if base == "" {
		base = imagesBaseURL
	}
	return &ImageGenerator{model: cfg.Model, base: base, apiKey: apiKey, defaults: defaults, client: cfg.Client}, nil
}

func loadImageDefaults(configDir, model string) (imageDefaults, error) {
	defaults := imageDefaults{N: 1, Size: "1024x1024", Quality: "auto"}
	if strings.Contains(model, "dall-e") {
		defaults.Quality = "hd"
	}
	if configDir == "" {
		return defaults, nil
	}
	path := filepath.Join(configDir, fmt.Sprintf("openai_dalle_%v.json", model))
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("read openai image defaults: %w", err)
	}
	if err := json.Unmarshal(b, &defaults); err != nil {
		return defaults, fmt.Errorf("parse openai image defaults %s: %w", path, err)
	}
	return defaults, nil
}

type imageParams struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type imagesResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	OutputFormat string `json:"output_format"`
}

func (g *ImageGenerator) Generate(ctx context.Context, req imagegen.ImageRequest) ([]imagegen.GeneratedImage, error) {
	params := g.params(req)
	var httpReq *http.Request
	var err error
	if len(req.Inputs) == 0 {
		httpReq, err = imagegen.NewJSONRequest(ctx, g.base+"/v1/images/generations", params)
	} else {
		httpReq, err = editRequest(ctx, g.base+"/v1/images/edits", params, req.Inputs)
	}
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	resp, err := imagegen.Exchange[imagesResponse](g.client, httpReq, imageFailure)
	if err != nil {
		return nil, fmt.Errorf("openai images: %w", err)
	}
	return resp.images()
}

func (g *ImageGenerator) params(req imagegen.ImageRequest) imageParams {
	p := imageParams{Model: g.model, Prompt: req.Prompt, N: g.defaults.N, Size: g.defaults.Size, Quality: g.defaults.Quality}
	if req.Size != "" {
		p.Size = req.Size
	}
	if strings.HasPrefix(g.model, "dall-e-") {
		p.ResponseFormat = "b64_json"
	}
	return p
}

func editRequest(ctx context.Context, url string, p imageParams, inputs []asset.Image) (*http.Request, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fields := [][2]string{{"model", p.Model}, {"prompt", p.Prompt}, {"size", p.Size}, {"quality", p.Quality}, {"response_format", p.ResponseFormat}}
	if p.N > 0 {
		fields = append(fields, [2]string{"n", strconv.Itoa(p.N)})
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, fmt.Errorf("write edit field %s: %w", f[0], err)
		}
	}
	for i, img := range inputs {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="image-%d.%s"`, i, strings.TrimPrefix(img.MIME, "image/")))
		header.Set("Content-Type", img.MIME)
		part, err := w.CreatePart(header)
		if err != nil {
			return nil, fmt.Errorf("create edit image %d: %w", i, err)
		}
		if _, err := part.Write(img.Data); err != nil {
			return nil, fmt.Errorf("write edit image %d: %w", i, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close edit body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("build edit request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, nil
}

func (r imagesResponse) images() ([]imagegen.GeneratedImage, error) {
	if len(r.Data) == 0 || r.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("openai images: %w", imagegen.ErrNoImage)
	}
	part, err := generic.GeneratedImage(r.Data[0].B64JSON, outputMIME(r.OutputFormat))
	if err != nil {
		return nil, fmt.Errorf("openai images: %w", err)
	}
	return []imagegen.GeneratedImage{{Image: asset.Image{Data: part.Data, MIME: part.MIME}, RevisedPrompt: r.Data[0].RevisedPrompt}}, nil
}

func outputMIME(format string) string {
	if format == "" {
		return ""
	}
	return "image/" + format
}

func imageFailure(status int, body []byte) error {
	return generic.ResponseError(status, body, decodeImageError)
}

// decodeImageError is decodeError plus the images API's content-policy refusals.
func decodeImageError(status int, body []byte) error {
	if err := decodeError(status, body); err != nil {
		return err
	}
	facts := parseProviderError(body)
	if facts.Code != "moderation_blocked" && facts.Code != "content_policy_violation" {
		return nil
	}
	return claierr.NewContentFiltered(&claierr.APIError{
		StatusCode:   status,
		ProviderCode: facts.Code,
		Message:      facts.Message,
		Body:         string(body),
	})
}
