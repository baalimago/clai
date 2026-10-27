package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// TrustFile is the trust store's name in the config directory. It is
// never synced: trust is a decision about this machine.
const TrustFile = "trust.json"

// CacheSubdir holds fetched profiles under the cache directory, one file
// per content hash, and the index of which target last served which hash.
const CacheSubdir = "remote"

// Digest is the lower-case hex SHA-256 of body.
func Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type trustRecord struct {
	Version int                    `json:"version"`
	Targets map[string][]trustItem `json:"targets"`
}

type trustItem struct {
	SHA256    string    `json:"sha256"`
	TrustedAt time.Time `json:"trusted_at"`
}

// TrustStore records which content hashes the user trusts per target base.
type TrustStore struct {
	path string
}

// NewTrustStore keeps trust in <configDir>/trust.json.
func NewTrustStore(configDir string) TrustStore {
	return TrustStore{path: filepath.Join(configDir, TrustFile)}
}

func (s TrustStore) load() (trustRecord, error) {
	rec := trustRecord{Version: 1, Targets: map[string][]trustItem{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, fmt.Errorf("read trust store: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, fmt.Errorf("parse trust store %s: %w", s.path, err)
	}
	if rec.Targets == nil {
		rec.Targets = map[string][]trustItem{}
	}
	return rec, nil
}

// Trusted reports whether digest is trusted for the target base.
func (s TrustStore) Trusted(base, digest string) (bool, error) {
	rec, err := s.load()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(rec.Targets[base], func(i trustItem) bool { return i.SHA256 == digest }), nil
}

// Trust records digest as trusted for the target base.
func (s TrustStore) Trust(base, digest string, now time.Time) error {
	rec, err := s.load()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(rec.Targets[base], func(i trustItem) bool { return i.SHA256 == digest }) {
		return nil
	}
	rec.Targets[base] = append(rec.Targets[base], trustItem{SHA256: digest, TrustedAt: now.UTC()})
	return writeJSON(s.path, rec)
}

// Cache keeps fetched bytes by content hash and remembers which hash each
// target last resolved to.
type Cache struct {
	dir string
}

// NewCache keeps fetched profiles in <cacheDir>/remote.
func NewCache(cacheDir string) Cache {
	return Cache{dir: filepath.Join(cacheDir, CacheSubdir)}
}

func (c Cache) indexPath() string { return filepath.Join(c.dir, "index.json") }

// Path is where the bytes with digest are kept.
func (c Cache) Path(digest string) string { return filepath.Join(c.dir, digest+".json") }

func (c Cache) index() (map[string]string, error) {
	idx := map[string]string{}
	b, err := os.ReadFile(c.indexPath())
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read remote cache index: %w", err)
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("parse remote cache index: %w", err)
	}
	return idx, nil
}

// ErrNotCached is returned by Lookup for a target the cache has not seen,
// or whose bytes no longer match their hash.
var ErrNotCached = errors.New("not in the remote cache")

// Lookup returns the bytes target last resolved to.
func (c Cache) Lookup(target string) ([]byte, string, error) {
	idx, err := c.index()
	if err != nil {
		return nil, "", err
	}
	digest, ok := idx[target]
	if !ok {
		return nil, "", fmt.Errorf("%s: %w", target, ErrNotCached)
	}
	body, err := os.ReadFile(c.Path(digest))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && Digest(body) != digest) {
		return nil, "", fmt.Errorf("%s: %w", target, ErrNotCached)
	}
	if err != nil {
		return nil, "", fmt.Errorf("read cached %s: %w", target, err)
	}
	return body, digest, nil
}

// Store writes body under its hash and points target at it.
func (c Cache) Store(target string, body []byte) (string, error) {
	digest := Digest(body)
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", fmt.Errorf("create remote cache: %w", err)
	}
	if err := writeFileAtomic(c.Path(digest), body); err != nil {
		return "", err
	}
	idx, err := c.index()
	if err != nil {
		return "", err
	}
	idx[target] = digest
	if err := writeJSON(c.indexPath(), idx); err != nil {
		return "", err
	}
	return digest, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return writeFileAtomic(path, append(b, '\n'))
}

func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
