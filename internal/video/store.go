package video

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func SaveVideo(out Output, b64JSON, container string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64JSON)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	videoName := fmt.Sprintf("%v_%v.%v", out.Prefix, utils.RandomPrefix(), container)
	outFile := fmt.Sprintf("%v/%v", out.Dir, videoName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", videoName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return "", fmt.Errorf("failed to write file: %w", err)
		}
	}
	return outFile, nil
}
