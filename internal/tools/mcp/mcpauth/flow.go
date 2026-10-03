package mcpauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// redirectResult is what either the loopback listener or the paste
// fallback yields: a code and the state it was issued with, or an OAuth
// error parameter in place of a code.
type redirectResult struct {
	code  string
	state string
	err   string
}

// loopbackHTML is shown to the operator's browser after the redirect lands;
// clai itself has already captured the code by the time it renders.
const loopbackHTML = `<!DOCTYPE html><html><body>Authorization complete. You may close this window.</body></html>`

// listenLoopback binds the loopback-host and loopback-port parameters. A
// bind failure is returned so the caller falls back to the printed-URL
// path, per the spec's explicit "cannot bind" case.
func listenLoopback(host string, port int) (net.Listener, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("mcpauth: bind loopback listener on %q: %w", addr, err)
	}
	return ln, nil
}

// awaitRedirect serves exactly one request on ln and parses its query
// string into a redirectResult. It returns once that request lands, or
// when ctx is done.
func awaitRedirect(ctx context.Context, ln net.Listener) (redirectResult, error) {
	resultCh := make(chan redirectResult, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// R1-11: the handler previously resolved the wait on a request
			// to any path, so anything else reaching the ephemeral port
			// (a port scan, an unrelated browser prefetch) could do so.
			// Only the redirect URI's own path carries the authorization
			// response.
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, loopbackHTML)
			select {
			case resultCh <- redirectResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}:
			default:
			}
		}),
	}
	go srv.Serve(ln)
	defer srv.Close()

	select {
	case res := <-resultCh:
		return res, nil
	case <-ctx.Done():
		return redirectResult{}, ctx.Err()
	}
}

// defaultPrintURL is the fallback a.printURL uses when PrintURL is nil.
func defaultPrintURL() func(string) {
	return func(u string) {
		fmt.Printf("Open this URL to authorize clai, then paste the resulting code:\n%s\n", u)
	}
}
