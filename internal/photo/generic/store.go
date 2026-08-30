package generic

import (
	"github.com/baalimago/clai/internal/utils"
)

func SaveImage(out Output, b64JSON, encoding string) (string, error) {
	return utils.SaveBase64File(out.Prefix, out.Dir, b64JSON, encoding)
}
