package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type DallE struct {
	Model   string       `json:"model"`
	N       int          `json:"n"`
	Size    string       `json:"size"`
	Quality string       `json:"quality"`
	Style   string       `json:"style,omitempty"`
	Output  photo.Output `json:"output"`
	// Don't save this as this is set via the Output struct
	ResponseFormat string       `json:"-"`
	Prompt         string       `json:"-"`
	client         *http.Client `json:"-"`
	debug          bool         `json:"-"`
	raw            bool         `json:"-"`
	apiKey         string       `json:"-"`
}

type DallERequest struct {
	Model          string `json:"model"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Prompt         string `json:"prompt"`
}

type ImageResponse struct {
	RevisedPrompt string `json:"revised_prompt"`
	URL           string `json:"url"`
	B64JSON       string `json:"b64_json"`
}

type ImageResponses struct {
	Created int             `json:"created"`
	Data    []ImageResponse `json:"data"`
}

var defaultDalle = DallE{
	Model:   "gpt-image-1",
	Size:    "1024x1024",
	N:       1,
	Quality: "auto",
}

func NewPhotoQuerier(pConf photo.Configurations) (models.Querier, error) {
	home, _ := os.UserConfigDir()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	model := pConf.Model
	defaultCpy := defaultDalle
	defaultCpy.Model = model
	defaultCpy.Output = pConf.Output
	if strings.Contains(model, "dall-e") {
		defaultCpy.Quality = "hd"
		defaultCpy.Output.Type = photo.LOCAL
	}
	// Load config based on model, allowing for different configs for each model
	dalleQuerier, err := utils.LoadConfigFromFile(home, fmt.Sprintf("openai_dalle_%v.json", model), nil, &defaultCpy)
	switch dalleQuerier.Output.Type {
	case photo.URL:
		dalleQuerier.ResponseFormat = "url"
	case photo.LOCAL:
		dalleQuerier.ResponseFormat = "b64_json"
	case photo.UNSET:
		dalleQuerier.ResponseFormat = ""
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		dalleQuerier.debug = true
	}
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to load config for model: %v, error: %v\n", model, err))
	}
	dalleQuerier.client = &http.Client{}
	dalleQuerier.apiKey = apiKey
	dalleQuerier.Prompt = pConf.Prompt
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &dalleQuerier, nil
}

func (q *DallE) createRequest(ctx context.Context) (*http.Request, error) {
	if q.debug {
		ancli.PrintOK(fmt.Sprintf("DallE request: %+v\n", q))
	}
	reqVersion := DallERequest{
		Model:          q.Model,
		N:              q.N,
		Size:           q.Size,
		Quality:        q.Quality,
		Style:          q.Style,
		ResponseFormat: q.ResponseFormat,
		Prompt:         q.Prompt,
	}
	bodyBytes, err := json.Marshal(reqVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", PhotoURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))
	req.Header.Set("Content-Type", "application/json")

	ancli.PrintOK(fmt.Sprintf("pre-revision prompt: '%v'\n", q.Prompt))
	return req, nil
}

func (q *DallE) do(req *http.Request) error {
	if !q.raw {
		stop := photo.StartAnimation()
		defer stop()
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed tosending request: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(b))
	}
	var imgResps ImageResponses
	err = json.Unmarshal(b, &imgResps)
	if err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	if q.Output.Type == photo.URL {
		defer func() {
			ancli.PrintOK(fmt.Sprintf("image URL: '%v'", imgResps.Data[0].URL))
		}()
	} else {
		localPath, err := q.saveImage(imgResps.Data[0])
		if err != nil {
			return fmt.Errorf("failed to save image: %w", err)
		}
		// Defer to let animator finish first
		defer func() {
			ancli.PrintOK(fmt.Sprintf("image saved to: '%v'\n", localPath))
		}()

	}
	defer func() {
		fmt.Println()
		ancli.PrintOK(fmt.Sprintf("revised prompt: '%v'\n", imgResps.Data[0].RevisedPrompt))
	}()

	return nil
}

func (q *DallE) saveImage(imgResp ImageResponse) (string, error) {
	data, err := base64.StdEncoding.DecodeString(imgResp.B64JSON)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	pictureName := fmt.Sprintf("%v_%v.jpg", q.Output.Prefix, utils.RandomPrefix())
	outFile := fmt.Sprintf("%v/%v", q.Output.Dir, pictureName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", pictureName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return "", fmt.Errorf("failed to write file: %w", err)
		}
	}
	return outFile, nil
}

func (q *DallE) Query(ctx context.Context) error {
	req, err := q.createRequest(ctx)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	err = q.do(req)
	if err != nil {
		return fmt.Errorf("failed to do request: %w", err)
	}
	return nil
}
