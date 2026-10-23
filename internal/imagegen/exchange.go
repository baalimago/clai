package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/baalimago/clai/pkg/claierr"
)

// NewJSONRequest builds a POST of body as JSON; the backend adds its auth header.
func NewJSONRequest(ctx context.Context, url string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode image request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build image request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// Exchange sends req and decodes a 2xx JSON reply into T. A non-2xx reply is the
// error failure decodes; a cancelled request returns the context's error, wrapped; a
// request that gets no answer is a *claierr.TransportError; an undecodable 2xx reply
// is a *claierr.UnexpectedProviderResponseError.
func Exchange[T any](client *http.Client, req *http.Request, failure func(status int, body []byte) error) (T, error) {
	var reply T
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return reply, unanswered(req, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return reply, unanswered(req, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return reply, failure(res.StatusCode, body)
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return reply, claierr.NewUnexpectedProviderResponseWithAPI(&claierr.APIError{
			StatusCode: res.StatusCode,
			Message:    fmt.Sprintf("image response is not JSON: %v", err),
		})
	}
	return reply, nil
}

func unanswered(req *http.Request, err error) error {
	if ctxErr := req.Context().Err(); ctxErr != nil {
		return fmt.Errorf("image request to %s: %w", req.URL.Host, ctxErr)
	}
	return claierr.NewTransport(err)
}
