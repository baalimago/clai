package oauthtestserver

import (
	"net/http"
	"time"
)

// AutoFollowOpener is the fake browser opener this worklog's phase 5
// introduces (README code-layout table: "the injectable that opens an
// authorization URL... the fake in internal/tools/mcp/oauthtestserver").
// Open issues a real GET against the URL, following redirects, so a fake
// authorization server's own /authorize handler drives a loopback redirect
// exactly as an operator's browser would. It satisfies any BrowserOpener
// interface shaped like Open(string) error without importing one.
type AutoFollowOpener struct {
	client *http.Client
}

// NewAutoFollowOpener builds an AutoFollowOpener with a bounded client.
func NewAutoFollowOpener() *AutoFollowOpener {
	return &AutoFollowOpener{client: &http.Client{Timeout: 5 * time.Second}}
}

// Open drives url in the background and never blocks the caller: the
// loopback listener the real flow is waiting on is what actually observes
// completion.
func (o *AutoFollowOpener) Open(url string) error {
	go func() {
		resp, err := o.client.Get(url)
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

// FailingOpener always reports it cannot open a browser, driving a test
// down the printed-URL fallback.
type FailingOpener struct{ Reason string }

// Open always fails.
func (f FailingOpener) Open(string) error {
	reason := f.Reason
	if reason == "" {
		reason = "no display"
	}
	return &browserUnavailableError{reason: reason}
}

type browserUnavailableError struct{ reason string }

func (e *browserUnavailableError) Error() string { return e.reason }
