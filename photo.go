package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type imageQuery struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	Style          string `json:"style"`
	ResponseFormat string `json:"response_format"`
}

type ImageResponse struct {
	RevisedPrompt string `json:"revised_prompt"`
	URL           string `json:"url"`
	B64_JSON      string `json:"b64_json"`
}

type ImageResponses struct {
	Created int             `json:"created"`
	Data    []ImageResponse `json:"data"`
}

type photoQuerier struct {
	model, API_KEY, pictureDir, picturePrefix string
}

func (pq *photoQuerier) query(ctx context.Context, text []string) (ImageResponses, error) {
	url := "https://api.openai.com/v1/images/generations"
	body := imageQuery{
		Model:          pq.model,
		Prompt:         fmt.Sprintf("I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'", strings.Join(text, " ")),
		N:              1,
		Size:           "1024x1024",
		Quality:        "hd",
		Style:          "vivid",
		ResponseFormat: "b64_json",
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", pq.API_KEY))
	req.Header.Set("Content-Type", "application/json")

	ancli.PrintOK(fmt.Sprintf("command setup: '%v', sending request\n", body.Prompt))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed tosending request: %w", err)
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return ImageResponses{}, fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(bytes))
	}
	var imgResps ImageResponses
	err = json.Unmarshal(bytes, &imgResps)
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed to decode JSON: %w", err)
	}
	return imgResps, nil
}

func (pq *photoQuerier) saveImage(ctx context.Context, imgResp ImageResponse) error {
	data, err := base64.StdEncoding.DecodeString(imgResp.B64_JSON)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}
	pictureName := fmt.Sprintf("%v_%v.jpg", pq.picturePrefix, randomPrefix())
	outFile := fmt.Sprintf("%v/%v", pq.pictureDir, pictureName)
	err = os.WriteFile(outFile, data, 0644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", pictureName)
		err = os.WriteFile(outFile, data, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
	}
	ancli.PrintOK(fmt.Sprintf("revised prompt: '%v'\nimage rendered to: '%v'\n", imgResp.RevisedPrompt, outFile))
	return nil
}

// queryPhotoModel using the supplied arguments as instructions
func (pq *photoQuerier) queryPhotoModel(ctx context.Context, text []string) error {
	imgResps, err := pq.query(ctx, text)
	if err != nil {
		return err
	}
	for _, imgResp := range imgResps.Data {
		pq.saveImage(ctx, imgResp)
	}
	return nil
}

func randomPrefix() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 10)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}

	return string(result)
}
