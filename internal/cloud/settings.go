package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// DirName is the per-machine directory under the clai config dir that
// holds the login, the settings and each store's sync state. It is never
// synced itself.
const DirName = "cloud"

// DefaultMinInterval is how long the background sync waits after the last
// one before another command may start it again.
const DefaultMinInterval = 60

// Credentials is one machine's login. The file is 0600.
type Credentials struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	Device  string `json:"device"`
	Account string `json:"account,omitempty"`
}

// Settings says which stores this machine syncs and how often.
type Settings struct {
	Stores             map[Store]bool `json:"stores"`
	MinIntervalSeconds int            `json:"min-interval-seconds"`
}

// Enabled reports whether this machine syncs store.
func (s Settings) Enabled(store Store) bool { return s.Stores[store] }

// NotLoggedInError is any sync or status call on a machine with no login.
type NotLoggedInError struct{}

func (e *NotLoggedInError) Error() string {
	return "not logged in to clai.chat: run 'clai cloud login'"
}

func cloudDir(confDir string) string { return filepath.Join(confDir, DirName) }

func credentialsPath(confDir string) string {
	return filepath.Join(cloudDir(confDir), "credentials.json")
}

func settingsPath(confDir string) string { return filepath.Join(cloudDir(confDir), "settings.json") }

// LoadCredentials reads the login, letting CLAI_CLOUD_TOKEN and
// CLAI_CLOUD_URL stand in for (or override) the file, so a CI job can sync
// without one.
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
		c.Token = t
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

func saveCredentials(confDir string, c Credentials) error {
	if err := os.MkdirAll(cloudDir(confDir), 0o700); err != nil {
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

func removeCredentials(confDir string) error {
	err := os.Remove(credentialsPath(confDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// LoadSettings reads the settings; a machine that never wrote any syncs
// the config store only.
func LoadSettings(confDir string) (Settings, error) {
	s := Settings{Stores: map[Store]bool{StoreConfig: true}, MinIntervalSeconds: DefaultMinInterval}
	b, err := os.ReadFile(settingsPath(confDir))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// The file's stores replace the default ones rather than merging in.
	s.Stores = nil
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("read %s: %w", settingsPath(confDir), err)
	}
	if s.Stores == nil {
		s.Stores = map[Store]bool{}
	}
	if s.MinIntervalSeconds < 0 {
		s.MinIntervalSeconds = DefaultMinInterval
	}
	return s, nil
}

func saveSettings(confDir string, s Settings) error {
	if err := os.MkdirAll(cloudDir(confDir), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return utils.WriteFileAtomic(settingsPath(confDir), b, 0o644)
}

// Off reports whether CLAI_CLOUD turns every automatic sync off.
func Off() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLAI_CLOUD"))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}
