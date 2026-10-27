package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// FileLimits is the largest single file each store takes, mirroring
// clai-cloud's FILE_LIMITS. A larger file is left out of a commit and named.
var FileLimits = map[Store]int64{
	StoreConfig:        1_000_000,
	StoreConversations: 25_000_000,
}

// LocalFile is one synced file as it is on disk now.
type LocalFile struct {
	SHA256 string
	Size   int64
}

// hashEntry lets a scan skip re-hashing a file whose size and mod time
// have not moved since the last scan.
type hashEntry struct {
	Size    int64  `json:"size"`
	ModNano int64  `json:"mod"`
	SHA256  string `json:"sha256"`
}

// conversationSubdirs are the only directories under conversations/ a
// store holds; dirs/ (per-directory scope) and anything else stay local.
var conversationSubdirs = map[string]bool{"assets": true, "reasoning": true, "imported": true}

type found struct {
	rel, abs string
	size     int64
	modNano  int64
}

// scan walks the store's part of confDir, then hashes every allowed
// regular file whose size or mod time moved since the hash in hashes,
// which it reads and refreshes in place. The walk comes first so the
// hashing reports against a known total. report, when set, hears both.
// A cancelled ctx stops it between files.
func scan(ctx context.Context, confDir string, store Store, hashes map[string]hashEntry, report func(Progress)) (map[string]LocalFile, error) {
	files, err := walk(ctx, confDir, store, report)
	if err != nil {
		return nil, err
	}
	out := make(map[string]LocalFile, len(files))
	var todo []found
	hashing := Progress{Phase: PhaseHashing}
	for _, f := range files {
		if h, ok := hashes[f.rel]; ok && h.Size == f.size && h.ModNano == f.modNano {
			out[f.rel] = LocalFile{SHA256: h.SHA256, Size: h.Size}
			hashing.Reused++
			continue
		}
		todo = append(todo, f)
		hashing.Total++
		hashing.TotalBytes += f.size
	}
	if report != nil {
		report(hashing)
	}
	for _, f := range todo {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sum, err := hashFile(f.abs)
		if err != nil {
			return nil, err
		}
		hashes[f.rel] = hashEntry{Size: f.size, ModNano: f.modNano, SHA256: sum}
		out[f.rel] = LocalFile{SHA256: sum, Size: f.size}
		hashing.Done++
		hashing.Bytes += f.size
		if report != nil {
			report(hashing)
		}
	}
	for rel := range hashes {
		if _, ok := out[rel]; !ok {
			delete(hashes, rel)
		}
	}
	return out, nil
}

func walk(ctx context.Context, confDir string, store Store, report func(Progress)) ([]found, error) {
	var files []found
	seen := map[string]bool{}
	// A config dir that is itself a link is where the user keeps it.
	if real, err := filepath.EvalSymlinks(confDir); err == nil {
		confDir = real
	}
	for _, root := range roots(store) {
		abs := filepath.Join(confDir, filepath.FromSlash(root))
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			rel, relErr := filepath.Rel(confDir, p)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if p == abs {
					return nil
				}
				if root == "." {
					return fs.SkipDir
				}
				if root == "conversations" && path.Dir(rel) == "conversations" && !conversationSubdirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || seen[rel] || !Allowed(store, rel) {
				return nil
			}
			seen[rel] = true
			info, err := d.Info()
			if err != nil {
				return err
			}
			files = append(files, found{rel: rel, abs: p, size: info.Size(), modNano: info.ModTime().UnixNano()})
			if report != nil {
				report(Progress{Phase: PhaseScanning, Done: len(files)})
			}
			return nil
		})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", abs, err)
		}
	}
	return files, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HumanBytes is a size as clai cloud prints it: "31 B", "1.5 kB", "3.0 MB".
func HumanBytes(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}
