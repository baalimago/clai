package mcpauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// CodeChallengeMethod is the PKCE parameter clai always advertises and
// computes (RFC 7636).
const CodeChallengeMethod = "S256"

// newPKCE generates a verifier and its S256 challenge: the challenge is the
// base64url (no padding) encoding of the SHA-256 of the verifier.
func newPKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("mcpauth: generate pkce verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// newState generates an opaque CSRF state value for the authorization
// request.
func newState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mcpauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
