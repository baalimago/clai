package openrouter

import (
	"encoding/json"
	"net/http"

	"github.com/baalimago/clai/pkg/claierr"
)

// decodeError is openrouter's vendor decoder (worklog
// 2026-09-05-error-propagation, D11). Recognized signal, from the live probe
// (journal, "Live vendor probe", 2026-09-05): a drained account answers 402
// with metadata.limit_source "openrouter_credits" and a numeric code. The
// decoder keys on the status and preserves the limit_source as the
// provider-code fact when the body parses. Anything but 402 is unrecognized:
// nil, and the baseline stands.
func decodeError(status int, body []byte) error {
	if status != http.StatusPaymentRequired {
		return nil
	}
	api := &claierr.APIError{StatusCode: status, Body: string(body)}
	var probe struct {
		Error struct {
			Metadata struct {
				LimitSource string `json:"limit_source"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		api.ProviderCode = probe.Error.Metadata.LimitSource
	}
	return claierr.NewInsufficientCredits(api)
}
