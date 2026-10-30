// Package generic implements the OpenAI-protocol multipart transcription
// client shared by all compatible vendors. Vendor-specific behavior (env keys,
// URLs, model prefixes, headers) lives in the vendor packages.
package generic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/debugflags"
)

// ResponseFormat is the JSON shape requested from the endpoint; text/srt/vtt are rendered locally.
type ResponseFormat string

const (
	// ResponseFormatJSON is text only, no timestamps: the transcript is one segment
	ResponseFormatJSON ResponseFormat = "json"
	// ResponseFormatVerboseJSON carries timestamped segments
	ResponseFormatVerboseJSON ResponseFormat = "verbose_json"
	// ResponseFormatDiarizedJSON adds speaker labels to the segments
	ResponseFormatDiarizedJSON ResponseFormat = "diarized_json"
)

type Transcriber struct {
	Model        string
	URL          string
	ExtraHeaders map[string]string
	Client       *http.Client
	// Format is the vendor-negotiated response format; empty uses the model-name rule
	Format ResponseFormat
	apiKey string
	debug  bool
}

func (t *Transcriber) Setup(apiKeyEnv, url, debugEnv string) error {
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return fmt.Errorf("environment variable '%v' not set", apiKeyEnv)
	}
	if t.Client == nil {
		t.Client = &http.Client{}
	}
	t.apiKey = apiKey
	t.URL = url
	if debugflags.EnabledEnv(debugEnv) {
		t.debug = true
	}
	return nil
}

func (t *Transcriber) responseFormat() ResponseFormat {
	if t.Format != "" {
		return t.Format
	}
	if strings.Contains(t.Model, "diarize") {
		return ResponseFormatDiarizedJSON
	}
	return ResponseFormatVerboseJSON
}

func parserFor(format ResponseFormat) (func([]byte) ([]Segment, error), error) {
	switch format {
	case ResponseFormatJSON:
		return ParseJSON, nil
	case ResponseFormatVerboseJSON:
		return ParseVerboseJSON, nil
	case ResponseFormatDiarizedJSON:
		return ParseDiarizedJSON, nil
	default:
		return nil, fmt.Errorf("unsupported transcription response format: %q, supported: %v, %v, %v", format, ResponseFormatJSON, ResponseFormatVerboseJSON, ResponseFormatDiarizedJSON)
	}
}

func (t *Transcriber) Transcribe(ctx context.Context, filePath string) ([]Segment, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer file.Close()
	respFormat := t.responseFormat()
	parse, err := parserFor(respFormat)
	if err != nil {
		return nil, err
	}
	body, err := t.post(ctx, file, respFormat)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func (t *Transcriber) post(ctx context.Context, file *os.File, respFormat ResponseFormat) ([]byte, error) {
	// Pipe streams the file part to the request without buffering it in memory
	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)
	go func() {
		err := writeMultipart(multipartWriter, file, t.Model, respFormat)
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		pipeWriter.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, pipeReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create transcription request: %w", err)
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	for key, value := range t.ExtraHeaders {
		req.Header.Set(key, value)
	}
	if t.debug {
		slog.Debug("transcription request", "url", t.URL, "model", t.Model, "response_format", respFormat)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("transcription request aborted: %w", ctx.Err())
		}
		return nil, fmt.Errorf("failed to post transcription request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("transcription response read aborted: %w", ctx.Err())
		}
		return nil, fmt.Errorf("failed to read transcription response: %w", err)
	}
	if t.debug {
		slog.Debug("transcription response", "status", resp.Status, "body", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("transcription request failed with status: %v, body: %v", resp.Status, truncate(body, 1024))
	}
	return body, nil
}

func writeMultipart(w *multipart.Writer, file *os.File, model string, respFormat ResponseFormat) error {
	part, err := w.CreateFormFile("file", filepath.Base(file.Name()))
	if err != nil {
		return fmt.Errorf("failed to create file part: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to stream audio file: %w", err)
	}
	if err := w.WriteField("model", model); err != nil {
		return fmt.Errorf("failed to write model field: %w", err)
	}
	if err := w.WriteField("response_format", string(respFormat)); err != nil {
		return fmt.Errorf("failed to write response_format field: %w", err)
	}
	// OpenAI requires chunking_strategy for diarization models (400 without it)
	if respFormat == ResponseFormatDiarizedJSON {
		if err := w.WriteField("chunking_strategy", "auto"); err != nil {
			return fmt.Errorf("failed to write chunking_strategy field: %w", err)
		}
	}
	return nil
}

func truncate(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…"
}

// Endpoint reports the request URL; the calibration cache keys on it.
func (t *Transcriber) Endpoint() string { return t.URL }

// RequestFormat reports the format every request asks for; the splitter
// derives the per-request duration cap from it.
func (t *Transcriber) RequestFormat() ResponseFormat { return t.responseFormat() }
