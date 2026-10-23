// Package imagegentest provides the one fake imagegen.ImageGenerator every test uses.
// It is a subject of TestImageGeneratorContract (internal/imagegen/route).
package imagegentest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
)

// Fake answers every request with Images and records each request and config.
// It keeps the ImageGenerator contract: no Images answers imagegen.ErrNoImage,
// bytes that are not an image answer asset.ErrNotImage, an image whose MIME is not
// its sniffed type answers *asset.MIMEMismatchError, and a cancelled ctx ends a call
// with ctx.Err(), wrapped.
type Fake struct {
	Images []imagegen.GeneratedImage
	// Err, when set, answers every request instead of Images.
	Err error
	// Refuse, when set, answers a request with its non-nil error instead of Images.
	Refuse func(imagegen.ImageRequest) error
	// Latency is how long each call generates; cancelling ctx ends it early.
	Latency time.Duration

	mu       sync.Mutex
	requests []imagegen.ImageRequest
	configs  []imagegen.GeneratorConfig
}

var _ imagegen.ImageGenerator = (*Fake)(nil)

// New is the fake's imagegen.NewGeneratorFunc: it records cfg and returns f.
func (f *Fake) New(cfg imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs = append(f.configs, cfg)
	return f, nil
}

func (f *Fake) Generate(ctx context.Context, req imagegen.ImageRequest) ([]imagegen.GeneratedImage, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Refuse != nil {
		if err := f.Refuse(req); err != nil {
			return nil, err
		}
	}
	return answer(f.Images)
}

// Requests returns every request the fake received, in order.
func (f *Fake) Requests() []imagegen.ImageRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Configs returns every config New was called with, in order.
func (f *Fake) Configs() []imagegen.GeneratorConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.configs)
}

func (f *Fake) wait(ctx context.Context) error {
	if f.Latency > 0 {
		timer := time.NewTimer(f.Latency)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake image generator: %w", err)
	}
	return nil
}

func answer(images []imagegen.GeneratedImage) ([]imagegen.GeneratedImage, error) {
	if len(images) == 0 {
		return nil, imagegen.ErrNoImage
	}
	out := make([]imagegen.GeneratedImage, 0, len(images))
	for i, g := range images {
		sniffed, err := asset.Sniff(g.Image.Data)
		if err != nil {
			return nil, fmt.Errorf("fake image %d: %w", i+1, err)
		}
		if sniffed != g.Image.MIME {
			return nil, &asset.MIMEMismatchError{Declared: g.Image.MIME, Sniffed: sniffed}
		}
		out = append(out, imagegen.GeneratedImage{
			Image:         asset.Image{Data: bytes.Clone(g.Image.Data), MIME: g.Image.MIME},
			RevisedPrompt: g.RevisedPrompt,
		})
	}
	return out, nil
}
