package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// tokenResponse is a token endpoint's successful JSON body, shared by the
// authorization-code grant and the refresh grant.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// postTokenRequest POSTs form to tokenEndpoint and decodes a successful
// response. A non-2xx status is reported with the server's own stated
// reason; the caller wraps it with the grant-specific typed error.
func postTokenRequest(ctx context.Context, client *http.Client, tokenEndpoint string, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("post to %q: %w", tokenEndpoint, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, discoveryReadLimit))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("%s", tokenRejectReason(data, resp.StatusCode))
	}
	var out tokenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return tokenResponse{}, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("token response carried no access_token")
	}
	return out, nil
}

func tokenRejectReason(body []byte, status int) string {
	var errBody struct {
		ErrorDescription string `json:"error_description"`
		Error            string `json:"error"`
	}
	if json.Unmarshal(body, &errBody) == nil && (errBody.ErrorDescription != "" || errBody.Error != "") {
		if errBody.ErrorDescription != "" {
			return errBody.ErrorDescription
		}
		return errBody.Error
	}
	return fmt.Sprintf("status %d", status)
}
