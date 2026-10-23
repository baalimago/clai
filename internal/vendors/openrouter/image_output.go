package openrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
)

// DefaultImageModel is the image model generate_image uses beside an OpenRouter chat
// model when the photo model is not an OpenRouter one; route adds the "or:" prefix.
const DefaultImageModel = "google/gemini-3.1-flash-lite-image"

// imageChunk is the image subset of a streamed chunk; reasoning_details is never decoded (O6).
type imageChunk struct {
	Choices []struct {
		Delta struct {
			Images []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"delta"`
	} `json:"choices"`
}

// decodeImageChunk emits each choices[].delta.images[] data URL as an ImageEvent (O1).
func decodeImageChunk(payload []byte) ([]models.CompletionEvent, error) {
	if !bytes.Contains(payload, []byte(`"images"`)) {
		return nil, nil
	}
	var chunk imageChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, generic.UnexpectedImage(fmt.Sprintf("openrouter image chunk does not decode: %v", err))
	}
	var events []models.CompletionEvent
	for _, choice := range chunk.Choices {
		for _, image := range choice.Delta.Images {
			event, err := decodeDataURL(image.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func decodeDataURL(url string) (models.ImageEvent, error) {
	header, payload, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	mime, isBase64 := strings.CutSuffix(header, ";base64")
	if !strings.HasPrefix(url, "data:") || !ok || !isBase64 || mime == "" {
		return models.ImageEvent{}, generic.UnexpectedImage(fmt.Sprintf("openrouter image is not a base64 data URL: %.40q", url))
	}
	part, err := generic.GeneratedImage(payload, mime)
	if err != nil {
		return models.ImageEvent{}, fmt.Errorf("openrouter image: %w", err)
	}
	return models.ImageEvent{Image: part}, nil
}
