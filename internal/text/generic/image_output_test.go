package generic

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestGeneratedImage(t *testing.T) {
	want, _ := base64.StdEncoding.DecodeString(onePixelPNG)
	for _, declared := range []string{"image/png", ""} {
		img, err := GeneratedImage(onePixelPNG, declared)
		if err != nil {
			t.Fatalf("declared %q: %v", declared, err)
		}
		if !reflect.DeepEqual(img, pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginGenerated, Data: want}) {
			t.Fatalf("declared %q: image = %+v", declared, img)
		}
	}

	var mm *asset.MIMEMismatchError
	if _, err := GeneratedImage(onePixelPNG, "image/jpeg"); !errors.As(err, &mm) || mm.Declared != "image/jpeg" || mm.Sniffed != "image/png" {
		t.Fatalf("mismatch: err = %v", err)
	}

	var upr *claierr.UnexpectedProviderResponseError
	if _, err := GeneratedImage("***", "image/png"); !errors.As(err, &upr) || upr.StatusCode != http.StatusOK {
		t.Fatalf("bad base64: err = %v", err)
	}
	notImage := base64.StdEncoding.EncodeToString([]byte("plain text"))
	if _, err := GeneratedImage(notImage, ""); !errors.As(err, &upr) || !errors.Is(err, asset.ErrNotImage) {
		t.Fatalf("not an image: err = %v", err)
	}
}

func streamOf(t *testing.T, s *StreamCompleter, body string) []models.CompletionEvent {
	t.Helper()
	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	out, err := s.handleStreamResponse(context.Background(), res)
	if err != nil {
		t.Fatalf("handleStreamResponse: %v", err)
	}
	var evs []models.CompletionEvent
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs
}

func TestHandleStreamResponse_DecodeChunk(t *testing.T) {
	marker := models.ReasoningEvent{Content: "vendor"}
	var seen []string
	s := &StreamCompleter{DecodeChunk: func(payload []byte) ([]models.CompletionEvent, error) {
		seen = append(seen, string(payload))
		if strings.Contains(string(payload), "boom") {
			return nil, errors.New("boom")
		}
		if strings.Contains(string(payload), "extra") {
			return []models.CompletionEvent{marker, marker}, nil
		}
		return nil, nil
	}}

	evs := streamOf(t, s, "data: {\"extra\":1,\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n"+
		"data: [DONE]\n")
	want := []models.CompletionEvent{marker, marker, "hi", models.StopEvent{}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %#v, want %#v", evs, want)
	}
	if !strings.HasPrefix(seen[0], "{") {
		t.Fatalf("hook saw %q, want the payload without the data prefix", seen[0])
	}

	evs = streamOf(t, s, "data: {\"boom\":1}\ndata: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n")
	if len(evs) != 1 {
		t.Fatalf("events after a hook error = %#v, want only the error", evs)
	}
	if err, ok := evs[0].(error); !ok || err.Error() != "boom" {
		t.Fatalf("event = %#v, want the hook error", evs[0])
	}
}

func TestCreateRequest_OutputModalities(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "c"}}}
	for _, row := range []struct {
		modalities []string
		want       string
	}{
		{nil, ""},
		{[]string{"image", "text"}, `"modalities":["image","text"]`},
	} {
		s := &StreamCompleter{Model: "m", apiKey: "k", URL: "http://example.invalid", OutputModalities: row.modalities}
		req, err := s.createRequest(context.Background(), chat)
		if err != nil {
			t.Fatalf("createRequest: %v", err)
		}
		b, _ := io.ReadAll(req.Body)
		if row.want == "" && strings.Contains(string(b), "modalities") {
			t.Fatalf("body carries modalities: %s", b)
		}
		if !strings.Contains(string(b), row.want) {
			t.Fatalf("body lacks %s: %s", row.want, b)
		}
	}
}
