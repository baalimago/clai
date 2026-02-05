package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

type DirScope struct {
	Version int    `json:"version"`
	DirHash string `json:"dir_hash"`
	ChatID  string `json:"chat_id"`
	Updated string `json:"updated"`
}

func (cq *ChatHandler) canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("abs: %w", err)
	}
	clean := filepath.Clean(abs)

	// Best-effort EvalSymlinks; fall back to clean path.
	if eval, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(eval), nil
	}
	return clean, nil
}

func (cq *ChatHandler) dirHash(canonicalDir string) string {
	sum := sha256.Sum256([]byte(canonicalDir))
	return hex.EncodeToString(sum[:])
}

func (cq *ChatHandler) dirScopePathFromHash(hash string) string {
	return filepath.Join(cq.dirscopeRoot(), hash+".json")
}

func (cq *ChatHandler) LoadDirScope(dir string) (DirScope, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return DirScope{}, fmt.Errorf("getwd: %w", err)
		}
		dir = wd
	}
	canonical, err := cq.canonicalDir(dir)
	if err != nil {
		return DirScope{}, err
	}
	h := cq.dirHash(canonical)
	p := cq.dirScopePathFromHash(h)

	b, err := os.ReadFile(p)
	if err != nil {
		return DirScope{}, fmt.Errorf("read dirscope binding: %w", err)
	}

	var ds DirScope
	if err := json.Unmarshal(b, &ds); err != nil {
		return DirScope{}, fmt.Errorf("unmarshal dirscope binding: %w", err)
	}
	return ds, nil
}

func (cq *ChatHandler) dirscopeRoot() string {
	return path.Join(cq.confDir, "conversations", "dirs")
}

func (cq *ChatHandler) SaveDirScope(dir, chatID string) error {
	canonical, err := cq.canonicalDir(dir)
	if err != nil {
		return fmt.Errorf("failed to get canonicalDir: %w", err)
	}
	if _, existsErr := os.Stat(cq.dirscopeRoot()); existsErr != nil {
		return fmt.Errorf("dir: '%v' does not exist: %w", cq.dirscopeRoot(), err)
	}
	h := cq.dirHash(canonical)
	binding := DirScope{
		Version: 1,
		DirHash: h,
		ChatID:  chatID,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}

	finalPath := cq.dirScopePathFromHash(h)
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), h+"-*.tmp")
	if err != nil {
		return fmt.Errorf("CreateTemp: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(binding); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode binding: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	if err := os.Rename(tmp.Name(), finalPath); err != nil {
		return fmt.Errorf("rename tmp to final: %w", err)
	}
	return nil
}

// UpdateDirScopeFromCWD binds the current working directory to the provided chatID.
// It is used after non-reply interactions (e.g. query) to keep the directory-scoped
// pointer up to date.
func (cq *ChatHandler) UpdateDirScopeFromCWD(chatID string) error {
	if chatID == "" {
		return fmt.Errorf("empty chatID")
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if err := cq.SaveDirScope(wd, chatID); err != nil {
		return fmt.Errorf("save dirscope binding: %w", err)
	}
	return nil
}

// UpdateDirScopeFromCWD binds the current working directory to the provided chatID.
// This is a convenience wrapper for the internal package (which can not access
// ChatHandler's unexported fields).
func UpdateDirScopeFromCWD(confDir, chatID string) error {
	if chatID == "" {
		return fmt.Errorf("empty chatID")
	}
	cq := &ChatHandler{confDir: confDir}
	return cq.UpdateDirScopeFromCWD(chatID)
}

// LoadDirScopeChatID loads the bound chat id for the current working directory.
func LoadDirScopeChatID(claiConfDir string) (string, error) {
	if claiConfDir == "" {
		var err error
		claiConfDir, err = utils.GetClaiConfigDir()
		if err != nil {
			return "", fmt.Errorf("get clai config dir: %w", err)
		}
	}

	cq := &ChatHandler{
		confDir: claiConfDir,
		convDir: path.Join(claiConfDir, "conversations"),
	}
	ds, err := cq.LoadDirScope("")
	if err != nil {
		return "", fmt.Errorf("load dir scope: %w", err)
	}
	return ds.ChatID, nil
}
