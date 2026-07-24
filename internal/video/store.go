package video

import (
	"github.com/baalimago/clai/internal/utils"
)

func SaveVideo(out Output, b64JSON, container string) (string, error) {
	return utils.SaveBase64File(out.Prefix, out.Dir, b64JSON, container)
}
