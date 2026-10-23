package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var ErrNotImage = asset.ErrNotImage

type ImageTooLargeError = asset.ImageTooLargeError

var errViewImagePath = errors.New("view_image: path must be a non-empty string")

type ViewImageTool pub_models.Specification

var ViewImage = ViewImageTool{
	Name:        "view_image",
	Description: "Look at a local image file (png, jpeg, gif, webp). Returns the image so you can see it.",
	Inputs: &pub_models.InputSchema{
		Type:     "object",
		Required: []string{"path"},
		Properties: map[string]pub_models.ParameterObject{
			"path": {
				Type:        "string",
				Description: "Path to the image file. '~' expands to the home directory.",
			},
		},
	},
}

var _ pub_models.PartsCaller = ViewImage

func (v ViewImageTool) Specification() pub_models.Specification {
	return pub_models.Specification(v)
}

func (v ViewImageTool) Call(in pub_models.Input) (string, error) {
	parts, err := v.CallParts(context.Background(), in)
	if err != nil {
		return "", err
	}
	return pub_models.Message{Parts: parts}.Describe(), nil
}

func (v ViewImageTool) CallParts(_ context.Context, in pub_models.Input) ([]pub_models.Part, error) {
	raw, ok := in["path"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("%w, got %T %v", errViewImagePath, in["path"], in["path"])
	}
	path, err := utils.AbsHomePath(raw)
	if err != nil {
		return nil, fmt.Errorf("view_image %s: %w", raw, err)
	}
	local, err := asset.ReadLocalImage(path)
	if err != nil {
		return nil, fmt.Errorf("view_image: %w", err)
	}
	img := pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: local.MIME, Data: local.Data}
	handle, err := img.Handle()
	if err != nil {
		return nil, fmt.Errorf("view_image %s: %w", path, err)
	}
	return []pub_models.Part{pub_models.TextPart{Text: "image " + handle + " from " + path}, img}, nil
}
