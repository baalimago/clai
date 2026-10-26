package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
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

// scan walks the store's part of confDir and hashes every allowed regular
// file. hashes is read and refreshed in place.
func scan(confDir string, store Store, hashes map[string]hashEntry) (map[string]LocalFile, error) {
	out := map[string]LocalFile{}
	seen := map[string]bool{}
	for _, root := range roots(store) {
		abs := filepath.Join(confDir, filepath.FromSlash(root))
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
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
			if h, ok := hashes[rel]; ok && h.Size == info.Size() && h.ModNano == info.ModTime().UnixNano() {
				out[rel] = LocalFile{SHA256: h.SHA256, Size: h.Size}
				return nil
			}
			sum, err := hashFile(p)
			if err != nil {
				return err
			}
			hashes[rel] = hashEntry{Size: info.Size(), ModNano: info.ModTime().UnixNano(), SHA256: sum}
			out[rel] = LocalFile{SHA256: sum, Size: info.Size()}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", abs, err)
		}
	}
	for rel := range hashes {
		if !seen[rel] {
			delete(hashes, rel)
		}
	}
	return out, nil
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

func humanBytes(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return trimZero(float64(n)/1e9) + " GB"
	case n >= 1_000_000:
		return trimZero(float64(n)/1e6) + " MB"
	case n >= 1_000:
		return trimZero(float64(n)/1e3) + " kB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

func trimZero(f float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
}
