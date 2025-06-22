package utils

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func CreateConfigDir(configPath string) error {
	requiredDirs := []string{"conversations", "profiles", "mcpServers"}
	for _, d := range requiredDirs {
		err := ensureDirExists(configPath, d)
		if err != nil {
			return fmt.Errorf("failed to setup config dir: %w", err)
		}
	}
	return nil
}

func ensureDirExists(configPath, toCreate string) error {
	shouldExist := path.Join(configPath, toCreate)
	if _, err := os.Stat(shouldExist); os.IsNotExist(err) {
		// Create the .clai directory.
		if err := os.MkdirAll(shouldExist, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create .clai + .clai/%v directory: %w", toCreate, err)
		}
		ancli.Okf("created directory: %v \n", shouldExist)
	}
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

	err := CreateConfigDir(configDirPath)
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

	if len(hasChanged) > 0 {
		err = CreateFile(configPath, &conf)
		if err != nil {
			return conf, fmt.Errorf("failed to write config '%v' post zero-field appendage, error: %v", configFileName, err)
		}
		ancli.PrintOK(fmt.Sprintf("appended new fields: '%s', to textConfig and updated config file: '%v'\n", hasChanged, configPath))
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found config: %v\n", debug.IndentedJsonFmt(conf)))
	}
	return conf, nil
}

// setNonZeroValueFields on a using b as template
func setNonZeroValueFields[T any](a, b *T) []string {
	hasChanged := []string{}
	t := reflect.TypeOf(*a)
	for i := range t.NumField() {
		f := t.Field(i)
		aVal := reflect.ValueOf(a).Elem().FieldByName(f.Name)
		bVal := reflect.ValueOf(b).Elem().FieldByName(f.Name)
		if f.IsExported() && aVal.IsZero() && !bVal.IsZero() {
			hasChanged = append(hasChanged, f.Tag.Get("json"))
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
