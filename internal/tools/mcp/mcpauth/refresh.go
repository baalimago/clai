package mcpauth

import (
	"context"
	"net/url"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// refresh performs the refresh grant for server, single-flight per server
// name: a concurrent caller arriving while one refresh is in flight waits
// for and shares its result rather than issuing a second request. This is
// this package's own single flight, deliberately distinct from the
// connector's: a connection already resolved still needs its token
// refreshed, a separate layer from resolving the connection itself.
func (a *Authorizer) refresh(ctx context.Context, server pub_models.McpServer, entry TokenEntry) (TokenEntry, error) {
	a.refreshMu.Lock()
	call, inflight := a.inflight[server.Name]
	if !inflight {
		call = &refreshCall{done: make(chan struct{})}
		a.inflight[server.Name] = call
	}
	a.refreshMu.Unlock()

	// Test-only hook: every caller, winner or follower, signals here that
	// it has resolved its role under the lock, before the winner does any
	// real work. Nil in production.
	if a.testJoined != nil {
		a.testJoined()
	}

	if inflight {
		<-call.done
		return call.entry, call.err
	}

	// Test-only hook: lets a test hold the winner here until every
	// follower has joined above, so a concurrency assertion never races
	// the goroutine scheduler. Nil in production.
	if a.testBarrier != nil {
		<-a.testBarrier
	}

	entry2, err := a.doRefresh(ctx, server, entry)

	a.refreshMu.Lock()
	delete(a.inflight, server.Name)
	a.refreshMu.Unlock()

	call.entry, call.err = entry2, err
	close(call.done)
	return entry2, err
}

// refreshResource is the RFC 8707 `resource` a refresh grant names. An
// entry written before that parameter was sent carries none, and the MCP
// server's own endpoint is its canonical resource identifier.
func refreshResource(entry TokenEntry, server pub_models.McpServer) string {
	if entry.Resource != "" {
		return entry.Resource
	}
	return server.Url
}

func (a *Authorizer) doRefresh(ctx context.Context, server pub_models.McpServer, entry TokenEntry) (TokenEntry, error) {
	if entry.RefreshToken == "" {
		return TokenEntry{}, &RefreshError{ServerName: server.Name, Reason: "no refresh token on the stored entry"}
	}
	asMeta, err := discoverAuthorizationServer(ctx, a.httpClient(), server.Name, entry.Issuer)
	if err != nil {
		return TokenEntry{}, &RefreshError{ServerName: server.Name, Reason: err.Error(), Cause: err}
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", entry.RefreshToken)
	form.Set("client_id", entry.ClientID)
	form.Set("resource", refreshResource(entry, server))
	if entry.ClientSecret != "" {
		form.Set("client_secret", entry.ClientSecret)
	}

	tok, err := postTokenRequest(ctx, a.strictHTTPClient(), asMeta.TokenEndpoint, form)
	if err != nil {
		// The spec's "interactive run may re-authorize" is the caller's
		// decision (ResolveCached resolves a credential; it does not drive
		// a browser): a.Interactive only tells the caller whether that
		// retry is worth attempting, via AuthorizeInteractive with a
		// synthesised challenge. Here, a rejected refresh is always
		// reported.
		return TokenEntry{}, &RefreshError{ServerName: server.Name, Reason: err.Error(), Cause: err}
	}

	refreshed := TokenEntry{
		Issuer:       entry.Issuer,
		Resource:     refreshResource(entry, server),
		ClientID:     entry.ClientID,
		ClientSecret: entry.ClientSecret,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    a.clock().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       entry.Scopes,
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = entry.RefreshToken
	}
	if saveErr := a.store.Save(server.Name, refreshed); saveErr != nil {
		// R1-09: the refresh itself succeeded, so refreshed is a valid,
		// freshly issued token; only persisting it failed. Returning it
		// alongside the error lets the caller proceed with the token it
		// already holds (error-coverage row) instead of discarding it.
		return refreshed, saveErr
	}
	return refreshed, nil
}
