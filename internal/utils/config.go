package utils

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func createConfigDir(configDirPath string) error {
	if _, err := os.Stat(configDirPath); os.IsNotExist(err) {
		err := setupClaiConfigDir(configDirPath)
		if err != nil {
			return fmt.Errorf("failed to setup config dotdir: %w", err)
		}
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

func createDefaultConfigFile[T any](configDirPath, configFileName string, dflt *T) error {
	configFilePath := filepath.Join(configDirPath, configFileName)
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("attempting to create file: '%v'\n", configFilePath))
		}
		err := CreateFile(configFilePath, dflt)
		if err != nil {
			return fmt.Errorf("failed to write config: '%v', error: %w", configFileName, err)
		}
	}
	return nil
}

func runMigrationCallback(migrationCb func(string) error, configDirPath string) error {
	if migrationCb != nil {
		err := migrationCb(configDirPath)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to migrate for config, error: %v\n", err))
			return err
		}
	}
	return nil
}

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

	err := createConfigDir(configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	err = createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	err = runMigrationCallback(migrationCb, configDirPath)
	if err != nil {
		var nilVal T
		return nilVal, err
	}

	configPath := path.Join(configDirPath, configFileName)
	var conf T
	err = ReadAndUnmarshal(configPath, &conf)
	if err != nil {
		return conf, fmt.Errorf("failed to unmarshal config '%v', error: %v", configFileName, err)
	}

	// Append any new fields from defauly config, in case of config extension
	hasChanged := setNonZeroValueFields(&conf, dflt)

	if hasChanged {
		err = CreateFile(configPath, &conf)
		if err != nil {
			return conf, fmt.Errorf("failed to write config '%v' post zero-field appendage, error: %v", configFileName, err)
		}
		ancli.PrintOK(fmt.Sprintf("appended new fields to textConfig and updated config file: %v\n", configPath))
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found config: %+v\n", conf))
	}
	return conf, nil
}

// setNonZeroValueFields on a using b as template
func setNonZeroValueFields[T any](a, b *T) bool {
	hasChanged := false
	t := reflect.TypeOf(*a)
	for i := range t.NumField() {
		f := t.Field(i)
		aVal := reflect.ValueOf(a).Elem().FieldByName(f.Name)
		bVal := reflect.ValueOf(b).Elem().FieldByName(f.Name)
		if f.IsExported() && aVal.IsZero() && !bVal.IsZero() {
			hasChanged = true
			aVal.Set(bVal)
		}
	}
	return hasChanged
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
