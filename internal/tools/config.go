package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// LoadConfigFromFile if config exists. If non existent, create a new config
// using default. Run migrationCb after config has been created or fetched
func LoadConfigFromFile[T any](
	placeConfigPath,
	configFileName string,
	migrationCb func(string) error,
	dflt *T,
) (T, error) {
	configDirPath := fmt.Sprintf("%v/.clai/", placeConfigPath)
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("attempting to load file: %v%v\n", configDirPath, configFileName))
	}
	err := createWithDefault(configDirPath, configFileName, dflt)
	if err != nil {
		var nilVal T
		return nilVal, fmt.Errorf("failed to create default: %w", err)
	}
	if migrationCb != nil {
		err := migrationCb(configDirPath)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to migrate for config: '%v', error: %v\n", configFileName, err))
		}
	}
	photoConfigPath := configDirPath + configFileName
	var conf T
	err = ReadAndUnmarshal(photoConfigPath, &conf)
	if err != nil {
		return conf, fmt.Errorf("failed to unmarshal config '%v', error: %v", configFileName, err)
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found config: %+v\n", conf))
	}
	return conf, nil
}

// createWithDefault if file using defaults if it doesn't exists
func createWithDefault[T any](configDirPath string, fileName string, dfault *T) error {
	if _, err := os.Stat(configDirPath); os.IsNotExist(err) {
		err := setupClaiConfigDir(configDirPath)
		if err != nil {
			return fmt.Errorf("failed to setup config dotdir: %w", err)
		}
	}

	if _, err := os.Stat(filepath.Join(configDirPath, fileName)); os.IsNotExist(err) {
		confFile := filepath.Join(configDirPath, fileName)
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("attempting to create file: '%v'\n", confFile))
		}
		err := CreateFile(confFile, dfault)
		if err != nil {
			return fmt.Errorf("failed to write config: '%v', error: %w", fileName, err)
		}
	}
	return nil
}

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

func setupClaiConfigDir(configPath string) error {
	conversationsDir := filepath.Join(configPath, "conversations")
	ancli.PrintOK("created conversations directory\n")

	// Create the .clai directory.
	if err := os.MkdirAll(conversationsDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create .clai + .clai/conversations directory: %w", err)
	}
	ancli.PrintOK(fmt.Sprintf("created .clai directory at: '%v'\n", configPath))
	return nil
}

func ReturnNonDefault[T comparable](a, b, defaultVal T) (T, error) {
	if a != defaultVal && b != defaultVal {
		return defaultVal, fmt.Errorf("values are mutually exclusive")
	}
	if a != defaultVal {
		return a, nil
	}
	if b != defaultVal {
		return b, nil
	}
	return defaultVal, nil
}
