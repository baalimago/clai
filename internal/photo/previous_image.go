package photo

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var _ PreviousImageFunc = PreviousQueryImage

// PreviousQueryImage is the production PreviousImageFunc: the last ImagePart of
// globalScope.json, loaded through the conversation Persister. A stored part is
// read from the asset store; a pending (legacy) part gives its own bytes. No
// globalScope.json, or one without an image, is ErrNoPreviousImage (query.6).
func PreviousQueryImage(confDir string) (asset.Image, error) {
	store := asset.NewStore(chat.AssetsDir(confDir))
	prev, err := chat.NewPersister(confDir, store).Load(chat.GlobalScopePath(confDir))
	if errors.Is(err, fs.ErrNotExist) {
		return asset.Image{}, ErrNoPreviousImage
	}
	if err != nil {
		return asset.Image{}, fmt.Errorf("load the previous query: %w", err)
	}
	img, err := lastImage(prev)
	if err != nil {
		return asset.Image{}, err
	}
	if img.Asset == "" {
		return asset.Image{Data: img.Data, MIME: img.MIME}, nil
	}
	stored, err := store.Load(img.Asset)
	if err != nil {
		return asset.Image{}, fmt.Errorf("load the previous image %s: %w", img.Describe(), err)
	}
	return stored, nil
}

func lastImage(c pub_models.Chat) (pub_models.ImagePart, error) {
	for _, v := range slices.Backward(c.Messages) {
		parts := v.Parts
		for _, part := range slices.Backward(parts) {
			if img, ok := part.(pub_models.ImagePart); ok {
				return img, nil
			}
		}
	}
	return pub_models.ImagePart{}, ErrNoPreviousImage
}
