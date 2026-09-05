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
