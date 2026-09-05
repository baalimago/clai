package openrouter

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

// Test_OpenrouterDecode_CreditsExhausted drives the real openrouter
// completer against the reconstructed 402 credits fixture (D14, see
// testdata/README.md — journal shape: metadata.limit_source
// "openrouter_credits", numeric code) and expects the credits meaning with
// the limit_source preserved as the provider-code fact.
func Test_OpenrouterDecode_CreditsExhausted(t *testing.T) {
	body, readErr := os.ReadFile(filepath.Join("testdata", "credits_402.json"))
	if readErr != nil {
		t.Fatalf("read fixture: %v", readErr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	o := Default
	if err := o.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	o.StreamCompleter.URL = srv.URL
	_, err := o.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
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
	if ic.ProviderCode != "openrouter_credits" {
		t.Fatalf("facts provider code: got %q", ic.ProviderCode)
	}
	if !strings.Contains(ic.Body, "requires more credits") {
		t.Fatalf("facts body lost: %q", ic.Body)
	}
}

// Test_OpenrouterDecode_NonPaymentRequired_Nil pins the decoder's silence on
// anything but 402: the baseline stands alone.
func Test_OpenrouterDecode_NonPaymentRequired_Nil(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if err := decodeError(status, []byte(`{"error":{"message":"x","code":500}}`)); err != nil {
			t.Fatalf("expected nil for status %v, got: %v", status, err)
		}
	}
}
