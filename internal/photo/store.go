package photo

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func SaveImage(out Output, b64JSON, encoding string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64JSON)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	pictureName := fmt.Sprintf("%v_%v.%v", out.Prefix, utils.RandomPrefix(), encoding)
	outFile := fmt.Sprintf("%v/%v", out.Dir, pictureName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", pictureName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return "", fmt.Errorf("failed to write file: %w", err)
		}
	}
	return outFile, nil
}
