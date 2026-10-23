package vendors

import (
	"bytes"
	"encoding/base64"
	"encoding/json"

	"github.com/baalimago/clai/internal/asset"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ImageSource is how a foreign image block carries its bytes.
type ImageSource int

const (
	// ImageSourceInline is base64 inside the block.
	ImageSourceInline ImageSource = iota
	// ImageSourceRemote is a URL or any other reference that is not fetched.
	ImageSourceRemote
)

// ImageBlock is one foreign {"type":"image"} block, before import.
type ImageBlock struct {
	Source ImageSource
	MIME   string
	Base64 string
}

// ImageBlockFunc reads a vendor's {"type":"image"} block.
type ImageBlockFunc func(block map[string]any) ImageBlock

const (
	reasonRemote     = "url source"
	reasonBadBase64  = "bad base64"
	reasonNotImage   = "not an image"
	reasonMIMEDiffer = "mime mismatch"
)

// ContentMessage maps content, a string or an array of blocks, to one message
// of role. Content with an image block carries Parts with text and image blocks
// in source order; any other content keeps the text-only mapping.
func ContentMessage(role string, content any, origin pub_models.ImageOrigin, image ImageBlockFunc) pub_models.Message {
	if parts := imageBlockParts(content, origin, image); parts != nil {
		return pub_models.Message{Role: role, Parts: parts}
	}
	return pub_models.Message{Role: role, Content: TextBlocksContent(content)}
}

// RawUserPreview is RawTextBlocksContent, except that a message with image
// blocks and no text previews as its Describe.
func RawUserPreview(raw json.RawMessage, image ImageBlockFunc) string {
	if text := RawTextBlocksContent(raw); text != "" || !bytes.Contains(raw, []byte(`"image"`)) {
		return text
	}
	var content any
	if err := json.Unmarshal(raw, &content); err != nil {
		return ""
	}
	return ContentMessage("user", content, pub_models.OriginUser, image).Describe()
}

// HasContent reports whether m carries text or parts.
func HasContent(m pub_models.Message) bool {
	return m.Content != "" || len(m.Parts) > 0
}

func imageBlockParts(content any, origin pub_models.ImageOrigin, image ImageBlockFunc) []pub_models.Part {
	arr, ok := content.([]any)
	if !ok {
		return nil
	}
	parts := make([]pub_models.Part, 0, len(arr))
	images := 0
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch typ, _ := m["type"].(string); typ {
		case "text":
			if t, _ := m["text"].(string); t != "" {
				parts = append(parts, pub_models.TextPart{Text: t})
			}
		case "image":
			images++
			parts = append(parts, importImage(image(m), origin))
		}
	}
	if images == 0 {
		return nil
	}
	return parts
}

// importImage never fails: one damaged image must not make a conversation
// unreadable, and the note keeps the loss visible.
func importImage(b ImageBlock, origin pub_models.ImageOrigin) pub_models.Part {
	if b.Source != ImageSourceInline {
		return notImported(reasonRemote)
	}
	data, err := base64.StdEncoding.DecodeString(b.Base64)
	if err != nil {
		return notImported(reasonBadBase64)
	}
	sniffed, err := asset.Sniff(data)
	if err != nil {
		return notImported(reasonNotImage)
	}
	if sniffed != b.MIME {
		return notImported(reasonMIMEDiffer)
	}
	return pub_models.ImagePart{Origin: origin, MIME: b.MIME, Data: data}
}

func notImported(reason string) pub_models.Part {
	return pub_models.TextPart{Text: "[image not imported: " + reason + "]"}
}
