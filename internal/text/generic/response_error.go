package generic

import (
	"errors"
	"net/http"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
)

// ResponseError decodes a provider error payload into the shared claierr
// vocabulary. The vendor decoder speaks first: when it recognizes the
// payload, its answer is the whole answer. Only a nil decode or a nil
// return falls back to the baseline — what the HTTP status alone suggests —
// and when neither speaks, to the catch-all. It never returns nil for a
// non-OK status.
//
// Exported because the anthropic and openai-responses boundaries do not go
// through this package's completer and reuse the same chain.
func ResponseError(status int, body []byte, decode func(int, []byte) error) error {
	if decode != nil {
		if err := decode(status, body); err != nil {
			return err // vendor recognized it — its answer stands alone
		}
	}
	if err := baselineError(status, body); err != nil {
		return err // status-code default for unmapped vendors
	}
	return claierr.NewUnexpectedProviderResponse(status, body) // non-OK, no meaning found — never nil
}

// baselineError maps what the HTTP status alone suggests. It carries the
// body as a fact but never parses it: body-decoded meanings are vendor
// knowledge and live in vendor decoders.
func baselineError(status int, body []byte) error {
	api := &claierr.APIError{StatusCode: status, Body: string(body)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return claierr.NewAuthFailed(api)
	case status == http.StatusPaymentRequired:
		return claierr.NewInsufficientCredits(api)
	case status == http.StatusNotFound:
		return claierr.NewModelNotFound(api)
	case status == http.StatusTooManyRequests:
		return claierr.NewRateLimited(api, time.Time{}, 0, 0)
	case status >= 500 && status <= 599:
		return claierr.NewProviderUnavailable(api)
	}
	return nil
}

// ResponseErrorWithProviderCode is the status baseline of ResponseError with
// code, the code a vendor decoder read from the payload, as its
// provider-code fact. It returns nil for an empty code, so the decoder
// leaves the payload to the baseline.
func ResponseErrorWithProviderCode(status int, body []byte, code string) error {
	if code == "" {
		return nil
	}
	err := ResponseError(status, body, nil)
	var apiErr claierr.APIErrorer
	if errors.As(err, &apiErr) {
		apiErr.API().ProviderCode = code
	}
	return err
}
