package e2etest

import (
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
)

// ErrImageGeneratorRefused is the refusal every e2e TestMain injects (I9).
var ErrImageGeneratorRefused = errors.New("real image generator is off in the test binary; opt in through UseFakeImageGenerator")

// RefusingImageGenerator never builds a generator, so no e2e row can pay for an image.
func RefusingImageGenerator(imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
	return nil, ErrImageGeneratorRefused
}

// FakeImageGenerator is the shared imagegentest.Fake, a subject of TestImageGeneratorContract.
type FakeImageGenerator = imagegentest.Fake

// UseFakeImageGenerator opts h into a fake that returns output, until the test ends.
func (h *Harness) UseFakeImageGenerator(t *testing.T, output []byte) *FakeImageGenerator {
	t.Helper()
	mime, err := asset.Sniff(output)
	if err != nil {
		t.Fatalf("fake image generator output: %v", err)
	}
	fake := &FakeImageGenerator{Images: []imagegen.GeneratedImage{{Image: asset.Image{Data: output, MIME: mime}}}}
	previous := h.Deps.NewImageGenerator
	t.Cleanup(func() { h.Deps.NewImageGenerator = previous })
	h.Deps.NewImageGenerator = fake.New
	return fake
}

// PNG returns a valid one-pixel PNG; each shade gives different bytes.
func PNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	data, err := assettest.PNG(shade)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
