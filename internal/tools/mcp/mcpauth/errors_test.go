package mcpauth

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestErrorMessagesNameTheirServer exercises every typed error's Error()
// and, where present, Unwrap(), so each formats without panicking and
// names the server it is about.
func TestErrorMessagesNameTheirServer(t *testing.T) {
	cause := errors.New("boom")
	cases := []error{
		&ChallengeMissingResourceMetadataError{ServerName: "srv"},
		&DiscoveryError{ServerName: "srv", Stage: "protected-resource", Cause: cause},
		&MetadataMissingFieldError{ServerName: "srv", Field: "token_endpoint"},
		&MetadataURLError{ServerName: "srv", Inserted: "a", Appended: "b"},
		&RegistrationRejectedError{ServerName: "srv", Reason: "nope"},
		&RedirectError{ServerName: "srv", Reason: "access_denied"},
		&ExchangeRejectedError{ServerName: "srv", Reason: "nope"},
		&RefreshError{ServerName: "srv", Reason: "nope"},
		&CredentialSourceError{ServerName: "srv", Source: SourceTokenCommand, Cause: cause},
		&TokenStoreWriteError{ServerName: "srv", Path: "/tmp/x", Cause: cause},
	}
	for _, err := range cases {
		msg := err.Error()
		if !strings.Contains(msg, "srv") {
			t.Errorf("%T.Error() = %q, want it to name the server", err, msg)
		}
	}

	if !errors.Is(&DiscoveryError{Cause: cause}, cause) {
		t.Error("*DiscoveryError does not unwrap to its cause")
	}
	if !errors.Is(&CredentialSourceError{Cause: cause}, cause) {
		t.Error("*CredentialSourceError does not unwrap to its cause")
	}
	if !errors.Is(&TokenStoreWriteError{Cause: cause}, cause) {
		t.Error("*TokenStoreWriteError does not unwrap to its cause")
	}
}

func TestOptionsApply(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	a := NewAuthorizer(NewTokenStore(t.TempDir()), WithHTTPClient(client), WithInteractive(true))
	if a.httpclient != client {
		t.Error("WithHTTPClient did not install the client")
	}
	// Both derived clients reuse the installed transport but carry their own
	// redirect policy, so neither mutates a client the caller owns.
	if a.httpClient().Transport != client.Transport || a.strictHTTPClient().Transport != client.Transport {
		t.Error("derived clients do not reuse the installed transport")
	}
	if a.httpClient().CheckRedirect == nil || a.strictHTTPClient().CheckRedirect == nil || client.CheckRedirect != nil {
		t.Error("redirect policies not installed on copies only")
	}
	if !a.Interactive {
		t.Error("WithInteractive did not set Interactive")
	}
}

func TestDefaultPrintURLDoesNotPanic(t *testing.T) {
	defaultPrintURL()("https://example.invalid/authorize")
}
