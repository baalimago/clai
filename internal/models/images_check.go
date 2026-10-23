package models

import (
	"fmt"
	"slices"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// CheckImageParts enforces clauses E4 and E5 of ImageEncoder for msgs before a
// request is built, and rejects a part outside the closed Part set, so an
// encoder that ran it encodes Parts without failing.
func CheckImageParts(caps ImageCapabilities, vendor, model string, msgs []pub_models.Message) error {
	for i, m := range msgs {
		for j, p := range m.Parts {
			switch part := p.(type) {
			case pub_models.TextPart:
			case pub_models.ImagePart:
				if err := checkImage(caps, vendor, model, m.Role, part); err != nil {
					return fmt.Errorf("message %d part %d: %w", i, j, err)
				}
			default:
				return fmt.Errorf("message %d part %d: %w: %T", i, j, pub_models.ErrUnknownPartType, p)
			}
		}
	}
	return nil
}

func checkImage(caps ImageCapabilities, vendor, model, role string, p pub_models.ImagePart) error {
	if len(p.Data) == 0 {
		return ErrImageNotLoaded
	}
	refuse := func(reason string) error {
		return &claierr.ImageUnsupportedError{Vendor: vendor, Model: model, Reason: reason}
	}
	switch {
	case caps.Input == ImageInputNone:
		return refuse("route accepts no images")
	case role == "assistant":
		return refuse("image in assistant turn")
	case role == "tool" && caps.ToolResult == ToolResultFallback:
		return refuse("image in tool result")
	case !slices.Contains(caps.MIMEs, p.MIME):
		h, err := p.Handle()
		if err != nil {
			return err
		}
		return refuse(fmt.Sprintf("mime %s not accepted (image %s)", p.MIME, h))
	}
	return nil
}
