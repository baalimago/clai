package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureTrustedDedupesRecords(t *testing.T) {
	cacheDir := t.TempDir()
	mgr := &Manager{
		Config:   Config{TrustAllSkills: true},
		cacheDir: cacheDir,
	}
	skill := Skill{Name: "review", Dir: "/tmp/review", Hash: "abc", SourceClass: "default", Parsed: ParsedSkill{Metadata: Metadata{Description: "d"}}}
	if err := mgr.ensureTrusted(context.Background(), skill); err != nil {
		t.Fatalf("ensureTrusted first: %v", err)
	}
	if err := mgr.ensureTrusted(context.Background(), skill); err != nil {
		t.Fatalf("ensureTrusted second: %v", err)
	}
	cache, err := loadTrustCache(filepath.Join(cacheDir, trustFileName))
	if err != nil {
		t.Fatalf("loadTrustCache: %v", err)
	}
	if len(cache.Entries) != 1 {
		t.Fatalf("expected 1 trust record, got %d", len(cache.Entries))
	}
	if _, err := os.Stat(filepath.Join(cacheDir, trustFileName)); err != nil {
		t.Fatalf("expected trust cache file: %v", err)
	}
}
