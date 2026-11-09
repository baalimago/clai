package setup

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal/runenv"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/utils"
)

type oldPhotoConfig struct {
	Model         string `json:"model"`
	PictureDir    string `json:"photo-dir"`
	PicturePrefix string `json:"photo-prefix"`
	PromptFormat  string `json:"prompt-format"`
}

// migrateOldPhotoConfig by attempting to read and unmarshal the photoConfig.json file
// and transferring the fields which are applicable to the new photo.Configurations struct.
// Then writes the new photoConfig.json file.
func migrateOldPhotoConfig(ctx context.Context, configDirPath string) error {
	oldPhotoConfigPath := fmt.Sprintf("%v/photoConfig.json", configDirPath)
	if _, err := os.Stat(oldPhotoConfigPath); os.IsNotExist(err) {
		// Nothing to migrate, return
		return nil
	}
	var oldConf oldPhotoConfig
	err := utils.ReadAndUnmarshal(oldPhotoConfigPath, &oldConf)
	if err != nil {
		return diag.Errorf("failed to unmarshal old photo config: %w", err)
	}
	runenv.Debugf(ctx, runenv.SubsystemConfig, "oldConf: %+v", oldConf)
	if oldConf.PictureDir == "" {
		// Field is empty only if the photoConfig already has been migrated. Super hacky dodge, but good enough for now
		return nil
	}
	newFilePath := path.Join(configDirPath, "photoConfig.json")
	runenv.Noticef(ctx, runenv.SubsystemSetup, "migrating old photo config to new format saved to: '%v'\n", newFilePath)
	migratedPhotoConfig := photo.Configurations{
		Model:        oldConf.Model,
		ModelAliases: photo.DEFAULT.ModelAliases,
		PromptFormat: oldConf.PromptFormat,
		Output: photo.Output{
			Type:   photo.LOCAL,
			Dir:    oldConf.PictureDir,
			Prefix: oldConf.PicturePrefix,
		},
	}
	err = os.Remove(oldPhotoConfigPath)
	if err != nil {
		return diag.Errorf("failed to remove old photoConfig: %w", err)
	}
	err = utils.CreateFile(newFilePath, &migratedPhotoConfig)
	if err != nil {
		return diag.Errorf("failed to write new chat config: %w", err)
	}

	return nil
}
