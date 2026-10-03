package mcpauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// TokenStoreDirName is the token-store-directory parameter: the
// subdirectory of the clai config dir the token store lives under.
const TokenStoreDirName = "mcpAuth"

// TokenStoreFileMode is the token-store-mode parameter. POSIX-only, per the
// repository's existing async-tooling note; a non-POSIX platform writes
// with the platform default (os.WriteFile's mode argument is already
// best-effort there).
const TokenStoreFileMode = 0o600

// TokenEntry is the token store's on-disk record (README record formats).
// AccessToken, RefreshToken and ClientSecret are the three secret fields the
// redaction table governs; Issuer, Resource, ClientID, ExpiresAt and
// Scopes are not secret and may appear in an error or a log line.
type TokenEntry struct {
	Issuer       string    `json:"issuer"`
	Resource     string    `json:"resource,omitempty"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// TokenStore is a directory of per-server TokenEntry files, keyed by the
// hex SHA-256 of the server name (the dirscope digest-keyed convention),
// since a server name can carry filesystem-unsafe characters. It is a
// cache, not a credential source (D15): absence, unreadability or
// corruption is always a miss, never an error.
type TokenStore struct {
	dir string
}

// NewTokenStore builds a TokenStore rooted at dir. dir is required.
func NewTokenStore(dir string) *TokenStore { return &TokenStore{dir: dir} }

func (s *TokenStore) path(serverName string) string {
	sum := sha256.Sum256([]byte(serverName))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

// Path exposes the on-disk path for serverName's entry, so the auth
// subcommand can print it for the maintainer to inspect its mode.
func (s *TokenStore) Path(serverName string) string { return s.path(serverName) }

// Load returns serverName's stored entry. A missing file, an unreadable
// file, a corrupt (unparseable) entry, or an entry with no access token is
// reported as a miss (ok == false), never as an error.
func (s *TokenStore) Load(serverName string) (TokenEntry, bool) {
	data, err := os.ReadFile(s.path(serverName))
	if err != nil {
		return TokenEntry{}, false
	}
	var entry TokenEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return TokenEntry{}, false
	}
	if entry.AccessToken == "" {
		return TokenEntry{}, false
	}
	return entry, true
}

// Save writes entry for serverName with TokenStoreFileMode, atomically
// (temp file plus rename). A write failure is returned as a typed
// *TokenStoreWriteError; the caller decides how to degrade.
func (s *TokenStore) Save(serverName string, entry TokenEntry) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: s.dir, Cause: err}
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: s.path(serverName), Cause: err}
	}
	path := s.path(serverName)
	tmp, err := os.CreateTemp(s.dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: path, Cause: err}
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return &TokenStoreWriteError{ServerName: serverName, Path: path, Cause: err}
	}
	if err := tmp.Close(); err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: path, Cause: err}
	}
	if err := os.Chmod(tmpPath, TokenStoreFileMode); err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: path, Cause: err}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return &TokenStoreWriteError{ServerName: serverName, Path: path, Cause: err}
	}
	return nil
}
