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
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"golang.org/x/term"
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
	Model         string `json:"model"`
	PictureDir    string `json:"picture-dir"`
	PicturePrefix string `json:"picture-prefix"`
	PromptFormat  string `json:"prompt-format"`
	url           string
	raw           bool
	client        *http.Client
}

func (pq *photoQuerier) query(ctx context.Context, API_KEY string, text []string) (ImageResponses, error) {
	body := imageQuery{
		Model:          pq.Model,
		Prompt:         fmt.Sprintf(pq.PromptFormat, strings.Join(text, " ")),
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

	req, err := http.NewRequestWithContext(ctx, "POST", pq.url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", API_KEY))
	req.Header.Set("Content-Type", "application/json")

	ancli.PrintOK(fmt.Sprintf("command setup: '%v', sending request\n", body.Prompt))
	if !pq.raw {
		stop := startAnimation()
		defer stop()
	}
	resp, err := pq.client.Do(req)
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed tosending request: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ImageResponses{}, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != 200 {
		return ImageResponses{}, fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(b))
	}
	var imgResps ImageResponses
	err = json.Unmarshal(b, &imgResps)
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
	pictureName := fmt.Sprintf("%v_%v.jpg", pq.PicturePrefix, randomPrefix())
	outFile := fmt.Sprintf("%v/%v", pq.PictureDir, pictureName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", pictureName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
	}
	ancli.PrintOK(fmt.Sprintf("revised prompt: '%v'\nimage rendered to: '%v'\n", imgResp.RevisedPrompt, outFile))
	return nil
}

// queryPhotoModel using the supplied arguments as instructions
func (pq *photoQuerier) queryPhotoModel(ctx context.Context, API_KEY string, text []string) error {
	imgResps, err := pq.query(ctx, API_KEY, text)
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

func startAnimation() func() {
	t0 := time.Now()
	ticker := time.NewTicker(time.Second / 60)
	stop := make(chan struct{})
	termInt := int(os.Stderr.Fd())
	termWidth, _, err := term.GetSize(termInt)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
		termWidth = 100
	}
	go func() {
		for {
			select {
			case <-ticker.C:
				cTick := time.Since(t0)
				clearLine := strings.Repeat(" ", termWidth)
				fmt.Printf("\r%v", clearLine)
				fmt.Printf("\rElapsed time: %v - %v", funimation(cTick), cTick)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		fmt.Print("\n\r")
	}
}

func funimation(t time.Duration) string {
	images := []string{
		"🕛",
		"🕧",
		"🕐",
		"🕜",
		"🕑",
		"🕝",
		"🕒",
		"🕞",
		"🕓",
		"🕟",
		"🕔",
		"🕠",
		"🕕",
		"🕡",
		"🕖",
		"🕢",
		"🕗",
		"🕣",
		"🕘",
		"🕤",
		"🕙",
		"🕥",
		"🕚",
		"🕦",
	}
	// 1 nanosecond / 23 frames = 43478260 nanoseconds. Too low brainjuice to know
	// why that works right now
	return images[int(t.Nanoseconds()/43478260)%len(images)]
}
