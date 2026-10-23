package models

import (
	"errors"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCheckImageParts(t *testing.T) {
	png := []byte("\x89PNG fake bytes")
	native := ImageCapabilities{Input: ImageInputInline, ToolResult: ToolResultNative, MIMEs: []string{"image/png"}}
	fallback := ImageCapabilities{Input: ImageInputInline, ToolResult: ToolResultFallback, MIMEs: []string{"image/png"}}
	img := func(mime string, data []byte) pub_models.Part {
		return pub_models.ImagePart{Asset: "", MIME: mime, Origin: pub_models.OriginUser, Data: data}
	}
	msg := func(role string, parts ...pub_models.Part) []pub_models.Message {
		return []pub_models.Message{{Role: "user", Content: "first"}, {Role: role, Parts: parts}}
	}
	h, err := img("image/png", png).(pub_models.ImagePart).Handle()
	if err != nil {
		t.Fatal(err)
	}

	accepted := []struct {
		name string
		caps ImageCapabilities
		msgs []pub_models.Message
	}{
		{"text only", ImageCapabilities{}, msg("user", pub_models.TextPart{Text: "hi"})},
		{"user image", fallback, msg("user", pub_models.TextPart{Text: "[image " + h + "]"}, img("image/png", png))},
		{"native tool image", native, msg("tool", img("image/png", png))},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckImageParts(tc.caps, "v", "m", tc.msgs); err != nil {
				t.Fatalf("CheckImageParts: %v", err)
			}
		})
	}

	refused := []struct {
		name   string
		caps   ImageCapabilities
		msgs   []pub_models.Message
		reason string
	}{
		{"none route", ImageCapabilities{}, msg("user", img("image/png", png)), "route accepts no images"},
		{"assistant image", native, msg("assistant", img("image/png", png)), "image in assistant turn"},
		{"fallback tool image", fallback, msg("tool", img("image/png", png)), "image in tool result"},
		{"mime", native, msg("user", img("image/gif", png)), "mime image/gif not accepted (image " + h + ")"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckImageParts(tc.caps, "v", "m", tc.msgs)
			var iu *claierr.ImageUnsupportedError
			if !errors.As(err, &iu) {
				t.Fatalf("err = %v, want *claierr.ImageUnsupportedError", err)
			}
			if iu.Vendor != "v" || iu.Model != "m" || iu.Reason != tc.reason {
				t.Fatalf("got %+v, want reason %q", iu, tc.reason)
			}
		})
	}

	t.Run("not loaded wins over refusal", func(t *testing.T) {
		err := CheckImageParts(ImageCapabilities{}, "v", "m", msg("user", pub_models.ImagePart{Asset: h + "0000.png", MIME: "image/png"}))
		if !errors.Is(err, ErrImageNotLoaded) {
			t.Fatalf("err = %v, want ErrImageNotLoaded", err)
		}
	})
	t.Run("part outside the closed set", func(t *testing.T) {
		for _, p := range []pub_models.Part{nil, &pub_models.TextPart{Text: "x"}} {
			err := CheckImageParts(native, "v", "m", msg("user", p))
			if !errors.Is(err, pub_models.ErrUnknownPartType) {
				t.Fatalf("part %T: err = %v, want ErrUnknownPartType", p, err)
			}
		}
	})
}
