package asset

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/utils"
)

type diskStore struct {
	dir string
}

func (s diskStore) Put(data []byte, mime string) (string, error) {
	ref, err := refFor(data, mime)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, ref)
	_, err = os.Stat(path)
	if err == nil {
		return ref, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat asset %s: %w", ref, err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("create asset dir for %s: %w", ref, err)
	}
	if err := utils.WriteFileAtomic(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write asset %s: %w", ref, err)
	}
	return ref, nil
}

func (s diskStore) Load(ref string) (Image, error) {
	f, err := parseRef(ref)
	if err != nil {
		return Image{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, ref))
	if errors.Is(err, fs.ErrNotExist) {
		return Image{}, fmt.Errorf("%w: %s", ErrAssetNotFound, ref)
	}
	if err != nil {
		return Image{}, fmt.Errorf("read asset %s: %w", ref, err)
	}
	if err := checkContent(ref, data); err != nil {
		return Image{}, err
	}
	return Image{Data: data, MIME: f.mime}, nil
}

func (s diskStore) Path(ref string) (string, error) {
	if _, err := parseRef(ref); err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(s.dir, ref))
	if err != nil {
		return "", fmt.Errorf("absolute path of asset %s: %w", ref, err)
	}
	_, err = os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", ErrAssetNotFound, ref)
	}
	if err != nil {
		return "", fmt.Errorf("stat asset %s: %w", ref, err)
	}
	return path, nil
}

func (s diskStore) Resolve(handle string) (string, error) {
	if err := checkHandle(handle); err != nil {
		return "", err
	}
	refs, err := s.refs()
	if err != nil {
		return "", err
	}
	return matchHandle(handle, refs)
}

func (s diskStore) Sweep(live map[string]struct{}) (SweepCount, error) {
	refs, err := s.refs()
	if err != nil {
		return 0, err
	}
	var removed SweepCount
	for _, ref := range refs {
		if _, ok := live[ref]; ok {
			continue
		}
		err := os.Remove(filepath.Join(s.dir, ref))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("remove asset %s: %w", ref, err)
		}
		removed++
	}
	return removed, nil
}

// refs lists the regular files named like refs, sorted; a missing dir holds none.
func (s diskStore) refs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list assets in %s: %w", s.dir, err)
	}
	var refs []string
	for _, e := range entries {
		if _, err := parseRef(e.Name()); err == nil && e.Type().IsRegular() {
			refs = append(refs, e.Name())
		}
	}
	return refs, nil
}
