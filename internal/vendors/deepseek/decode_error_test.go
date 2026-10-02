package deepseek

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Test_DeepseekDecode_InsufficientBalance drives the real deepseek completer
// against the reconstructed 402 drained-balance fixture (D14, see
// testdata/README.md): the decoder keys on the status — the body's code and
// type fields are useless (journal, "Live vendor probe") — and preserves the
// body as facts.
func Test_DeepseekDecode_InsufficientBalance(t *testing.T) {
	body, readErr := os.ReadFile(filepath.Join("testdata", "insufficient_balance_402.json"))
	if readErr != nil {
		t.Fatalf("read fixture: %v", readErr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v.StreamCompleter.URL = srv.URL
	_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
		t.Fatalf("expected ErrLikelyInsufficientCredits, got: %v", err)
	}
	var ic *claierr.InsufficientCreditsError
	if !errors.As(err, &ic) {
		t.Fatalf("expected *claierr.InsufficientCreditsError, got: %v", err)
	}
	if ic.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("facts status: got %v", ic.StatusCode)
	}
	if !strings.Contains(ic.Body, "Insufficient Balance") {
		t.Fatalf("facts body lost: %q", ic.Body)
	}
	if ic.ProviderCode != "invalid_request_error" {
		t.Fatalf("facts provider code: got %q", ic.ProviderCode)
	}
}

func Test_DeepseekDecode_BadRequestPreservesFullMessage(t *testing.T) {
	message := "The supported API model names are deepseek-flash, deepseek-v4-pro, but you passed deepseek-v4. (request_id: e50ef622-1d11-41c6-b6c5-ffc5d73879fc)"
	body := `{"error":{"message":"` + message + `","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v.StreamCompleter.URL = srv.URL
	_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
	}
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("provider message was missing or truncated: %v", err)
	}
	if !strings.Contains(err.Error(), "provider code: invalid_request_error") {
		t.Fatalf("provider code was not included in the error: %v", err)
	}
	var apiErr claierr.APIErrorer
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected API error facts, got: %v", err)
	}
	if apiErr.API().ProviderCode != "invalid_request_error" {
		t.Fatalf("provider code: got %q", apiErr.API().ProviderCode)
	}
	if apiErr.API().Message != message {
		t.Fatalf("provider message fact: got %q, want %q", apiErr.API().Message, message)
	}
}

// Test_DeepseekDecode_NonPaymentRequired_Nil pins the decoder's silence on
// anything but 402: the baseline stands alone.
func Test_DeepseekDecode_NonPaymentRequired_Nil(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if err := decodeError(status, []byte(`{"error":{"message":"x"}}`)); err != nil {
			t.Fatalf("expected nil for status %v, got: %v", status, err)
		}
	}
}

func Test_DeepseekDecode_BadRequestWithoutMessage_Nil(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`not json`, `{"error":{"code":"invalid_request_error"}}`, `{"message":"outside error envelope"}`} {
		if err := decodeError(http.StatusBadRequest, []byte(body)); err != nil {
			t.Errorf("decodeError(400, %q) = %v, want nil", body, err)
		}
	}
}
