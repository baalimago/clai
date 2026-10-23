// Package imagenorm prepares a conversation's images for a route's declared image capabilities.
package imagenorm

import (
	"fmt"
	"slices"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type Normalizer interface {
	// Normalize returns the chat to send to a route with capabilities caps. It reads
	// only caps and never names a vendor (I8).
	//  1. A chat with no ImagePart is returned deep-equal to the input (I10).
	//  2. When caps.Input is none, an image with Origin user fails with
	//     *claierr.ImageUnsupportedError{Reason:"route accepts no images"} (I4). Every
	//     other image is replaced in place by TextPart "[image <handle>]" and sent
	//     without bytes, as its string form (TL1).
	//  3. An image whose MIME type is not in caps.MIMEs fails with
	//     *claierr.ImageUnsupportedError, naming the MIME and the handle (I4).
	//  4. Every ImagePart in the output has its Data loaded: a stored image through
	//     ImageLoader.Load, a pending one as it is. Each is directly preceded by
	//     TextPart "[image <handle>]", whose handle comes from ImagePart.Handle (I1, I2).
	//  5. An image in an assistant message is removed from it and replaced in place by
	//     TextPart "[image <handle> generated, attached below]". The image is delivered
	//     in a synthetic user message placed after that assistant message and after
	//     every tool message that directly follows it (I3).
	//  6. When caps.ToolResult is fallback, a tool message's images move the same way.
	//     The tool message keeps its text plus "[image <handle> attached below]", and
	//     one synthetic user message after the last consecutive tool message carries
	//     the label and image of every moved image, in order (I3).
	//  7. When caps.ToolResult is native, tool images stay in the tool message,
	//     labelled per 4.
	//  8. A synthetic user message starts with TextPart "[images from the previous turn]".
	//  9. The input chat is not mutated.
	// 10. A Load failure is returned wrapped with the handle. Typed asset errors stay
	//     matchable with errors.Is and errors.As.
	Normalize(chat pub_models.Chat, caps models.ImageCapabilities) (pub_models.Chat, error)
}

func New(loader asset.ImageLoader) Normalizer {
	return normalizer{loader: loader}
}

type normalizer struct{ loader asset.ImageLoader }

const syntheticHeader = "[images from the previous turn]"

// noteFunc returns the text left in place of an image moved out of its message;
// nil keeps the image in place.
type noteFunc func(handle string) string

func generatedNote(h string) string { return "[image " + h + " generated, attached below]" }

func movedNote(h string) string { return "[image " + h + " attached below]" }

func labelOf(h string) pub_models.TextPart { return pub_models.TextPart{Text: "[image " + h + "]"} }

func (n normalizer) Normalize(chat pub_models.Chat, caps models.ImageCapabilities) (pub_models.Chat, error) {
	if !anyImage(chat.Messages, func(pub_models.ImagePart) bool { return true }) {
		return chat, nil
	}
	if caps.Input == models.ImageInputNone {
		return degrade(chat)
	}
	toolNote, err := checkCaps(caps)
	if err != nil {
		return pub_models.Chat{}, err
	}
	msgs, err := n.messages(chat.Messages, caps.MIMEs, toolNote)
	if err != nil {
		return pub_models.Chat{}, err
	}
	chat.Messages = msgs
	return chat, nil
}

func anyImage(msgs []pub_models.Message, match func(pub_models.ImagePart) bool) bool {
	for _, m := range msgs {
		for _, p := range m.Parts {
			if img, ok := p.(pub_models.ImagePart); ok && match(img) {
				return true
			}
		}
	}
	return false
}

// degrade replaces every image by its label for a route that accepts none (Normalize.2).
func degrade(chat pub_models.Chat) (pub_models.Chat, error) {
	if anyImage(chat.Messages, func(img pub_models.ImagePart) bool { return img.Origin == pub_models.OriginUser }) {
		return pub_models.Chat{}, &claierr.ImageUnsupportedError{Reason: "route accepts no images"}
	}
	msgs := slices.Clone(chat.Messages)
	for i, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		parts := make([]pub_models.Part, len(m.Parts))
		for j, p := range m.Parts {
			img, ok := p.(pub_models.ImagePart)
			if !ok {
				parts[j] = p
				continue
			}
			h, err := img.Handle()
			if err != nil {
				return pub_models.Chat{}, fmt.Errorf("message %d: part %d: %w", i, j, err)
			}
			parts[j] = labelOf(h)
		}
		msgs[i].Parts = parts
	}
	chat.Messages = msgs
	return chat, nil
}

// checkCaps returns how tool images travel on a route that takes images inline.
func checkCaps(caps models.ImageCapabilities) (noteFunc, error) {
	if caps.Input != models.ImageInputInline {
		return nil, fmt.Errorf("unknown image input capability %d", caps.Input)
	}
	switch caps.ToolResult {
	case models.ToolResultNative:
		return nil, nil
	case models.ToolResultFallback:
		return movedNote, nil
	default:
		return nil, fmt.Errorf("unknown tool-result image capability %d", caps.ToolResult)
	}
}

func (n normalizer) messages(in []pub_models.Message, mimes []string, toolNote noteFunc) ([]pub_models.Message, error) {
	out := make([]pub_models.Message, 0, len(in)+1)
	var moved []pub_models.Part
	for i, m := range in {
		if m.Role != "tool" && len(moved) > 0 {
			out = append(out, synthetic(moved))
			moved = nil
		}
		note := noteFunc(nil)
		switch m.Role {
		case "assistant":
			note = generatedNote
		case "tool":
			note = toolNote
		}
		if len(m.Parts) > 0 {
			kept, carried, err := n.parts(m.Parts, mimes, note)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			m.Parts = kept
			moved = append(moved, carried...)
		}
		out = append(out, m)
	}
	if len(moved) > 0 {
		out = append(out, synthetic(moved))
	}
	return out, nil
}

func synthetic(moved []pub_models.Part) pub_models.Message {
	return pub_models.Message{
		Role:  "user",
		Parts: append([]pub_models.Part{pub_models.TextPart{Text: syntheticHeader}}, moved...),
	}
}

// parts labels and loads every image in in. With a note, each image moves to
// carried and its note stays in kept.
func (n normalizer) parts(in []pub_models.Part, mimes []string, note noteFunc) (kept, carried []pub_models.Part, err error) {
	kept = make([]pub_models.Part, 0, len(in)+1)
	for j, p := range in {
		img, ok := p.(pub_models.ImagePart)
		if !ok {
			kept = append(kept, p)
			continue
		}
		h, err := img.Handle()
		if err != nil {
			return nil, nil, fmt.Errorf("part %d: %w", j, err)
		}
		img, err = n.load(img, h, mimes)
		if err != nil {
			return nil, nil, fmt.Errorf("part %d: %w", j, err)
		}
		if note == nil {
			kept = append(kept, labelOf(h), img)
			continue
		}
		kept = append(kept, pub_models.TextPart{Text: note(h)})
		carried = append(carried, labelOf(h), img)
	}
	return kept, carried, nil
}

func (n normalizer) load(img pub_models.ImagePart, h string, mimes []string) (pub_models.ImagePart, error) {
	if img.Asset != "" {
		loaded, err := n.loader.Load(img.Asset)
		if err != nil {
			return pub_models.ImagePart{}, fmt.Errorf("load image %s: %w", h, err)
		}
		img.Data, img.MIME = loaded.Data, loaded.MIME
	}
	if !slices.Contains(mimes, img.MIME) {
		return pub_models.ImagePart{}, &claierr.ImageUnsupportedError{Reason: fmt.Sprintf("mime %s not accepted (image %s)", img.MIME, h)}
	}
	return img, nil
}
