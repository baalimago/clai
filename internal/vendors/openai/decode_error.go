package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
)

// decodeError is openai's vendor decoder (worklog 2026-09-05-error-propagation,
// D11): it answers "which shared errors does this payload mean?", building
// only through claierr constructors, and returns nil for anything it does
// not recognize so the status baseline stands alone.
//
// It reads the three error shapes openai speaks at clai's two text
// boundaries (event names and the quota code verified against current
// provider docs, D9 — citations in the phase's Implementation notes):
//
//   - the {"error": {message, type, code}} envelope of a non-OK response
//     (both APIs), which is also the error-frame envelope at HTTP 200;
//   - the top-level {"type": "error", code, message} Responses stream event;
//   - the {"type": "response.failed", "response": {"error": {code, message}}}
//     Responses stream event.
//
// Recognized signal: insufficient_quota. On a 429 the payload means BOTH
// rate-limited and likely-out-of-credits, so the decoder states the complete
// meaning set itself (the D11 obligation) with one shared facts allocation.
// Arriving on any other status — http.StatusOK marks a mid-stream frame —
// there is no throttling evidence, so it means insufficient credits alone.
func decodeError(status int, body []byte) error {
	code := providerErrorCode(body)
	if code != "insufficient_quota" {
		return nil
	}
	api := &claierr.APIError{
		StatusCode:   status,
		ProviderCode: code,
		Body:         string(body),
	}
	if status == http.StatusTooManyRequests {
		return errors.Join(
			claierr.NewRateLimited(api, time.Time{}, 0, 0),
			claierr.NewInsufficientCredits(api),
		)
	}
	return claierr.NewInsufficientCredits(api)
}

// providerErrorCode extracts openai's machine-readable error code from any
// of the wire shapes above, preferring code over type (the error-codes guide
// notes billing errors may carry insufficient_quota as the broader type).
// Unparseable or shape-less payloads yield "" — the decoder stays silent.
func providerErrorCode(body []byte) string {
	var probe struct {
		Error *struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
		Response *struct {
			Error *struct {
				Type string `json:"type"`
				Code string `json:"code"`
			} `json:"error"`
		} `json:"response"`
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	switch {
	case probe.Error != nil:
		if probe.Error.Code != "" {
			return probe.Error.Code
		}
		return probe.Error.Type
	case probe.Response != nil && probe.Response.Error != nil:
		if probe.Response.Error.Code != "" {
			return probe.Response.Error.Code
		}
		return probe.Response.Error.Type
	case probe.Type == "error" || probe.Type == "response.error":
		return probe.Code
	}
	return ""
}
