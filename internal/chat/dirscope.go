package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// dirScopeHistoryCap bounds how many conversations a single directory binding
// remembers. The descriptor and [d]ir filter draw from this list; search uses
// origin_dir instead, so the cap is purely a recency window.
const dirScopeHistoryCap = 50

// DirScope is the directory binding record (version 2). A version 1 record (no
// abs_path, no history, Updated as an RFC3339 string) unmarshals cleanly into
// this struct and is upgraded in place on the next write.
type DirScope struct {
	Version int          `json:"version"`
	DirHash string       `json:"dir_hash,omitempty"` // sha256 filename key, self-describing
	AbsPath string       `json:"abs_path,omitempty"` // canonical dir, informational only
	ChatID  string       `json:"chat_id"`            // current binding (head)
	History []ScopedChat `json:"history,omitempty"`  // newest-first, deduped, capped
	Updated time.Time    `json:"updated"`            // typed; marshals to RFC3339
}

// ScopedChat records a single conversation's binding lifetime within a directory.
type ScopedChat struct {
	ChatID      string    `json:"chat_id"`
	FirstScoped time.Time `json:"first_scoped"` // when THIS dir first bound the chat
	LastScoped  time.Time `json:"last_scoped"`  // when THIS dir last bound the chat
}

// canonicalDir resolves dir to a stable canonical path:
// filepath.Abs -> filepath.Clean -> best-effort filepath.EvalSymlinks.
func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("abs: %w", err)
	}
	clean := filepath.Clean(abs)
	if eval, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(eval), nil
	}
	return clean, nil
}

// canonicalCWD canonicalizes the current working directory.
func canonicalCWD() (string, error) {
	wd, err := currentWorkingDirectory()
	if err != nil {
		return "", err
	}
	return canonicalDir(wd)
}

// dirHash is the hex sha256 of a canonical directory path. A cryptographic hash
// is the directory-identity guard: distinct dirs practically never collide.
func dirHash(canonicalDir string) string {
	sum := sha256.Sum256([]byte(canonicalDir))
	return hex.EncodeToString(sum[:])
}

// originMatches reports whether a conversation whose canonical origin_dir is
// origin belongs to a search anchored at canonical queryDir. With subtree it is
// inclusive of nested directories on a path boundary; otherwise it is exact.
func originMatches(origin, queryDir string, subtree bool) bool {
	if origin == "" {
		return false
	}
	if origin == queryDir {
		return true
	}
	if !subtree {
		return false
	}
	sep := string(os.PathSeparator)
	if queryDir == sep { // root matches everything
		return true
	}
	return strings.HasPrefix(origin, queryDir+sep)
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
	canonical, err := canonicalDir(dir)
	if err != nil {
		return DirScope{}, fmt.Errorf("canonicalize directory %q: %w", dir, err)
	}
	bindingPath := cq.dirScopePathFromHash(dirHash(canonical))

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

// SaveDirScope read-modify-writes the binding for dir, promoting it to version 2:
// it sets the head ChatID, refreshes abs_path + updated, upserts the chat into
// the capped newest-first history, and persists atomically (temp + rename).
func (cq *ChatHandler) SaveDirScope(dir, chatID string) error {
	canonical, err := canonicalDir(dir)
	if err != nil {
		return fmt.Errorf("canonicalize directory %q: %w", dir, err)
	}
	if err := os.MkdirAll(cq.dirscopeRoot(), 0o755); err != nil {
		return fmt.Errorf("ensure dirscope root %q: %w", cq.dirscopeRoot(), err)
	}

	binding, err := cq.loadDirScopeForDir(canonical)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load existing binding: %w", err)
	}

	now := time.Now().UTC()
	binding.Version = 2
	binding.DirHash = dirHash(canonical)
	binding.AbsPath = canonical
	binding.ChatID = chatID
	binding.History = upsertScopedHistory(binding.History, chatID, now)
	binding.Updated = now

	return cq.persistDirScope(binding)
}

// upsertScopedHistory moves chatID to the front (newest-first). On a hit it
// updates LastScoped and preserves FirstScoped; otherwise it prepends a fresh
// entry. The result is capped to dirScopeHistoryCap.
func upsertScopedHistory(history []ScopedChat, chatID string, now time.Time) []ScopedChat {
	out := make([]ScopedChat, 0, len(history)+1)
	var existing *ScopedChat
	for i := range history {
		if history[i].ChatID == chatID {
			cp := history[i]
			existing = &cp
			continue
		}
		out = append(out, history[i])
	}
	head := ScopedChat{ChatID: chatID, FirstScoped: now, LastScoped: now}
	if existing != nil {
		head.FirstScoped = existing.FirstScoped
	}
	out = append([]ScopedChat{head}, out...)
	if len(out) > dirScopeHistoryCap {
		out = out[:dirScopeHistoryCap]
	}
	return out
}

func (cq *ChatHandler) persistDirScope(binding DirScope) error {
	finalPath := cq.dirScopePathFromHash(binding.DirHash)
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), binding.DirHash+"-*.tmp")
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

// EnsureOriginDir stamps chat.OriginDir with the canonical CWD the first time the
// chat is persisted, and preserves the value on every subsequent write. If a
// conversation file for the id already carries an origin_dir, that value is
// adopted so a reply never rewrites the original origin. Stamping is always-on
// and forward-only: it has no enablement switch and never overwrites a set value.
func EnsureOriginDir(confDir string, chat *pub_models.Chat) error {
	if chat == nil || chat.OriginDir != "" {
		return nil
	}
	if chat.ID != "" && chat.ID != globalScopeChatID {
		if existing, err := FromPath(conversationPath(confDir, chat.ID)); err == nil && existing.OriginDir != "" {
			chat.OriginDir = existing.OriginDir
			return nil
		}
	}
	canonical, err := canonicalCWD()
	if err != nil {
		return fmt.Errorf("canonicalize cwd for origin stamp: %w", err)
	}
	chat.OriginDir = canonical
	return nil
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
