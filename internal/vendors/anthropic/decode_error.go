package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/baalimago/clai/pkg/claierr"
)

// drainedAccount is the phrase anthropic puts in the message of the
// invalid_request_error it answers a drained account with.
const drainedAccount = "credit balance is too low"

// decodeError reads anthropic's error envelope and reports the one meaning the
// status baseline cannot see: a drained account arrives as a 400, which the
// baseline would call an unexpected response and so a broken request no other
// model could answer. Every other status keeps the baseline meaning.
func decodeError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(envelope.Error.Message), drainedAccount) {
		return nil
	}
	return claierr.NewInsufficientCredits(&claierr.APIError{
		StatusCode:   status,
		ProviderCode: envelope.Error.Type,
		Message:      envelope.Error.Message,
		Body:         string(body),
	})
}
