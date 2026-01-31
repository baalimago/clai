package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"
)

type DirScope struct {
	Version int    `json:"version"`
	DirHash string `json:"dir_hash"`
	ChatID  string `json:"chat_id"`
	Updated string `json:"updated"`
}

func (cq *ChatHandler) canonicalDir(dir string) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		dir = wd
	}

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

func (cq *ChatHandler) LoadDirScope(dir string) (DirScope, bool, error) {
	canonical, err := cq.canonicalDir(dir)
	if err != nil {
		return DirScope{}, false, err
	}
	h := cq.dirHash(canonical)
	p := cq.dirScopePathFromHash(h)

	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DirScope{}, false, nil
		}
		return DirScope{}, false, fmt.Errorf("read dirscope binding: %w", err)
	}

	var ds DirScope
	if err := json.Unmarshal(b, &ds); err != nil {
		return DirScope{}, false, fmt.Errorf("unmarshal dirscope binding: %w", err)
	}
	return ds, true, nil
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
