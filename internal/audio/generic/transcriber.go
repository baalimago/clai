// Package generic implements the OpenAI-protocol multipart transcription
// client shared by all compatible vendors. Vendor-specific behavior (env keys,
// URLs, model prefixes, headers) lives in the vendor packages.
package generic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/apikey"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/resend"
	"github.com/baalimago/clai/internal/runenv"
	textgeneric "github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/pkg/claierr"
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

// Diarization is what a transcriber's speaker labels mean beyond one request.
type Diarization int

const (
	// DiarizationNone means the transcript carries no speaker labels
	DiarizationNone Diarization = iota
	// DiarizationAnchored labels are request-local and calibration maps them across requests (architecture/audio.md)
	DiarizationAnchored
	// DiarizationRequestLocal labels are request-local and never mapped across requests
	DiarizationRequestLocal
)

type Transcriber struct {
	Model        string
	URL          string
	ExtraHeaders map[string]string
	// Fields are extra form fields the vendor's protocol needs
	Fields map[string]string
	Client *http.Client
	// Format is the vendor-negotiated response format; empty uses the model-name rule
	Format ResponseFormat
	// Stream asks for diarized segments as server-sent events when the request's
	// context has a Heard; the vendor sets it for the models that stream them
	Stream bool
	// DecodeError is the vendor's reading of a failed response; nil keeps the status baseline
	DecodeError func(status int, body []byte) error
	apiKey      string
}

func (t *Transcriber) Setup(apiKeyEnv, url string) error {
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return &apikey.MissingError{Env: apiKeyEnv, Message: fmt.Sprintf("environment variable '%v' not set", apiKeyEnv)}
	}
	if t.Client == nil {
		t.Client = &http.Client{}
	}
	t.apiKey = apiKey
	t.URL = url
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
	format := t.responseFormat()
	if heard, ok := heardFrom(ctx); ok && t.Stream && format == ResponseFormatDiarizedJSON {
		return t.transcribeStream(ctx, filePath, heard)
	}
	parse, err := parserFor(format)
	if err != nil {
		return nil, err
	}
	body, err := t.Request(ctx, filePath)
	if err != nil {
		return nil, err
	}
	segs, err := parse(body)
	if err == nil {
		runenv.Debugf(ctx, runenv.SubsystemAudio, "transcription parsed segments=%d", len(segs))
	}
	return segs, err
}

// logResponse keeps the body, which is meeting content, out of the debug level.
func logResponse(ctx context.Context, status string, body []byte, sent time.Time) {
	runenv.Debugf(ctx, runenv.SubsystemAudio, "transcription response status=%q bytes=%d took=%.1fs", status, len(body), time.Since(sent).Seconds())
	runenv.Tracef(ctx, runenv.SubsystemAudio, "transcription response body=%s", body)
}

// transcribeStream is Transcribe with the segments streamed (architecture/audio.md, Progress).
func (t *Transcriber) transcribeStream(ctx context.Context, filePath string, heard Heard) ([]Segment, error) {
	file, err := t.open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	sent := time.Now()
	resp, err := t.send(ctx, file, ResponseFormatDiarizedJSON, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	segs, err := ParseStream(resp.Body, heard)
	if err != nil && ctx.Err() != nil {
		return nil, diag.Errorf("transcription stream aborted: %w", ctx.Err())
	}
	if err == nil {
		runenv.Debugf(ctx, runenv.SubsystemAudio, "transcription response status=%q stream=true segments=%d took=%.1fs", resp.Status, len(segs), time.Since(sent).Seconds())
	}
	return segs, err
}

// Request posts the file and returns the raw payload for a vendor's own parser.
func (t *Transcriber) Request(ctx context.Context, filePath string) ([]byte, error) {
	file, err := t.open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return t.post(ctx, file, t.responseFormat())
}

// open checks the vendor's fields, then opens the audio file.
func (t *Transcriber) open(filePath string) (*os.File, error) {
	for name := range t.Fields {
		switch name {
		case "file", "model", "response_format", "chunking_strategy", "stream":
			return nil, fmt.Errorf("transcriber field %q is written by the client and cannot be set", name)
		}
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, diag.Errorf("failed to open audio file: %w", err)
	}
	return file, nil
}

func (t *Transcriber) post(ctx context.Context, file *os.File, respFormat ResponseFormat) ([]byte, error) {
	sent := time.Now()
	resp, err := t.send(ctx, file, respFormat, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, diag.Errorf("transcription response read aborted: %w", ctx.Err())
		}
		return nil, diag.Errorf("failed to read transcription response: %w", err)
	}
	logResponse(ctx, resp.Status, body, sent)
	return body, nil
}

// send posts the file and returns the response once its status is 200 OK;
// any other status is the claierr meaning of the response (architecture/errors.md).
func (t *Transcriber) send(ctx context.Context, file *os.File, respFormat ResponseFormat, stream bool) (*http.Response, error) {
	// Pipe streams the file part to the request without buffering it in memory
	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)
	fields := t.Fields
	if stream {
		fields = maps.Clone(t.Fields)
		if fields == nil {
			fields = map[string]string{}
		}
		fields["stream"] = "true"
	}
	go func() {
		err := writeMultipart(multipartWriter, file, t.Model, respFormat, fields)
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		pipeWriter.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, pipeReader)
	if err != nil {
		return nil, diag.Errorf("failed to create transcription request: %w", err)
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	for key, value := range t.ExtraHeaders {
		req.Header.Set(key, value)
	}
	runenv.Debugf(ctx, runenv.SubsystemAudio, "transcription request url=%s model=%s response_format=%v stream=%v", t.URL, t.Model, respFormat, stream)
	sent := time.Now()
	resp, err := t.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, diag.Errorf("transcription request aborted: %w", ctx.Err())
		}
		return nil, diag.Errorf("failed to post transcription request: %w", claierr.NewTransport(err))
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, diag.Errorf("failed to read transcription response: %w", err)
	}
	logResponse(ctx, resp.Status, body, sent)
	failure := textgeneric.ResponseError(resp.StatusCode, body, t.DecodeError)
	var facts claierr.APIErrorer
	if errors.As(failure, &facts) {
		facts.API().RetryAfter = resend.RetryAfter(resp.Header, time.Now())
	}
	return nil, diag.Errorf("transcription request failed: %w", failure)
}

func writeMultipart(w *multipart.Writer, file *os.File, model string, respFormat ResponseFormat, fields map[string]string) error {
	part, err := w.CreateFormFile("file", filepath.Base(file.Name()))
	if err != nil {
		return diag.Errorf("failed to create file part: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return diag.Errorf("failed to stream audio file: %w", err)
	}
	if err := w.WriteField("model", model); err != nil {
		return diag.Errorf("failed to write model field: %w", err)
	}
	if err := w.WriteField("response_format", string(respFormat)); err != nil {
		return diag.Errorf("failed to write response_format field: %w", err)
	}
	// OpenAI requires chunking_strategy for diarization models (400 without it)
	if respFormat == ResponseFormatDiarizedJSON {
		if err := w.WriteField("chunking_strategy", "auto"); err != nil {
			return diag.Errorf("failed to write chunking_strategy field: %w", err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if err := w.WriteField(name, fields[name]); err != nil {
			return diag.Errorf("failed to write %v field: %w", name, err)
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
