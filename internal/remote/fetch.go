package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// Doer sends one HTTP request. *http.Client satisfies it; the composition
// root builds one with NewClient.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// ErrInsecureRedirect is returned when a fetch is redirected off https.
var ErrInsecureRedirect = errors.New("redirected off https")

// NewClient returns the client remote fetches use: it follows at most five
// redirects, all of them over https.
func NewClient() *http.Client {
	return &http.Client{CheckRedirect: CheckRedirect}
}

// CheckRedirect refuses a redirect that leaves https and caps the chain.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInsecureRedirect, req.URL)
	}
	if len(via) >= 5 {
		return fmt.Errorf("more than 5 redirects")
	}
	return nil
}

// NotJSONError is a response whose content type is not JSON.
type NotJSONError struct {
	Target      string
	ContentType string
}

func (e *NotJSONError) Error() string {
	return fmt.Sprintf("%s answered %s, not application/json. Not a profile", e.Target, e.ContentType)
}

func (e *NotJSONError) ExitCode() int { return ExitRefused }

// FetchError is a non-200 answer.
type FetchError struct {
	Target string
	URL    string
	Status int
}

func (e *FetchError) Error() string {
	if e.Status == http.StatusNotFound {
		return fmt.Sprintf("%s: not found (HTTP 404 from %s)", e.Target, e.URL)
	}
	return fmt.Sprintf("%s: fetch %s: HTTP %d", e.Target, e.URL, e.Status)
}

// fetch reads at most MaxBodyBytes+1 bytes of url. requireJSON refuses a
// response whose content type is not application/json or a +json type;
// raw.githubusercontent.com serves every file as text/plain, so gh:
// targets skip it and rely on Validate.
func fetch(ctx context.Context, client Doer, target, url string, requireJSON bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &FetchError{Target: target, URL: url, Status: res.StatusCode}
	}
	if requireJSON && !isJSON(res.Header.Get("Content-Type")) {
		return nil, &NotJSONError{Target: target, ContentType: res.Header.Get("Content-Type")}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return body, nil
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || (len(mediaType) > 5 && mediaType[len(mediaType)-5:] == "+json")
}
