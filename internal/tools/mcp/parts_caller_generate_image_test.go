package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
	imagetool "github.com/baalimago/clai/internal/imagegen/tool"
	"github.com/baalimago/clai/internal/tools/toolstest"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// generateImageSubject is phase 9's generate_image tool over a fake generator and the memory store.
func generateImageSubject(t *testing.T) toolstest.PartsSubject {
	data := contractPNG(t)
	refused := errors.New("image generation refused")
	gen := &imagegentest.Fake{
		Images: []imagegen.GeneratedImage{{Image: asset.Image{Data: data, MIME: "image/png"}}},
		Refuse: func(req imagegen.ImageRequest) error {
			if req.Prompt == "refuse" {
				return refused
			}
			return nil
		},
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:]) + ".png"
	return toolstest.PartsSubject{
		Caller:  imagetool.NewGenerateImageTool(imagegen.GeneratorConfig{Model: "gpt-image-1-mini"}, gen.New, assettest.NewMemory()),
		ImageIn: pub_models.Input{"prompt": "a fox in the snow"},
		WantParts: []pub_models.Part{
			pub_models.TextPart{Text: "generated image " + ref[:6] + " saved at " + path.Join(assettest.MemoryDir, ref)},
			pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginGenerated},
		},
		FailIn:  pub_models.Input{"prompt": "refuse"},
		FailErr: refused,
	}
}
