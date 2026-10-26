package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Conversations imported from other tools are exported, one file each,
// to conversations/imported/<source>/<id>.json, so the cloud conversation
// store carries them to machines where that tool never ran. There the
// imported reader lists them; on the machine that has the tool, its own
// reader wins the dedup and the export is never shown twice.
// See architecture/cloud.md.

const (
	importedDirName = "imported"
	// importedReaderName is the imported reader's Source(); its rows carry
	// the original tool's name instead.
	importedReaderName = "imported"
	// exportManifest records each export's message count, so an unchanged
	// conversation is not re-read. The dot keeps it out of every store.
	exportManifest = ".exported.json"
)

var importedSourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func importedDir(convDir string) string { return filepath.Join(convDir, importedDirName) }

// importedFileName maps a source id to a file name the stores accept: no
// separator, no leading dot.
func importedFileName(sourceID string) string {
	name := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_").Replace(sourceID)
	if strings.HasPrefix(name, ".") {
		name = "_" + name
	}
	return name + ".json"
}

func importedPath(convDir, source, sourceID string) string {
	return filepath.Join(importedDir(convDir), source, importedFileName(sourceID))
}

// ExportForeign writes every conversation the source readers discover and
// no native clone already holds, skipping the ones whose message count has
// not moved since the last export. It never fails on one conversation; it
// returns the first error after trying them all.
func ExportForeign(ctx context.Context, confDir string, cache vendors.SourceCache) error {
	convDir := conversationsDir(confDir)
	cloned := map[string]bool{}
	if rows, err := readChatIndex(convDir); err == nil {
		for _, r := range rows {
			if r.Source != "" && r.SourceID != "" {
				cloned[sourceDedupKey(r.Source, r.SourceID)] = true
			}
		}
	}
	manifestPath := filepath.Join(importedDir(convDir), exportManifest)
	manifest := map[string]int{}
	if b, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(b, &manifest)
	}
	store := convAssetStore(convDir)
	var errs []error
	changed := false
	for _, r := range allSourceReaders() {
		rows, err := r.Discover(ctx, cache)
		if err != nil {
			continue
		}
		for _, row := range rows {
			key := sourceDedupKey(row.Source, row.SourceID)
			if row.SourceID == "" || !importedSourceName.MatchString(row.Source) || cloned[key] {
				continue
			}
			p := importedPath(convDir, row.Source, row.SourceID)
			if n, ok := manifest[key]; ok && n == row.MessageCount {
				if _, err := os.Stat(p); err == nil {
					continue
				}
			}
			if err := exportOne(ctx, r, cache, store, row, p); err != nil {
				errs = append(errs, err)
				continue
			}
			manifest[key] = row.MessageCount
			changed = true
		}
	}
	if p, ok := cache.(sourcePersister); ok {
		errs = append(errs, p.Persist())
	}
	if changed {
		b, err := json.Marshal(manifest)
		if err == nil {
			err = utils.WriteFileAtomic(manifestPath, b, 0o644)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func exportOne(ctx context.Context, r vendors.SourceReader, cache vendors.SourceCache, store asset.AssetStorer, row vendors.SourceRow, p string) error {
	c, err := r.Read(ctx, cache, row.SourceID)
	if err != nil {
		return fmt.Errorf("export %s %s: %w", row.Source, row.SourceID, err)
	}
	c.Source, c.SourceID = row.Source, row.SourceID
	if c.ID == "" {
		c.ID = row.SourceID
	}
	if c.OriginDir == "" {
		c.OriginDir = row.Cwd
	}
	c, err = storePendingImages(store, c)
	if err != nil {
		return fmt.Errorf("export %s %s: %w", row.Source, row.SourceID, err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return utils.WriteFileAtomic(p, b, 0o644)
}

// importedReader lists the exported conversations as foreign rows under
// their original source, so they dedup against that source's own reader.
type importedReader struct{ convDir string }

func (importedReader) Source() string { return importedReaderName }

func (r importedReader) files() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(importedDir(r.convDir), "*", "*.json"))
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (r importedReader) Discover(_ context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error) {
	files, err := r.files()
	if err != nil {
		return nil, err
	}
	rows := make([]vendors.SourceRow, 0, len(files))
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if cache != nil {
			if row, ok := cache.Lookup(p, info); ok {
				if row.SourceID != "" {
					rows = append(rows, row)
				}
				continue
			}
		}
		row, err := importedRow(p)
		if err != nil {
			continue
		}
		if cache != nil {
			cache.Store(p, info, row)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func importedRow(p string) (vendors.SourceRow, error) {
	c, err := FromPath(p)
	if err != nil {
		return vendors.SourceRow{}, err
	}
	row := vendors.SourceRow{
		Source:       c.Source,
		SourceID:     c.SourceID,
		Created:      c.Created,
		MessageCount: len(c.Messages),
		RawPath:      p,
		Cwd:          c.OriginDir,
	}
	if msg, err := c.FirstUserMessage(); err == nil {
		row.FullFirstUserMessage = msg.Describe()
		row.FirstUserMessage = strings.Join(strings.Fields(row.FullFirstUserMessage), " ")
		if len(row.FirstUserMessage) > 100 {
			row.FirstUserMessage = row.FirstUserMessage[:100]
		}
	}
	for i := len(c.Queries) - 1; i >= 0; i-- {
		if c.Queries[i].Model != "" {
			row.Model = c.Queries[i].Model
			break
		}
	}
	return row, nil
}

// ImportedNotFoundError is a Read for an id no export holds.
type ImportedNotFoundError struct{ SourceID string }

func (e *ImportedNotFoundError) Error() string {
	return fmt.Sprintf("no imported conversation %q", e.SourceID)
}

func (r importedReader) Read(_ context.Context, _ vendors.SourceCache, sourceID string) (pub_models.Chat, error) {
	matches, err := filepath.Glob(filepath.Join(importedDir(r.convDir), "*", globEscape(importedFileName(sourceID))))
	if err != nil {
		return pub_models.Chat{}, err
	}
	for _, p := range matches {
		c, err := FromPath(p)
		if err == nil && c.SourceID == sourceID {
			return c, nil
		}
	}
	return pub_models.Chat{}, &ImportedNotFoundError{SourceID: sourceID}
}

func globEscape(s string) string {
	return strings.NewReplacer("*", "\\*", "?", "\\?", "[", "\\[").Replace(s)
}

// ReindexPulled brings the chat index up to date with the conversation
// files a cloud sync wrote or removed (paths relative to confDir).
func ReindexPulled(confDir string, written, removed []string) error {
	convDir := conversationsDir(confDir)
	var chats []pub_models.Chat
	var errs []error
	for _, rel := range written {
		c, err := FromPath(filepath.Join(confDir, filepath.FromSlash(rel)))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		chats = append(chats, c)
	}
	errs = append(errs, UpsertChatIndexBatch(convDir, chats))
	if len(removed) > 0 && !SkipIndex {
		gone := map[string]bool{}
		for _, rel := range removed {
			gone[strings.TrimSuffix(filepath.Base(rel), ".json")] = true
		}
		rows, err := readChatIndex(convDir)
		if err == nil {
			kept := rows[:0]
			for _, r := range rows {
				if !gone[r.ID] {
					kept = append(kept, r)
				}
			}
			err = writeChatIndex(convDir, kept)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// importedFiles lists every export, for the asset sweep.
func importedFiles(convDir string) ([]string, error) {
	matches, err := importedReader{convDir: convDir}.files()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return matches, err
}
