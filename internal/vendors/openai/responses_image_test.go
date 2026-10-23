package openai

import (
	"encoding/json"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestResponsesMapper_ImageContentPart_MapsToInputImage guards the vision regression:
// image content parts must map to Responses input_image items instead of erroring.
func TestResponsesMapper_ImageContentPart_MapsToInputImage(t *testing.T) {
	t.Parallel()

	const dataURL = "data:image/png;base64,AAAA"
	msg := pub_models.Message{
		Role: "user",
		Parts: []pub_models.Part{
			pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: []byte{0, 0, 0}},
			pub_models.TextPart{Text: "describe this"},
		},
	}

	parts, err := mapMessageToResponsesContent(msg)
	if err != nil {
		t.Fatalf("map content: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: got %d want 2 (%#v)", len(parts), parts)
	}
	if parts[0].Type != "input_image" {
		t.Fatalf("part[0] type: got %q want input_image", parts[0].Type)
	}
	if parts[0].ImageURL != dataURL {
		t.Fatalf("part[0] image_url: got %q want %q", parts[0].ImageURL, dataURL)
	}
	b, err := json.Marshal(parts[0])
	if err != nil {
		t.Fatalf("marshal image part: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal image part: %v", err)
	}
	if _, ok := raw["text"]; ok {
		t.Fatalf("image part must not encode text: %s", b)
	}
	if parts[1].Type != "input_text" || parts[1].Text != "describe this" {
		t.Fatalf("part[1]: got %#v", parts[1])
	}
}

// TestResponsesMapper_ImageMessage_ThroughInputItems ensures an image message maps to
// a message input item without an error, exercising the full mapping path used by
// createRequest.
func TestResponsesMapper_ImageMessage_ThroughInputItems(t *testing.T) {
	t.Parallel()

	chat := pub_models.Chat{Messages: []pub_models.Message{{
		Role:  "user",
		Parts: []pub_models.Part{pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/jpeg", Data: []byte{0, 0, 0}}},
	}}}

	items, err := mapChatToResponsesInput(chat, false)
	if err != nil {
		t.Fatalf("mapChatToResponsesInput: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items: got %d want 1", len(items))
	}
	if items[0].Type != "message" || items[0].Role != "user" {
		t.Fatalf("item: got %#v", items[0])
	}
	if len(items[0].Content) != 1 || items[0].Content[0].Type != "input_image" {
		t.Fatalf("content: got %#v", items[0].Content)
	}
	if items[0].Content[0].ImageURL != "data:image/jpeg;base64,AAAA" {
		t.Fatalf("image_url: got %q", items[0].Content[0].ImageURL)
	}
}
