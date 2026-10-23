package imagegen

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
)

func TestErrorsNameTheirFacts(t *testing.T) {
	if msg := (&UnknownImageModelError{Model: "claude-x"}).Error(); !strings.Contains(msg, `"claude-x"`) || !strings.Contains(msg, "gpt-image-*") {
		t.Errorf("UnknownImageModelError = %q", msg)
	}
	if msg := (&MissingAPIKeyError{Env: "GEMINI_API_KEY"}).Error(); !strings.Contains(msg, "GEMINI_API_KEY") {
		t.Errorf("MissingAPIKeyError = %q", msg)
	}
}

type reply struct {
	Name string `json:"name"`
}

func TestExchange(t *testing.T) {
	status, body := http.StatusOK, `{"name":"ok"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" || r.Method != http.MethodPost {
			t.Errorf("%s with Content-Type %q", r.Method, r.Header.Get("Content-Type"))
		}
		if b, _ := io.ReadAll(r.Body); string(b) != `{"name":"in"}` {
			t.Errorf("body = %s", b)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	var failed []int
	failure := func(status int, body []byte) error {
		failed = append(failed, status)
		return claierr.NewUnexpectedProviderResponse(status, body)
	}
	send := func(ctx context.Context, client *http.Client) (reply, error) {
		req, err := NewJSONRequest(ctx, srv.URL, reply{Name: "in"})
		if err != nil {
			t.Fatalf("NewJSONRequest: %v", err)
		}
		return Exchange[reply](client, req, failure)
	}

	if got, err := send(context.Background(), nil); err != nil || got.Name != "ok" {
		t.Errorf("2xx with the default client: (%v, %v)", got, err)
	}

	status, body = http.StatusTeapot, "nope"
	if _, err := send(context.Background(), srv.Client()); !errors.Is(err, claierr.ErrUnexpectedProviderResponse) || len(failed) != 1 || failed[0] != http.StatusTeapot {
		t.Errorf("non-2xx: err %v, failure saw %v", err, failed)
	}

	status, body = http.StatusOK, "not json"
	if _, err := send(context.Background(), srv.Client()); !errors.Is(err, claierr.ErrUnexpectedProviderResponse) || len(failed) != 1 {
		t.Errorf("undecodable 2xx: err %v, failure saw %v", err, failed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := send(ctx, srv.Client()); !errors.Is(err, context.Canceled) || errors.Is(err, claierr.ErrTransport) {
		t.Errorf("cancelled: err = %v, want context.Canceled and no transport failure", err)
	}

	srv.Close()
	if _, err := send(context.Background(), srv.Client()); !errors.Is(err, claierr.ErrTransport) {
		t.Errorf("closed server: err = %v, want ErrTransport", err)
	}
}

func TestNewJSONRequestRejectsUnencodableBody(t *testing.T) {
	if _, err := NewJSONRequest(context.Background(), "http://vendor.invalid", make(chan int)); err == nil {
		t.Error("NewJSONRequest encoded a channel")
	}
	if _, err := NewJSONRequest(context.Background(), "http://vendor.invalid/\x7f", reply{}); err == nil {
		t.Error("NewJSONRequest accepted an invalid URL")
	}
}
