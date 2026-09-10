package utils

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func CreateFile[T any](path string, toCreate *T) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()
	b, err := json.MarshalIndent(toCreate, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if _, err := file.Write(b); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// SaveBase64File decodes a base64 string and writes it to dir/prefix_random.extension.
// Falls back to /tmp on write failure.
func SaveBase64File(prefix, dir, b64JSON, extension string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64JSON)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	fileName := fmt.Sprintf("%v_%v.%v", prefix, RandomPrefix(), extension)
	outFile := fmt.Sprintf("%v/%v", dir, fileName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", fileName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return "", fmt.Errorf("failed to write file: %w", err)
		}
	}
	return outFile, nil
}

func WriteFile[T any](path string, toWrite *T) error {
	fileBytes, err := json.MarshalIndent(toWrite, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal file: %w", err)
	}
	err = os.WriteFile(path, fileBytes, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// ReadAndUnmarshal by first finding the file, then attempting to read + unmarshal to T
func ReadAndUnmarshal[T any](filePath string, config *T) error {
	if _, err := os.Stat(filePath); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to find file: %w", err)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	err = json.Unmarshal(fileBytes, config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal file: %w", err)
	}

	return nil
}

// WriteFileAtomic writes data to path through a temp file in the same
// directory and a rename, so a concurrent reader sees the old file or the
// new file, never a prefix (worklog 2026-09-09-conversation-summaries, D31).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := createExclusiveTemp(path, perm)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file %q: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file %q: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %q to %q: %w", tmpName, path, err)
	}
	return nil
}

// createExclusiveTemp opens a fresh <base>-<rand>.tmp beside path with perm
// under the umask, like os.WriteFile.
func createExclusiveTemp(path string, perm os.FileMode) (*os.File, error) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	for range 10000 {
		name := filepath.Join(dir, base+"-"+strconv.FormatUint(rand.Uint64(), 36)+".tmp")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create temp file in %q: %w", dir, err)
		}
		return f, nil
	}
	return nil, fmt.Errorf("create temp file in %q: %w", dir, fs.ErrExist)
}
