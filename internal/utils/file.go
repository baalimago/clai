package utils

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

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
