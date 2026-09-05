package xai

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

// xaiErr drives the real xai completer against a server answering 403 with
// the given fixture (D14 reconstructions, see testdata/README.md),
// returning the error from StreamCompletions.
func xaiErr(t *testing.T, fixtureName string) error {
	t.Helper()
	body, readErr := os.ReadFile(filepath.Join("testdata", fixtureName))
	if readErr != nil {
		t.Fatalf("read fixture: %v", readErr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("XAI_API_KEY", "test-key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v.StreamCompleter.URL = srv.URL
	_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	return err
}

// Test_XaiDecode_DrainedCredits_NotAuthFailed pins the D11 retraction that
// motivated vendor-first decoding: xAI answers 403 for a drained account
// (journal, "Live vendor probe"), so the drained-credits body means
// insufficient credits ALONE — the baseline's auth guess must not fire and
// page someone about API keys while spend continues. A 403 with any other
// body keeps the baseline's ErrAuthFailed.
func Test_XaiDecode_DrainedCredits_NotAuthFailed(t *testing.T) {
	t.Run("drained-credits body means credits alone", func(t *testing.T) {
		err := xaiErr(t, "drained_credits_403.json")
		if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
			t.Fatalf("expected ErrLikelyInsufficientCredits, got: %v", err)
		}
		if errors.Is(err, claierr.ErrAuthFailed) {
			t.Fatalf("auth meaning fired on a drained account: %v", err)
		}
		var ic *claierr.InsufficientCreditsError
		if !errors.As(err, &ic) {
			t.Fatalf("expected *claierr.InsufficientCreditsError, got: %v", err)
		}
		if ic.StatusCode != http.StatusForbidden {
			t.Fatalf("facts status: got %v", ic.StatusCode)
		}
		if !strings.Contains(ic.Body, "used all available credits") {
			t.Fatalf("facts body lost: %q", ic.Body)
		}
	})

	t.Run("unrecognized 403 body keeps the baseline auth meaning", func(t *testing.T) {
		err := xaiErr(t, "forbidden_403.json")
		if !errors.Is(err, claierr.ErrAuthFailed) {
			t.Fatalf("expected baseline ErrAuthFailed, got: %v", err)
		}
		if errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
			t.Fatalf("credits meaning fired on a plain 403: %v", err)
		}
	})

	t.Run("decoder is silent off 403", func(t *testing.T) {
		if err := decodeError(http.StatusPaymentRequired, []byte("used all available credits")); err != nil {
			t.Fatalf("expected nil off 403, got: %v", err)
		}
	})
}
