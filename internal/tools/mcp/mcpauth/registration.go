package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ClientRegistration is what RFC 7591 dynamic client registration yields:
// a client identifier, retained with the tokens, and an optional secret.
type ClientRegistration struct {
	ClientID     string
	ClientSecret string
}

type registrationRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	GrantTypes   []string `json:"grant_types"`
	ResponseType []string `json:"response_types"`
}

// registerDynamicClient POSTs to registrationEndpoint (RFC 7591) and
// retains whichever client identity it returns. Rejection is reported with
// the server's own stated reason.
func registerDynamicClient(ctx context.Context, client *http.Client, serverName, registrationEndpoint, redirectURI string) (ClientRegistration, error) {
	body, err := json.Marshal(registrationRequest{
		RedirectURIs: []string{redirectURI},
		GrantTypes:   []string{"authorization_code", "refresh_token"},
		ResponseType: []string{"code"},
	})
	if err != nil {
		return ClientRegistration{}, fmt.Errorf("mcpauth: encode registration request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return ClientRegistration{}, fmt.Errorf("mcpauth: build registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ClientRegistration{}, &RegistrationRejectedError{ServerName: serverName, Reason: err.Error(), Cause: err}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, discoveryReadLimit))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ClientRegistration{}, &RegistrationRejectedError{ServerName: serverName, Reason: registrationRejectReason(data, resp.StatusCode)}
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return ClientRegistration{}, &RegistrationRejectedError{ServerName: serverName, Reason: fmt.Sprintf("decode response: %v", err)}
	}
	if out.ClientID == "" {
		return ClientRegistration{}, &RegistrationRejectedError{ServerName: serverName, Reason: "response carried no client_id"}
	}
	return ClientRegistration{ClientID: out.ClientID, ClientSecret: out.ClientSecret}, nil
}

func registrationRejectReason(body []byte, status int) string {
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
