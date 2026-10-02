package deepseek

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/baalimago/clai/pkg/claierr"
)

// decodeError maps DeepSeek's drained-account response and decodes the message
// from structured 400 errors. Other statuses or unrecognized 400 bodies defer
// to the generic status baseline.
func decodeError(status int, body []byte) error {
	switch status {
	case http.StatusPaymentRequired:
		api := &claierr.APIError{StatusCode: status, Body: string(body)}
		// Best-effort fact only — never a decode key.
		var probe struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &probe); err == nil {
			api.ProviderCode = probe.Error.Code
		}
		return claierr.NewInsufficientCredits(api)
	case http.StatusBadRequest:
		var probe struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &probe); err != nil || strings.TrimSpace(probe.Error.Message) == "" {
			return nil
		}
		code := probe.Error.Code
		if code == "" {
			code = probe.Error.Type
		}
		return claierr.NewUnexpectedProviderResponseWithAPI(&claierr.APIError{
			StatusCode:   status,
			ProviderCode: code,
			Message:      probe.Error.Message,
			Body:         string(body),
		})
	default:
		return nil
	}
}
