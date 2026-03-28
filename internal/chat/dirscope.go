package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
	return dirscopePath(cq.confDir, hash)
}

func (cq *ChatHandler) LoadDirScope(dir string) (DirScope, error) {
	if dir == "" {
		return cq.LoadDirScopeFromCWD()
	}
	return loadDirScope(cq.confDir, dir)
}

func (cq *ChatHandler) LoadDirScopeFromCWD() (DirScope, error) {
	wd, err := currentWorkingDirectory()
	if err != nil {
		return DirScope{}, err
	}
	return loadDirScope(cq.confDir, wd)
}

func loadDirScope(confDir, dir string) (DirScope, error) {
	handler := &ChatHandler{confDir: confDir}
	return handler.loadDirScopeForDir(dir)
}

func (cq *ChatHandler) loadDirScopeForDir(dir string) (DirScope, error) {
	if dir == "" {
		return DirScope{}, fmt.Errorf("directory is empty")
	}
	canonical, err := cq.canonicalDir(dir)
	if err != nil {
		return DirScope{}, fmt.Errorf("canonicalize directory %q: %w", dir, err)
	}
	dirHash := cq.dirHash(canonical)
	bindingPath := cq.dirScopePathFromHash(dirHash)

	b, err := os.ReadFile(bindingPath)
	if err != nil {
		return DirScope{}, fmt.Errorf("read dirscope binding %q: %w", bindingPath, err)
	}

	var scope DirScope
	if err := json.Unmarshal(b, &scope); err != nil {
		return DirScope{}, fmt.Errorf("unmarshal dirscope binding %q: %w", bindingPath, err)
	}
	return scope, nil
}

func (cq *ChatHandler) dirscopeRoot() string {
	return dirscopeRoot(cq.confDir)
}

func (cq *ChatHandler) SaveDirScope(dir, chatID string) error {
	canonical, err := cq.canonicalDir(dir)
	if err != nil {
		return fmt.Errorf("canonicalize directory %q: %w", dir, err)
	}
	if _, statErr := os.Stat(cq.dirscopeRoot()); statErr != nil {
		return fmt.Errorf("stat dirscope root %q: %w", cq.dirscopeRoot(), statErr)
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
	wd, err := currentWorkingDirectory()
	if err != nil {
		return err
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

	scope, err := loadDirScopeForCurrentDir(claiConfDir)
	if err != nil {
		return "", fmt.Errorf("load dir scope: %w", err)
	}
	return scope.ChatID, nil
}

// SaveDirScopedAsPrevQuery overwrites <confDir>/conversations/globalScope.json with the
// directory-scoped conversation bound to the current working directory.
//
// This allows us to reuse the existing global "reply" plumbing (-re) while letting
// users opt into directory-scoped replies via -dre/-dir-reply.
func SaveDirScopedAsPrevQuery(confDir string) (err error) {
	scope, err := loadDirScopeForCurrentDir(confDir)
	if err != nil {
		return fmt.Errorf("load dirscope: %w", err)
	}
	if scope.ChatID == "" {
		return fmt.Errorf("no directory-scoped conversation bound to current directory")
	}

	convPath := conversationPath(confDir, scope.ChatID)
	c, err := FromPath(convPath)
	if err != nil {
		return fmt.Errorf("load conversation for chat_id %q: %w", scope.ChatID, err)
	}

	if err := SaveAsPreviousQuery(confDir, c); err != nil {
		return fmt.Errorf("save as previous query: %w", err)
	}
	return nil
}

func loadDirScopeForCurrentDir(confDir string) (DirScope, error) {
	wd, err := currentWorkingDirectory()
	if err != nil {
		return DirScope{}, err
	}
	scope, err := loadDirScope(confDir, wd)
	if err != nil {
		return DirScope{}, fmt.Errorf("load dir scope for current working directory %q: %w", wd, err)
	}
	return scope, nil
}
