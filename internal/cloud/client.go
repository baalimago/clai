package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is the service clai syncs with unless CLAI_CLOUD_URL or the
// login's --url says otherwise.
const DefaultURL = "https://clai.chat"

// APIError is any non-2xx answer: the server's {error, message} body plus
// the status.
type APIError struct {
	Status  int
	Code    string
	Message string
	Missing []string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("clai.chat answered %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("clai.chat answered %d %s: %s", e.Status, e.Code, e.Message)
}

// Unauthorized is a revoked or unknown token.
func (e *APIError) Unauthorized() bool { return e.Status == http.StatusUnauthorized }

// SubscriptionRequired is a conversation store with no live subscription.
func (e *APIError) SubscriptionRequired() bool { return e.Status == http.StatusPaymentRequired }

// TransportError is a request that never got an answer.
type TransportError struct {
	Op    string
	Cause error
}

func (e *TransportError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Cause) }
func (e *TransportError) Unwrap() error { return e.Cause }

// Client speaks clai.chat's /api/v1.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client with a sane timeout; a large blob still fits.
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) do(ctx context.Context, method, p string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+p, body)
	if err != nil {
		return &TransportError{Op: method + " " + p, Cause: err}
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
		return &TransportError{Op: method + " " + p, Cause: err}
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return decodeAPIError(res)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	if w, ok := out.(io.Writer); ok {
		if _, err := io.Copy(w, res.Body); err != nil {
			return &TransportError{Op: method + " " + p, Cause: err}
		}
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return &TransportError{Op: "decode " + p, Cause: err}
	}
	return nil
}

func decodeAPIError(res *http.Response) error {
	var body struct {
		Error   string   `json:"error"`
		Message string   `json:"message"`
		Missing []string `json:"missing"`
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(b, &body) != nil || body.Error == "" {
		body.Error = http.StatusText(res.StatusCode)
		body.Message = strings.TrimSpace(string(b))
	}
	return &APIError{Status: res.StatusCode, Code: body.Error, Message: body.Message, Missing: body.Missing}
}

func (c *Client) postJSON(ctx context.Context, p string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, p, bytes.NewReader(b), "application/json", out)
}

// DeviceCode is the answer to the start of a device login.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func (c *Client) StartDevice(ctx context.Context, name string) (DeviceCode, error) {
	var out DeviceCode
	err := c.postJSON(ctx, "/api/v1/device/code", map[string]string{"name": name}, &out)
	return out, err
}

// DeviceToken is a granted login.
type DeviceToken struct {
	AccessToken string `json:"access_token"`
	DeviceName  string `json:"device_name"`
}

// PollDevice answers the token, or an *APIError whose Code is one of
// authorization_pending, slow_down, access_denied or expired_token.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (DeviceToken, error) {
	var out DeviceToken
	err := c.postJSON(ctx, "/api/v1/device/token", map[string]string{"device_code": deviceCode}, &out)
	return out, err
}

// StoreInfo is one store's line in /me.
type StoreInfo struct {
	Readable bool  `json:"readable"`
	Writable bool  `json:"writable"`
	Rev      int64 `json:"rev"`
	Bytes    int64 `json:"bytes"`
	Files    int   `json:"files"`
}

// Me describes the account behind the token.
type Me struct {
	Account       string `json:"account"`
	Device        string `json:"device"`
	Conversations struct {
		Kind     string `json:"kind"`
		RenewsAt int64  `json:"renews_at"`
		Until    int64  `json:"until"`
		DeleteAt int64  `json:"delete_at"`
	} `json:"conversations"`
	Billing bool                `json:"billing"`
	Stores  map[Store]StoreInfo `json:"stores"`
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	var out Me
	err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, "", &out)
	return out, err
}

// RevokeCurrent signs this device out on the server.
func (c *Client) RevokeCurrent(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens/current", nil, "", nil)
}

// RemoteEntry is one path's state on the server.
type RemoteEntry struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Rev     int64  `json:"rev"`
	Deleted bool   `json:"deleted"`
	Device  string `json:"device"`
}

// ChangesPage is one page of a store's history after a cursor.
type ChangesPage struct {
	Entries []RemoteEntry `json:"entries"`
	Cursor  int64         `json:"cursor"`
	More    bool          `json:"more"`
}

func (c *Client) Changes(ctx context.Context, store Store, since int64) (ChangesPage, error) {
	var out ChangesPage
	p := "/api/v1/stores/" + url.PathEscape(string(store)) + "/changes?since=" + strconv.FormatInt(since, 10)
	err := c.do(ctx, http.MethodGet, p, nil, "", &out)
	return out, err
}

// Missing returns which of hashes the server does not hold yet.
func (c *Client) Missing(ctx context.Context, hashes []string) ([]string, error) {
	var out struct {
		Missing []string `json:"missing"`
	}
	err := c.postJSON(ctx, "/api/v1/blobs/missing", map[string][]string{"hashes": hashes}, &out)
	return out.Missing, err
}

func (c *Client) PutBlob(ctx context.Context, sha string, b []byte) error {
	return c.do(ctx, http.MethodPut, "/api/v1/blobs/"+sha, bytes.NewReader(b), "application/octet-stream", nil)
}

func (c *Client) GetBlob(ctx context.Context, sha string) ([]byte, error) {
	var buf bytes.Buffer
	if err := c.do(ctx, http.MethodGet, "/api/v1/blobs/"+sha, nil, "", &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Change is one path in a commit: a new content hash, or a deletion.
type Change struct {
	Path    string `json:"path"`
	BaseRev int64  `json:"base_rev"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	Deleted bool   `json:"deleted,omitempty"`
}

// CommitResult says which changes landed and which lost to another device.
type CommitResult struct {
	Rev     int64 `json:"rev"`
	Applied []struct {
		Path string `json:"path"`
		Rev  int64  `json:"rev"`
	} `json:"applied"`
	Conflicts []struct {
		Path    string       `json:"path"`
		Current *RemoteEntry `json:"current"`
	} `json:"conflicts"`
}

func (c *Client) Commit(ctx context.Context, store Store, changes []Change) (CommitResult, error) {
	var out CommitResult
	err := c.postJSON(ctx, "/api/v1/stores/"+url.PathEscape(string(store))+"/commit", map[string][]Change{"changes": changes}, &out)
	return out, err
}

// IsAPIError unwraps err to an *APIError.
func IsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}
