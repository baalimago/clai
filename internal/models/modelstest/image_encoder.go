// Package modelstest holds the route-neutral cases of the ImageEncoder contract, so that
// TestImageEncoderContract (internal/vendors) and every fake ImageEncoder run the same checks.
// The wire cases, which only an encoder that builds a request body can break, stay in that suite.
package modelstest

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// EncoderSubject is one ImageEncoder under the route-neutral cases.
type EncoderSubject struct {
	Vendor string
	Model  string
	// Caps is the subject's declared capabilities row.
	Caps models.ImageCapabilities
	// Image is a stored and loaded image/png with Origin user.
	Image pub_models.ImagePart
	// New returns a configured encoder on which Setup has not run.
	New func() models.ImageEncoder
	// Send streams chat through a fresh encoder and reports what it observed.
	Send func(t *testing.T, chat pub_models.Chat) Exchange
}

// Exchange is what one Send observed.
type Exchange struct {
	Err      error
	Requests int
}

// RunEncoderCases runs Capabilities (ImageCapabilities.1), NotLoaded (E4) and Refused
// (E5, I3, I4) over s, each as a subtest.
func RunEncoderCases(t *testing.T, s EncoderSubject) {
	t.Helper()
	if s.Image.Asset == "" || len(s.Image.Data) == 0 || s.Image.MIME != "image/png" {
		t.Fatalf("subject image = {Asset %q, %d bytes, MIME %q}, want a stored and loaded image/png", s.Image.Asset, len(s.Image.Data), s.Image.MIME)
	}
	t.Run("Capabilities", func(t *testing.T) {
		got := s.New().ImageCapabilities()
		gotMIMEs, wantMIMEs := slices.Sorted(slices.Values(got.MIMEs)), slices.Sorted(slices.Values(s.Caps.MIMEs))
		if got.Input != s.Caps.Input || got.ToolResult != s.Caps.ToolResult || !slices.Equal(gotMIMEs, wantMIMEs) {
			t.Fatalf("ImageCapabilities() = %+v, want %+v", got, s.Caps)
		}
	})
	t.Run("NotLoaded", func(t *testing.T) {
		unloaded := s.Image
		unloaded.Data = nil
		res := s.Send(t, UserImageChat(unloaded))
		if !errors.Is(res.Err, models.ErrImageNotLoaded) {
			t.Fatalf("err = %v, want ErrImageNotLoaded", res.Err)
		}
		if res.Requests != 0 {
			t.Fatalf("requests = %d, want 0", res.Requests)
		}
	})
	t.Run("Refused", func(t *testing.T) {
		for _, row := range refusedRows(t, s) {
			t.Run(row.name, func(t *testing.T) {
				res := s.Send(t, row.chat)
				var iu *claierr.ImageUnsupportedError
				if !errors.As(res.Err, &iu) {
					t.Fatalf("err = %v, want *claierr.ImageUnsupportedError", res.Err)
				}
				if iu.Vendor != s.Vendor || iu.Model != s.Model || iu.Reason != row.reason {
					t.Fatalf("got %+v, want vendor %q model %q reason %q", iu, s.Vendor, s.Model, row.reason)
				}
				if res.Requests != 0 {
					t.Fatalf("requests = %d, want 0", res.Requests)
				}
			})
		}
	})
}

type refusedRow struct {
	name   string
	chat   pub_models.Chat
	reason string
}

func refusedRows(t *testing.T, s EncoderSubject) []refusedRow {
	t.Helper()
	if s.Caps.Input == models.ImageInputNone {
		return []refusedRow{{"NoneRoute", UserImageChat(s.Image), "route accepts no images"}}
	}
	rows := []refusedRow{{"AssistantImage", AssistantImageChat(s.Image), "image in assistant turn"}}
	if s.Caps.ToolResult == models.ToolResultFallback {
		rows = append(rows, refusedRow{"FallbackToolImage", ToolImageChat(s.Image), "image in tool result"})
	}
	disallowed := "image/bmp"
	for _, m := range slices.Sorted(maps.Keys(asset.MIMEExtensions())) {
		if !slices.Contains(s.Caps.MIMEs, m) {
			disallowed = m
			break
		}
	}
	handle, err := s.Image.Handle()
	if err != nil {
		t.Fatalf("subject image handle: %v", err)
	}
	img := s.Image
	img.MIME = disallowed
	reason := "mime " + disallowed + " not accepted (image " + handle + ")"
	return append(rows, refusedRow{"MIME", UserImageChat(img), reason})
}

// UserImageChat is a system message, then a user message [TextPart "[image ab12cd]", img, TextPart "what?"].
func UserImageChat(img pub_models.ImagePart) pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "Describe images."},
		{Role: "user", Parts: []pub_models.Part{pub_models.TextPart{Text: "[image ab12cd]"}, img, pub_models.TextPart{Text: "what?"}}},
	}}
}

// ToolImageChat ends on a view_image tool result [TextPart "loaded", img with Origin tool].
func ToolImageChat(img pub_models.ImagePart) pub_models.Chat {
	img.Origin = pub_models.OriginTool
	return pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "show me"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{
			ID: "call_1", Name: "view_image", Type: "function", Inputs: &pub_models.Input{"path": "a.png"},
			Function: pub_models.Specification{Name: "view_image", Arguments: `{"path":"a.png"}`},
		}}},
		{Role: "tool", ToolCallID: "call_1", Parts: []pub_models.Part{pub_models.TextPart{Text: "loaded"}, img}},
	}}
}

// AssistantImageChat holds img, with Origin generated, in an assistant message.
func AssistantImageChat(img pub_models.ImagePart) pub_models.Chat {
	img.Origin = pub_models.OriginGenerated
	return pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "draw"},
		{Role: "assistant", Parts: []pub_models.Part{pub_models.TextPart{Text: "here"}, img}},
		{Role: "user", Content: "nice"},
	}}
}
