package cloudcmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"

	"github.com/baalimago/clai/internal/cloud"
)

// UnreachableError is a request that never reached clai.chat: Host is
// where it went and Reason a few words on why.
type UnreachableError struct {
	Host   string
	Reason string
	Err    error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("can't reach %s (%s)", e.Host, e.Reason)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// unreachable reads a failed request's host and cause out of err; it
// reports false for anything else, a cancelled run included.
func unreachable(err error) (*UnreachableError, bool) {
	var transport *cloud.TransportError
	var uerr *url.Error
	if !errors.As(err, &transport) || !errors.As(transport, &uerr) || errors.Is(err, context.Canceled) {
		return nil, false
	}
	host := uerr.URL
	if u, perr := url.Parse(uerr.URL); perr == nil && u.Host != "" {
		host = u.Host
	}
	return &UnreachableError{Host: host, Reason: reason(uerr.Err), Err: err}, true
}

func reason(err error) string {
	var dns *net.DNSError
	var unknownCA x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network unreachable"
	case errors.As(err, &dns) && dns.IsNotFound:
		return "no such host"
	case errors.As(err, &dns):
		return "DNS lookup failed"
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return "timed out"
	case errors.As(err, &unknownCA), errors.As(err, &hostname), errors.As(err, &invalid):
		return "its TLS certificate is not trusted"
	case errors.As(err, &record):
		return "it does not speak TLS"
	}
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// explain turns the errors a person can act on into what to do: a server
// that cannot be reached, and a key the server refused, which is fixed
// where the key came from.
func (r *runner) explain(err error) error {
	if u, ok := unreachable(err); ok {
		return u
	}
	apiErr, ok := cloud.AsAPIError(err)
	if !ok || !apiErr.Unauthorized() {
		return err
	}
	if r.creds.TokenSource == cloud.TokenFromEnv {
		return fmt.Errorf("clai.chat refused the key in CLAI_CLOUD_TOKEN (%s): set it to a current key, or unset it and run clai cloud login", apiErr.Message)
	}
	return fmt.Errorf("this device was disconnected from clai.chat (%s). Your local files are unchanged. To connect again: clai cloud login", apiErr.Message)
}
