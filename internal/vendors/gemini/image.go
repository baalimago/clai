package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"golang.org/x/net/context"
)

// Behold, a multi-billion dollar company API request/resonse schema
type InlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type Part struct {
	InlineData *InlineData `json:"inlineData,omitempty"`
	Text       *string     `json:"text,omitempty"`
}

type Content struct {
	Parts []Part `json:"parts"`
}

type GeminiRequest struct {
	Contents []Content `json:"contents"`
}

type GeminiFlashImage struct {
	photo.Configurations
	apiKey string
}

type Candidate struct {
	Content Content `json:"content"`
}

type GeminiResponse struct {
	Candidates []Candidate `json:"candidates"`
}

func (gr *GeminiResponse) GetFirstB64Blob() string {
	for _, candidate := range gr.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				return part.InlineData.Data
			}
		}
	}
	return ""
}

// Seems like gemini lacks a lot of configuration. Even the model selection is via url, not body
const urlFormat = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

func (g *GeminiFlashImage) Query(ctx context.Context) error {
	if !g.Raw {
		stop := photo.StartAnimation()
		defer stop()
	}

	url := fmt.Sprintf(urlFormat, g.Model)
	req := GeminiRequest{
		Contents: []Content{
			{
				Parts: []Part{
					{
						Text: &g.Prompt,
					},
				},
			},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewReader(b),
	)
	if err != nil {
		return fmt.Errorf("failed to creat gemini req: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", g.apiKey)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to query gemini: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"gemini: status %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read body: %w", err)
	}
	var gemResp GeminiResponse
	if err := json.Unmarshal(body, &gemResp); err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	localPath, err := photo.SaveImage(
		g.Output,
		gemResp.GetFirstB64Blob(),
		"png")
	if err != nil {
		return fmt.Errorf("failed to save image: %w", err)
	}
	// Defer to let animator finish first
	defer func() {
		ancli.PrintOK(fmt.Sprintf("image saved to: '%v'\n", localPath))
	}()

	return nil
}

func NewPhotoQuerier(pConf photo.Configurations) (models.Querier, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'GEMINI_API_KEY' not set")
	}

	geminiFlash := GeminiFlashImage{
		pConf,
		apiKey,
	}
	return &geminiFlash, nil
}
