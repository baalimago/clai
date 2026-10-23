// Package assettest provides an in-memory asset.AssetStorer for tests.
package assettest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/asset"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// MemoryDir is the directory Memory.Path places every ref under; nothing exists there on disk.
const MemoryDir = "/assettest"

// Memory is a concurrency-safe in-memory asset.AssetStorer, written apart from the disk
// store so the shared contract suite cross-checks both.
type Memory struct {
	mu     sync.Mutex
	files  map[string][]byte
	exts   map[string]string
	mimes  map[string]string
	ref    *regexp.Regexp
	handle *regexp.Regexp
}

func NewMemory() *Memory {
	exts := asset.MIMEExtensions()
	m := &Memory{
		files:  map[string][]byte{},
		exts:   exts,
		mimes:  map[string]string{},
		ref:    regexp.MustCompile(`^[0-9a-f]{64}\.(` + strings.Join(slices.Sorted(maps.Values(exts)), "|") + `)$`),
		handle: regexp.MustCompile(fmt.Sprintf(`^[0-9a-f]{%d}$`, pub_models.HandleLength)),
	}
	for mime, ext := range m.exts {
		m.mimes["."+ext] = mime
	}
	return m
}

// Plant stores data under name as is, skipping Put's checks, to stage corrupt,
// colliding or foreign files.
func (m *Memory) Plant(name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = bytes.Clone(data)
}

// Names returns every stored name, planted ones included, sorted.
func (m *Memory) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(maps.Keys(m.files))
}

func (m *Memory) Put(data []byte, mime string) (string, error) {
	ext, ok := m.exts[mime]
	if !ok {
		return "", fmt.Errorf("%w: %q", asset.ErrUnsupportedMIME, mime)
	}
	sniffed, err := asset.Sniff(data)
	if err != nil {
		return "", fmt.Errorf("declared %s: %w", mime, err)
	}
	if sniffed != mime {
		return "", &asset.MIMEMismatchError{Declared: mime, Sniffed: sniffed}
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:]) + "." + ext
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, stored := m.files[ref]; !stored {
		m.files[ref] = bytes.Clone(data)
	}
	return ref, nil
}

func (m *Memory) Load(ref string) (asset.Image, error) {
	data, err := m.stored(ref)
	if err != nil {
		return asset.Image{}, err
	}
	sum := sha256.Sum256(data)
	if !strings.HasPrefix(ref, hex.EncodeToString(sum[:])+".") {
		return asset.Image{}, fmt.Errorf("%w: %s", asset.ErrAssetCorrupt, ref)
	}
	return asset.Image{Data: data, MIME: m.mimes[path.Ext(ref)]}, nil
}

func (m *Memory) Path(ref string) (string, error) {
	if _, err := m.stored(ref); err != nil {
		return "", err
	}
	return path.Join(MemoryDir, ref), nil
}

func (m *Memory) Resolve(handle string) (string, error) {
	if !m.handle.MatchString(handle) {
		return "", fmt.Errorf("%w: %q", asset.ErrHandle, handle)
	}
	var matches []string
	for _, name := range m.Names() {
		if m.ref.MatchString(name) && strings.HasPrefix(name, handle) {
			matches = append(matches, name)
		}
	}
	if len(matches) > 1 {
		return "", &asset.AmbiguousHandleError{Handle: handle, Refs: matches}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", asset.ErrUnknownHandle, handle)
	}
	return matches[0], nil
}

func (m *Memory) Sweep(live map[string]struct{}) (asset.SweepCount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed asset.SweepCount
	for name := range m.files {
		if _, keep := live[name]; keep || !m.ref.MatchString(name) {
			continue
		}
		delete(m.files, name)
		removed++
	}
	return removed, nil
}

func (m *Memory) stored(ref string) ([]byte, error) {
	if !m.ref.MatchString(ref) {
		return nil, fmt.Errorf("%w: %q", asset.ErrAssetRef, ref)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[ref]
	if !ok {
		return nil, fmt.Errorf("%w: %s", asset.ErrAssetNotFound, ref)
	}
	return bytes.Clone(data), nil
}
