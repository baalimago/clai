package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/baalimago/clai/internal/asset"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Persister reads and writes conversations under one clai config directory, with
// their images in one asset store.
type Persister interface {
	// Save writes chat to <confDir>/conversations/<chat.ID>.json and returns the chat
	// as written.
	//  1. Every pending ImagePart is stored with AssetStorer.Put first. In the written
	//     and the returned chat it carries its ref, with Data nil (C1, I6).
	//  2. A chat with no image is written byte-identically to the pre-worklog
	//     chat.Save, with the same index row and reasoning sidecar (I10).
	//  3. When Put fails, Save returns the error and leaves any existing chat file
	//     byte-unchanged, so no file references an unstored image.
	//  4. The GroupKey of a parts first message is computed from the concatenation of
	//     its parts' Describe(), so an image-only first message gets a key. A text-only
	//     key is unchanged (I10). The index row's FirstUserMessage is the first user
	//     message's Describe().
	//  5. The existing free functions chat.Save, chat.SaveWithoutIndex and
	//     chat.SaveAsPreviousQuery, which every current writer calls (summarize,
	//     list promote, message delete, the editor, the global-scope id rewrite and the
	//     text finalizer), behave as Save and Promote over
	//     asset.NewStore over the README asset directory under their conversations dir.
	//     No writer can put a pending image on disk or fail with ErrPendingImage (P4-2).
	Save(chat pub_models.Chat) (pub_models.Chat, error)
	// Promote persists chat as the previous query. It writes the same files today's
	// chat.SaveAsPreviousQuery writes for the same chat: a newly promoted conversation
	// when the chat has no persisted id and more than two messages, then
	// globalScope.json, then the chat's own conversation file when it has an id.
	//  1. Each file is written under Save.1–3.
	//  2. Every copy references the same asset files, and no asset is duplicated (C3).
	//  3. A text-only chat yields the same files, byte for byte, as SaveAsPreviousQuery
	//     does today (I10).
	Promote(chat pub_models.Chat) error
	// Load reads the conversation at path.
	//  1. A legacy inline image arrives from the codec as a pending ImagePart. Load
	//     checks its bytes with asset.Sniff. It fails with asset.ErrNotImage when they
	//     are not an image, and with *asset.MIMEMismatchError when the sniffed type
	//     differs from the declared one, each wrapped with the path and the message
	//     index (C2). Load never rewrites the file.
	//  2. A stored image stays a ref, and its bytes are not read.
	//  3. A codec error (pub_models.ErrLegacyImageURL, ErrUnknownPartType) is returned
	//     wrapped with the path.
	Load(path string) (pub_models.Chat, error)
	// Delete removes conversation chatID and its reasoning sidecar, as today. It then
	// sweeps every asset that no remaining conversation file references,
	// globalScope.json included (C3).
	//  1. It returns the sweep's count.
	//  2. When the sweep fails after the conversation was removed, it returns
	//     *SweepFailedError wrapping the cause. The conversation stays deleted.
	//  3. An unknown chatID fails with ErrChatNotFound, and nothing is swept.
	Delete(chatID string) (asset.SweepCount, error)
}

func NewPersister(confDir string, store asset.AssetStorer) Persister {
	return persister{confDir: confDir, convDir: conversationsDir(confDir), store: store}
}

var ErrChatNotFound = errors.New("conversation not found")

type SweepFailedError struct {
	ChatID string
	Cause  error
}

func (e *SweepFailedError) Error() string {
	return fmt.Sprintf("conversation %s was deleted, but sweeping its assets failed: %v", e.ChatID, e.Cause)
}

func (e *SweepFailedError) Unwrap() error { return e.Cause }

// sidecarNotRemovedError reports a delete that stood but left its reasoning sidecar; it is not fatal.
type sidecarNotRemovedError struct {
	ChatID string
	Cause  error
}

func (e *sidecarNotRemovedError) Error() string {
	return fmt.Sprintf("conversation %s was deleted, but removing its reasoning sidecar failed: %v", e.ChatID, e.Cause)
}

func (e *sidecarNotRemovedError) Unwrap() error { return e.Cause }

type persister struct {
	confDir string
	convDir string
	store   asset.AssetStorer
}

// convPersister is the Persister over convDir and its asset store.
func convPersister(convDir string) Persister {
	return persister{confDir: filepath.Dir(convDir), convDir: convDir, store: convAssetStore(convDir)}
}

// loadConversation reads path through the Persister of confDir (query.3).
func loadConversation(confDir, path string) (pub_models.Chat, error) {
	return NewPersister(confDir, asset.NewStore(AssetsDir(confDir))).Load(path)
}

func (p persister) Save(chat pub_models.Chat) (pub_models.Chat, error) {
	return saveChat(p.convDir, p.store, chat, withIndex)
}

func (p persister) Promote(chat pub_models.Chat) error {
	return promote(p.confDir, p.store, chat)
}

func (p persister) Load(path string) (pub_models.Chat, error) {
	chat, err := FromPath(path)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("load conversation %s: %w", path, err)
	}
	if err := checkPendingImages(chat); err != nil {
		return pub_models.Chat{}, fmt.Errorf("load conversation %s: %w", path, err)
	}
	return chat, nil
}

func (p persister) Delete(chatID string) (asset.SweepCount, error) {
	if !safeChatID(chatID) {
		return 0, fmt.Errorf("%w: invalid id %q", ErrChatNotFound, chatID)
	}
	convDir := p.convDir
	err := os.Remove(conversationPathFromDir(convDir, chatID))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("%w: %s", ErrChatNotFound, chatID)
	}
	if err != nil {
		return 0, fmt.Errorf("delete conversation %s: %w", chatID, err)
	}
	var sidecarErr error
	if err := removeReasoningSidecars(convDir, chatID); err != nil {
		sidecarErr = &sidecarNotRemovedError{ChatID: chatID, Cause: err}
	}
	live, err := liveAssetRefs(convDir)
	if err != nil {
		return 0, withSidecarFailure(&SweepFailedError{ChatID: chatID, Cause: err}, sidecarErr)
	}
	removed, err := p.store.Sweep(live)
	if err != nil {
		return removed, withSidecarFailure(&SweepFailedError{ChatID: chatID, Cause: err}, sidecarErr)
	}
	return removed, sidecarErr
}

func withSidecarFailure(sweepErr *SweepFailedError, sidecarErr error) error {
	if sidecarErr == nil {
		return sweepErr
	}
	return errors.Join(sweepErr, sidecarErr)
}

// convAssetStore is the asset store the free writers use for convDir.
func convAssetStore(convDir string) asset.AssetStorer {
	return asset.NewStore(assetsDirFromConvDir(convDir))
}

// storePendingImages returns chat with every pending image stored and replaced by
// its ref. The caller's messages and parts are never modified.
func storePendingImages(store asset.AssetStorer, chat pub_models.Chat) (pub_models.Chat, error) {
	var msgs []pub_models.Message
	for i, m := range chat.Messages {
		parts, err := storePendingParts(store, m.Parts)
		if err != nil {
			return pub_models.Chat{}, fmt.Errorf("store image of message %d: %w", i, err)
		}
		if parts == nil {
			continue
		}
		if msgs == nil {
			msgs = slices.Clone(chat.Messages)
		}
		msgs[i].Parts = parts
	}
	if msgs != nil {
		chat.Messages = msgs
	}
	return chat, nil
}

// storePendingParts returns nil when parts hold no pending image.
func storePendingParts(store asset.AssetStorer, parts []pub_models.Part) ([]pub_models.Part, error) {
	var out []pub_models.Part
	for j, part := range parts {
		img, ok := part.(pub_models.ImagePart)
		if !ok || img.Asset != "" || len(img.Data) == 0 {
			continue
		}
		ref, err := store.Put(img.Data, img.MIME)
		if err != nil {
			return nil, fmt.Errorf("content part %d: %w", j, err)
		}
		if out == nil {
			out = slices.Clone(parts)
		}
		img.Asset, img.Data = ref, nil
		out[j] = img
	}
	return out, nil
}

func checkPendingImages(chat pub_models.Chat) error {
	for i, m := range chat.Messages {
		for _, part := range m.Parts {
			img, ok := part.(pub_models.ImagePart)
			if !ok || img.Asset != "" {
				continue
			}
			sniffed, err := asset.Sniff(img.Data)
			if err != nil {
				return fmt.Errorf("message %d: %w", i, err)
			}
			if sniffed != img.MIME {
				return fmt.Errorf("message %d: %w", i, &asset.MIMEMismatchError{Declared: img.MIME, Sniffed: sniffed})
			}
		}
	}
	return nil
}

type assetRefScan struct {
	Messages []struct {
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

type assetRefPart struct {
	Type  string `json:"type"`
	Asset string `json:"asset"`
}

// liveAssetRefs collects every asset ref the conversation files directly under
// convDir reference. A file it cannot read or decode fails the scan, so the
// sweep never removes an asset that file may hold.
func liveAssetRefs(convDir string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(convDir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list conversations in %s: %w", convDir, err)
	}
	live := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if err := collectAssetRefs(filepath.Join(convDir, e.Name()), live); err != nil {
			return nil, err
		}
	}
	return live, nil
}

func collectAssetRefs(path string, live map[string]struct{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s for asset refs: %w", path, err)
	}
	var scan assetRefScan
	if err := json.Unmarshal(b, &scan); err != nil {
		return fmt.Errorf("scan %s for asset refs: %w", path, err)
	}
	for i, m := range scan.Messages {
		if !bytes.HasPrefix(bytes.TrimSpace(m.Content), []byte("[")) {
			continue
		}
		var parts []assetRefPart
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			return fmt.Errorf("scan %s message %d for asset refs: %w", path, i, err)
		}
		for _, part := range parts {
			if part.Type == "image" && part.Asset != "" {
				live[part.Asset] = struct{}{}
			}
		}
	}
	return nil
}
