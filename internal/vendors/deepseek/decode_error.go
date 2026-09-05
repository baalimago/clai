package deepseek

import (
	"encoding/json"
	"net/http"

	"github.com/baalimago/clai/pkg/claierr"
)

// decodeError is deepseek's vendor decoder (worklog
// 2026-09-05-error-propagation, D11). Recognized signal, from the live probe
// (journal, "Live vendor probe", 2026-09-05): a drained account answers 402
// with message "Insufficient Balance" — but its code
// ("invalid_request_error") and type ("unknown_error") are useless for
// decoding, so the decoder keys on the status alone and preserves the body
// as facts. Anything but 402 is unrecognized: nil, and the baseline stands.
func decodeError(status int, body []byte) error {
	if status != http.StatusPaymentRequired {
		return nil
	}
	api := &claierr.APIError{StatusCode: status, Body: string(body)}
	// Best-effort fact only — never a decode key (see above).
	var probe struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		api.ProviderCode = probe.Error.Code
	}
	return claierr.NewInsufficientCredits(api)
}
