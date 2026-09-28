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
func decodeError(status int, body []byte) error {
	facts := parseProviderError(body)
	if !facts.insufficientQuota() {
		return nil
	}
	api := &claierr.APIError{
		StatusCode:   status,
		ProviderCode: facts.code(),
		Message:      facts.Message,
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

type providerError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p providerError) code() string {
	if p.Code != "" {
		return p.Code
	}
	return p.Type
}

func (p providerError) insufficientQuota() bool {
	return p.Type == "insufficient_quota" ||
		p.Code == "insufficient_quota" ||
		p.Code == "credit_balance_exhausted"
}

func parseProviderError(body []byte) providerError {
	var probe struct {
		Error    *providerError `json:"error"`
		Response *struct {
			Error *providerError `json:"error"`
		} `json:"response"`
		providerError
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return providerError{}
	}
	switch {
	case probe.Error != nil:
		return *probe.Error
	case probe.Response != nil && probe.Response.Error != nil:
		return *probe.Response.Error
	case probe.Type == "error" || probe.Type == "response.error":
		return providerError{Code: probe.Code, Message: probe.Message}
	}
	return providerError{}
}
