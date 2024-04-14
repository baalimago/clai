package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
