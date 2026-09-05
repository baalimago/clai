package xai

import (
	"bytes"
	"net/http"

	"github.com/baalimago/clai/pkg/claierr"
)

// drainedCreditsSignal is the phrase xAI's drained-account 403 body carries
// (journal, "Live vendor probe", 2026-09-05; fixture reconstructed per D14).
var drainedCreditsSignal = []byte("used all available credits")

// decodeError is xai's vendor decoder, and the vendor quirk that motivated
// vendor-first decoding (worklog 2026-09-05-error-propagation, D11): xAI
// answers 403 — not 402 — for a drained account, so a 403 whose body carries
// the drained-credits phrase means insufficient credits ALONE; the
// baseline's ErrAuthFailed guess must not fire and page someone about API
// keys while spend continues. A 403 with any other body is unrecognized:
// nil, and the baseline's auth meaning stands.
func decodeError(status int, body []byte) error {
	if status != http.StatusForbidden {
		return nil
	}
	if !bytes.Contains(body, drainedCreditsSignal) {
		return nil
	}
	return claierr.NewInsufficientCredits(&claierr.APIError{
		StatusCode: status,
		Body:       string(body),
	})
}
