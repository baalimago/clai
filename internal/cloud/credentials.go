package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/utils"
)

// DirName is the per-machine directory under the clai config dir that
// holds the login, the sync state and the conflict copies. It is never
// synced itself, and it does not exist until `clai cloud login`.
const DirName = "cloud"

// Credentials is one machine's login. The file is 0600.
type Credentials struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	Device  string `json:"device"`
	Account string `json:"account,omitempty"`
	// Access is read or write, as approved in the browser.
	Access    string `json:"access,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// TokenSource is where Token came from; it is never saved.
	TokenSource TokenSource `json:"-"`
}

// TokenSource is where a login's key came from.
type TokenSource int

const (
	// TokenFromFile is the key `clai cloud login` saved.
	TokenFromFile TokenSource = iota
	// TokenFromEnv is CLAI_CLOUD_TOKEN, which overrides the file.
	TokenFromEnv
)

// NotLoggedInError is any account call on a machine with no login.
type NotLoggedInError struct{}

func (e *NotLoggedInError) Error() string {
	return "not logged in to clai.chat: run 'clai cloud login'"
}

// Dir is the cloud directory under confDir.
func Dir(confDir string) string { return filepath.Join(confDir, DirName) }

func credentialsPath(confDir string) string {
	return filepath.Join(Dir(confDir), "credentials.json")
}

// LoadCredentials reads the login. CLAI_CLOUD_TOKEN and CLAI_CLOUD_URL
// stand in for (or override) the file, so a CI step can pull without one.
// A *NotLoggedInError still carries the URL to log in to.
func LoadCredentials(confDir string) (Credentials, error) {
	var c Credentials
	b, err := os.ReadFile(credentialsPath(confDir))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &c); err != nil {
			return Credentials{}, fmt.Errorf("read %s: %w", credentialsPath(confDir), err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return Credentials{}, err
	}
	if t := os.Getenv("CLAI_CLOUD_TOKEN"); t != "" {
		c.Token, c.TokenSource = t, TokenFromEnv
	}
	if u := os.Getenv("CLAI_CLOUD_URL"); u != "" {
		c.URL = u
	}
	if c.URL == "" {
		c.URL = DefaultURL
	}
	if c.Token == "" {
		return c, &NotLoggedInError{}
	}
	return c, nil
}

// SaveCredentials writes the login with mode 0600.
func SaveCredentials(confDir string, c Credentials) error {
	if err := os.MkdirAll(Dir(confDir), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	p := credentialsPath(confDir)
	if err := utils.WriteFileAtomic(p, b, 0o600); err != nil {
		return err
	}
	// WriteFileAtomic keeps an existing file's mode; a login is always 0600.
	return os.Chmod(p, 0o600)
}

// RemoveCredentials forgets the login; a missing file is not an error.
func RemoveCredentials(confDir string) error {
	err := os.Remove(credentialsPath(confDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
