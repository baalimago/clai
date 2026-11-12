package e2etest

import (
	"fmt"
	"net/http"
)

// LoopbackOnly refuses any request that leaves the machine, so a misrouted
// model fails the test instead of reaching a real provider.
type LoopbackOnly struct{ Base http.RoundTripper }

func (g LoopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("refused to leave the machine: %s", r.URL)
	}
	return g.Base.RoundTrip(r)
}
