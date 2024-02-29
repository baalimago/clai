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

// queryPhotoModel using the supplied arguments as instructions
func queryPhotoModel(ctx context.Context, model, API_KEY, pictureDir string, text []string) error {
	url := "https://api.openai.com/v1/images/generations"
	body := imageQuery{
		Model:          model,
		Prompt:         fmt.Sprintf("I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: %v", strings.Join(text, " ")),
		N:              1,
		Size:           "1024x1024",
		Quality:        "hd",
		Style:          "vivid",
		ResponseFormat: "b64_json",
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", API_KEY))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed tosending request: %w", err)
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(bytes))
	}
	var imgResps ImageResponses
	err = json.Unmarshal(bytes, &imgResps)
	if err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	for _, imgResp := range imgResps.Data {
		data, err := base64.StdEncoding.DecodeString(imgResp.B64_JSON)
		if err != nil {
			return fmt.Errorf("failed to decode base64: %w", err)
		}
		outFile := fmt.Sprintf("%v/goai_%v.jpg", pictureDir, randomPrefix())
		err = os.WriteFile(outFile, data, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		ancli.PrintOK(fmt.Sprintf("revised prompt: '%v'\nimage rendered to: '%v'", imgResp.RevisedPrompt, outFile))
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
