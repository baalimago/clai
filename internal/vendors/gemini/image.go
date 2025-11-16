package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
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

func (gr *GeminiResponse) GetFirstB64Blob() (string, error) {
	for _, candidate := range gr.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				return part.InlineData.Data, nil
			}
		}
	}
	return "", fmt.Errorf("failed to find any image in response: %v", debug.IndentedJsonFmt(*gr))
}

// Seems like gemini lacks a lot of configuration. Even the model selection is via url, not body
const urlFormat = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

func (g *GeminiFlashImage) Query(ctx context.Context) error {
	if !g.Raw {
		stop := photo.StartAnimation()
		defer stop()
	}

	msgs, err := chat.PromptToImageMessage(g.Prompt)
	if err != nil {
		return fmt.Errorf("failed to prompt to image message: %w", err)
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("prompt to image msg msgs: %v", debug.IndentedJsonFmt(msgs))
	}

	parts := make([]Part, 0)
	for _, msg := range msgs {
		for _, cp := range msg.ContentParts {
			var p Part
			var inD InlineData
			if cp.Type == "image_url" {
				inD.Data = cp.ImageB64.RawB64
				inD.MimeType = cp.ImageB64.MIMEType
				p.InlineData = &inD
			}
			if cp.Type == "text" {
				p.Text = &cp.Text
			}
			parts = append(parts, p)
		}
		if msg.Content != "" {
			parts = append(parts, Part{
				Text: &msg.Content,
			})
		}
	}

	url := fmt.Sprintf(urlFormat, g.Model)
	req := GeminiRequest{
		Contents: []Content{
			{
				Parts: parts,
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
	if unnmarshalErr := json.Unmarshal(body, &gemResp); unnmarshalErr != nil {
		return fmt.Errorf("failed to decode JSON: %w", unnmarshalErr)
	}

	firstb64, err := gemResp.GetFirstB64Blob()
	if err != nil {
		return fmt.Errorf("failed to get first b64 blob: %w", err)
	}
	localPath, err := photo.SaveImage(
		g.Output,
		firstb64,
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
