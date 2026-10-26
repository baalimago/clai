// Package cloud is the clai.chat account client: the base URL, this
// machine's CLI key, the browser-approved login and who the account is. The
// sync engine (internal/cloud/sync) builds on it, and only the cloud command
// imports either. See architecture/cloud.md.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultURL is the service clai talks to unless CLAI_CLOUD_URL or the
// login's -url says otherwise.
const DefaultURL = "https://www.clai.chat"

// APIError is any non-2xx answer: the server's {error, message} body plus
// the status.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Missing lists the blobs a commit referenced that the server lacks.
	Missing []string
	// URL is a page that fixes the error, such as Billing and usage.
	URL string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("clai.chat answered %d %s", e.Status, e.Code)
	}
	return e.Message
}

// Unauthorized is a revoked or unknown token.
func (e *APIError) Unauthorized() bool { return e.Status == http.StatusUnauthorized }

// TransportError is a request that never got an answer.
type TransportError struct {
	Op    string
	Cause error
}

func (e *TransportError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Cause) }
func (e *TransportError) Unwrap() error { return e.Cause }

// MethodChangeError is a redirect that would resend a request with another
// method, as a 301 does to a POST: followed, the server answers the wrong
// request.
type MethodChangeError struct {
	From, To, Location string
}

func (e *MethodChangeError) Error() string {
	return fmt.Sprintf("the server redirected this %s to %s, which would turn it into a %s; set CLAI_CLOUD_URL (or login -url) to the address it redirects to", e.From, e.Location, e.To)
}

func keepMethod(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if from := via[0].Method; req.Method != from {
		return &MethodChangeError{From: from, To: req.Method, Location: req.URL.String()}
	}
	return nil
}

// AsAPIError unwraps err to an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

// Client speaks clai.chat's /api/v1 and /cli/v1.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client whose timeout still fits a large blob.
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: keepMethod}}
}

// Do sends one request. A non-nil out that is an io.Writer receives the
// body as is; any other non-nil out is decoded from JSON.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return &TransportError{Op: method + " " + path, Cause: err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &TransportError{Op: method + " " + path, Cause: err}
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return decodeAPIError(res)
	}
	switch w := out.(type) {
	case nil:
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	case io.Writer:
		if _, err := io.Copy(w, res.Body); err != nil {
			return &TransportError{Op: method + " " + path, Cause: err}
		}
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return &TransportError{Op: "decode " + path, Cause: err}
	}
	return nil
}

// PostJSON sends in as JSON and decodes the answer into out.
func (c *Client) PostJSON(ctx context.Context, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.Do(ctx, http.MethodPost, path, bytes.NewReader(b), "application/json", out)
}

func decodeAPIError(res *http.Response) error {
	var body struct {
		Error   string   `json:"error"`
		Message string   `json:"message"`
		Missing []string `json:"missing"`
		URL     string   `json:"url"`
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(b, &body) != nil || body.Error == "" {
		body.Error = http.StatusText(res.StatusCode)
		body.Message = strings.TrimSpace(string(b))
	}
	return &APIError{Status: res.StatusCode, Code: body.Error, Message: body.Message, Missing: body.Missing, URL: body.URL}
}

// DeviceCode is the answer to the start of a CLI login.
type DeviceCode struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// StartDevice begins a CLI login for a client called name.
func (c *Client) StartDevice(ctx context.Context, name string) (DeviceCode, error) {
	var out DeviceCode
	err := c.PostJSON(ctx, "/cli/v1/start", map[string]string{"client": name}, &out)
	return out, err
}

// DeviceToken is an approved login: the account's CLI key.
type DeviceToken struct {
	Key string `json:"key"`
	// Access is what the approver granted: read or write.
	Access    string `json:"access"`
	ExpiresAt string `json:"expiresAt"`
	// Endpoint is the API origin the key is for.
	Endpoint string `json:"endpoint"`
	Account  string `json:"account"`
}

// PollDevice answers the key, or an *APIError whose Code is one of
// authorization_pending, slow_down, access_denied or expired_token.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (DeviceToken, error) {
	var out DeviceToken
	err := c.PostJSON(ctx, "/cli/v1/token", map[string]string{"deviceCode": deviceCode}, &out)
	return out, err
}

// StoreInfo is one store's line in /me.
type StoreInfo struct {
	Rev    int64 `json:"rev"`
	Commit int64 `json:"commit"`
	Bytes  int64 `json:"bytes"`
	Files  int   `json:"files"`
}

// Usage is the account's storage and billing standing. Config sync is free
// up to ConfigLimitBytes; conversations cost $1 per started GB a month.
type Usage struct {
	Bytes            int64 `json:"bytes"`
	ConfigLimitBytes int64 `json:"config_limit_bytes"`
	// CardOnFile is whether the account may commit conversations.
	CardOnFile bool `json:"card_on_file"`
	// Overdue is a failed payment past its retries: commits are refused.
	Overdue bool `json:"overdue"`
	// BillingURL is the page where a card is added or an invoice paid.
	BillingURL string `json:"billing_url"`
}

// Me describes the account and device behind the token.
type Me struct {
	Account string               `json:"account"`
	Handle  string               `json:"handle"`
	Device  string               `json:"device"`
	Access  string               `json:"access"`
	Usage   Usage                `json:"usage"`
	Stores  map[string]StoreInfo `json:"stores"`
}

// Name is how the account is shown: its hub handle, else its id.
func (m Me) Name() string {
	if m.Handle != "" {
		return "@" + m.Handle
	}
	return m.Account
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	var out Me
	err := c.Do(ctx, http.MethodGet, "/api/v1/me", nil, "", &out)
	return out, err
}

// RevokeCurrent signs this device out on the server.
func (c *Client) RevokeCurrent(ctx context.Context) error {
	return c.Do(ctx, http.MethodDelete, "/api/v1/tokens/current", nil, "", nil)
}
