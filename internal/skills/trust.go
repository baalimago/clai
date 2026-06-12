package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (m *Manager) ensureTrusted(ctx context.Context, skill Skill) error {
	cachePath := filepath.Join(m.cacheDir, trustFileName)
	cache, err := loadTrustCache(cachePath)
	if err != nil {
		return err
	}
	key := trustKey(skill.Dir, skill.Hash)
	if _, ok := cache.Entries[key]; ok {
		return nil
	}
	if !m.Config.TrustAllSkills {
		if m.trustPrompter == nil {
			return fmt.Errorf("skill %q is untrusted", skill.Name)
		}
		ok, err := m.trustPrompter(ctx, TrustPrompt{
			Name:        skill.Name,
			SourceClass: skill.SourceClass,
			Path:        skill.Dir,
			Hash:        skill.Hash,
			Description: skill.Parsed.Metadata.Description,
		})
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("skill %q was not trusted", skill.Name)
		}
	}
	if cache.Entries == nil {
		cache.Entries = map[string]TrustRecord{}
	}
	cache.Entries[key] = TrustRecord{
		Path:        skill.Dir,
		Hash:        skill.Hash,
		TrustedAt:   time.Now().UTC(),
		SourceClass: skill.SourceClass,
	}
	return writeJSONFile(cachePath, cache)
}

func loadTrustCache(path string) (trustCache, error) {
	var cache trustCache
	err := readJSON(path, &cache)
	if errors.Is(err, os.ErrNotExist) {
		return trustCache{Entries: map[string]TrustRecord{}}, nil
	}
	if err != nil {
		return trustCache{}, err
	}
	if cache.Entries == nil {
		cache.Entries = map[string]TrustRecord{}
	}
	return cache, nil
}

func trustKey(path, hash string) string {
	return path + "|" + hash
}
