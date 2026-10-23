package imagenorm_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type subject struct {
	norm  imagenorm.Normalizer
	store asset.AssetStorer
	// plant stores data under name, bypassing Put, to stage a corrupt asset.
	plant func(t *testing.T, name string, data []byte)
}

func forEachSubject(t *testing.T, run func(t *testing.T, s subject)) {
	t.Helper()
	subjects := []struct {
		name string
		new  func(t *testing.T) subject
	}{
		{"New/memory", func(*testing.T) subject {
			m := assettest.NewMemory()
			return subject{
				norm:  imagenorm.New(m),
				store: m,
				plant: func(_ *testing.T, name string, data []byte) { m.Plant(name, data) },
			}
		}},
		{"New/disk", func(t *testing.T) subject {
			dir := filepath.Join(t.TempDir(), "assets")
			store := asset.NewStore(dir)
			return subject{
				norm:  imagenorm.New(store),
				store: store,
				plant: func(t *testing.T, name string, data []byte) {
					t.Helper()
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatalf("create %s: %v", dir, err)
					}
					if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
						t.Fatalf("plant %s: %v", name, err)
					}
				},
			}
		}},
	}
	for _, sub := range subjects {
		t.Run(sub.name, func(t *testing.T) { run(t, sub.new(t)) })
	}
}

func allMIMEs() []string { return []string{"image/png", "image/jpeg", "image/gif", "image/webp"} }

func fallbackCaps() models.ImageCapabilities {
	return models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: models.ToolResultFallback, MIMEs: allMIMEs()}
}

func nativeCaps() models.ImageCapabilities {
	return models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: models.ToolResultNative, MIMEs: allMIMEs()}
}

func pngData(seed byte) []byte  { return append([]byte("\x89PNG\r\n\x1a\n"), seed) }
func jpegData(seed byte) []byte { return append([]byte("\xff\xd8\xff\xe0"), seed) }
func gifData(seed byte) []byte  { return append([]byte("GIF89a"), seed) }

func stored(t *testing.T, s subject, data []byte, mime string, origin pub_models.ImageOrigin) pub_models.ImagePart {
	t.Helper()
	ref, err := s.store.Put(data, mime)
	if err != nil {
		t.Fatalf("Put(%s): %v", mime, err)
	}
	return pub_models.ImagePart{Asset: ref, MIME: mime, Origin: origin}
}

func pending(data []byte, mime string, origin pub_models.ImageOrigin) pub_models.ImagePart {
	return pub_models.ImagePart{MIME: mime, Origin: origin, Data: data}
}

func withData(p pub_models.ImagePart, data []byte) pub_models.ImagePart {
	p.Data = data
	return p
}

func handle(t *testing.T, p pub_models.ImagePart) string {
	t.Helper()
	h, err := p.Handle()
	if err != nil {
		t.Fatalf("Handle(): %v", err)
	}
	return h
}

func text(s string) pub_models.TextPart { return pub_models.TextPart{Text: s} }

func label(t *testing.T, p pub_models.ImagePart) pub_models.TextPart {
	t.Helper()
	return text("[image " + handle(t, p) + "]")
}

func synthetic(parts ...pub_models.Part) pub_models.Message {
	return pub_models.Message{Role: "user", Parts: append([]pub_models.Part{text("[images from the previous turn]")}, parts...)}
}

func msg(role string, parts ...pub_models.Part) pub_models.Message {
	return pub_models.Message{Role: role, Parts: parts}
}

func toolCalls(ids ...string) []pub_models.Call {
	calls := make([]pub_models.Call, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, pub_models.Call{ID: id, Name: "probe", Type: "function", Function: pub_models.Specification{Name: "probe"}})
	}
	return calls
}

func toolMsg(id string, parts ...pub_models.Part) pub_models.Message {
	return pub_models.Message{Role: "tool", ToolCallID: id, Parts: parts}
}

func chatOf(msgs ...pub_models.Message) pub_models.Chat {
	return pub_models.Chat{ID: "c1", Created: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Messages: msgs}
}

func normalize(t *testing.T, s subject, chat pub_models.Chat, caps models.ImageCapabilities) pub_models.Chat {
	t.Helper()
	out, err := s.norm.Normalize(chat, caps)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return out
}

func wantMessages(t *testing.T, got, want []pub_models.Message) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	t.Fatalf("messages differ\n got: %s\nwant: %s", outline(got), outline(want))
}

func outline(msgs []pub_models.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n  %s calls=%d id=%q content=%q [", m.Role, len(m.ToolCalls), m.ToolCallID, m.Content)
		for _, p := range m.Parts {
			switch part := p.(type) {
			case pub_models.ImagePart:
				fmt.Fprintf(&b, " img(%s asset=%t data=%d mime=%s origin=%s)", part.Describe(), part.Asset != "", len(part.Data), part.MIME, part.Origin)
			default:
				fmt.Fprintf(&b, " %q", p.Describe())
			}
		}
		b.WriteString(" ]")
	}
	return b.String()
}

// assertSendable checks I2 and I3 on a normalized chat: every image is loaded and
// directly follows its label, and no assistant message holds an image.
func assertSendable(t *testing.T, msgs []pub_models.Message, toolImages models.ToolResultImages) {
	t.Helper()
	for i, m := range msgs {
		for j, p := range m.Parts {
			img, ok := p.(pub_models.ImagePart)
			if !ok {
				continue
			}
			if m.Role == "assistant" || (m.Role == "tool" && toolImages == models.ToolResultFallback) {
				t.Fatalf("message %d (%s) part %d: image left in a role the route cannot take", i, m.Role, j)
			}
			if len(img.Data) == 0 {
				t.Fatalf("message %d part %d: image has no Data", i, j)
			}
			if j == 0 || !reflect.DeepEqual(m.Parts[j-1], label(t, img)) {
				t.Fatalf("message %d part %d: image is not directly preceded by its label", i, j)
			}
		}
	}
}

// Proves Normalize.1, I10, AC1.
func TestNormalize_TextOnlyIdentity(t *testing.T) {
	chats := map[string]func() pub_models.Chat{
		"nil messages": func() pub_models.Chat { return pub_models.Chat{ID: "empty"} },
		"content only": func() pub_models.Chat {
			c := chatOf(
				pub_models.Message{Role: "system", Content: "be brief"},
				pub_models.Message{Role: "user", Content: "hi"},
				pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a", "b")},
				pub_models.Message{Role: "tool", ToolCallID: "a", Content: "one"},
				pub_models.Message{Role: "tool", ToolCallID: "b", Content: "two"},
				pub_models.Message{Role: "assistant", Content: "done", ReasoningContent: "thought"},
			)
			c.Profile = "p"
			c.TokenUsage = &pub_models.Usage{TotalTokens: 3}
			c.Queries = []pub_models.QueryCost{{CostUSD: 0.5}}
			return c
		},
		"text parts": func() pub_models.Chat {
			return chatOf(
				msg("user", text("a"), text("b")),
				pub_models.Message{Role: "assistant", ToolCalls: toolCalls("x"), Parts: []pub_models.Part{text("calling")}},
				toolMsg("x", text("result")),
			)
		},
	}
	caps := []models.ImageCapabilities{
		{},
		{Input: models.ImageInputNone, ToolResult: models.ToolResultNative},
		fallbackCaps(),
		nativeCaps(),
		{Input: models.ImageInputInline, ToolResult: models.ToolResultFallback, MIMEs: []string{"image/png"}},
		{Input: models.ImageInput(9), ToolResult: models.ToolResultImages(9)},
	}
	forEachSubject(t, func(t *testing.T, s subject) {
		for name, build := range chats {
			for _, c := range caps {
				out, err := s.norm.Normalize(build(), c)
				if err != nil {
					t.Fatalf("%s %+v: Normalize: %v", name, c, err)
				}
				if !reflect.DeepEqual(out, build()) {
					t.Fatalf("%s %+v: output is not deep-equal to the input\n got: %s", name, c, outline(out.Messages))
				}
			}
		}
	})
}

// Proves Normalize.4, I1, I2, AC2.
func TestNormalize_LabelsAndLoads(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		png := pngData(1)
		st := stored(t, s, png, "image/png", pub_models.OriginUser)
		pe := pending(jpegData(2), "image/jpeg", pub_models.OriginUser)
		in := chatOf(
			pub_models.Message{Role: "system", Content: "sys"},
			msg("user", text("look"), st, pe),
		)
		out := normalize(t, s, in, nativeCaps())
		wantMessages(t, out.Messages, []pub_models.Message{
			{Role: "system", Content: "sys"},
			msg("user", text("look"), label(t, st), withData(st, png), label(t, pe), pe),
		})
		assertSendable(t, out.Messages, models.ToolResultNative)
		if label(t, st).Text != "[image "+hexSum(png)[:6]+"]" {
			t.Fatalf("stored label %q does not carry the sha256 handle of its bytes", label(t, st).Text)
		}
		if out.ID != in.ID || !out.Created.Equal(in.Created) {
			t.Fatalf("chat metadata changed: %q %v", out.ID, out.Created)
		}
	})
}

// Proves Normalize.5, Normalize.8, I3, AC3.
func TestNormalize_AssistantRelocated(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		data := pngData(3)
		gen := stored(t, s, data, "image/png", pub_models.OriginGenerated)
		h := handle(t, gen)
		in := chatOf(
			msg("user", text("draw")),
			msg("assistant", text("here"), gen),
			msg("user", text("again")),
		)
		for _, caps := range []models.ImageCapabilities{fallbackCaps(), nativeCaps()} {
			out := normalize(t, s, in, caps)
			wantMessages(t, out.Messages, []pub_models.Message{
				msg("user", text("draw")),
				msg("assistant", text("here"), text("[image "+h+" generated, attached below]")),
				synthetic(label(t, gen), withData(gen, data)),
				msg("user", text("again")),
			})
			assertSendable(t, out.Messages, caps.ToolResult)
		}
	})
}

// Proves Normalize.5, I3, AC3.
func TestNormalize_AssistantBeforeTools(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		data := pngData(4)
		gen := pending(data, "image/png", pub_models.OriginGenerated)
		h := handle(t, gen)
		in := chatOf(
			pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a", "b"), Parts: []pub_models.Part{gen}},
			toolMsg("a", text("one")),
			toolMsg("b", text("two")),
			msg("user", text("next")),
		)
		for _, caps := range []models.ImageCapabilities{fallbackCaps(), nativeCaps()} {
			out := normalize(t, s, in, caps)
			wantMessages(t, out.Messages, []pub_models.Message{
				{Role: "assistant", ToolCalls: toolCalls("a", "b"), Parts: []pub_models.Part{text("[image " + h + " generated, attached below]")}},
				toolMsg("a", text("one")),
				toolMsg("b", text("two")),
				synthetic(label(t, gen), gen),
				msg("user", text("next")),
			})
			assertSendable(t, out.Messages, caps.ToolResult)
		}
	})
}

// Proves Normalize.6, Normalize.8, I3, AC4.
func TestNormalize_ToolFallback(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		d1, d2, d3 := pngData(5), jpegData(6), pngData(7)
		img1 := stored(t, s, d1, "image/png", pub_models.OriginTool)
		img2 := pending(d2, "image/jpeg", pub_models.OriginTool)
		gen := stored(t, s, d3, "image/png", pub_models.OriginGenerated)
		h1, h2, hg := handle(t, img1), handle(t, img2), handle(t, gen)
		tool1 := toolMsg("a", text("first"), img1)
		tool2 := toolMsg("b", text("second"), img2)
		wantTool1 := toolMsg("a", text("first"), text("[image "+h1+" attached below]"))
		wantTool2 := toolMsg("b", text("second"), text("[image "+h2+" attached below]"))
		calls := pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a", "b")}

		t.Run("block before a user message", func(t *testing.T) {
			out := normalize(t, s, chatOf(calls, tool1, tool2, msg("user", text("next"))), fallbackCaps())
			wantMessages(t, out.Messages, []pub_models.Message{
				calls, wantTool1, wantTool2,
				synthetic(label(t, img1), withData(img1, d1), label(t, img2), img2),
				msg("user", text("next")),
			})
			assertSendable(t, out.Messages, models.ToolResultFallback)
		})
		t.Run("block ends the chat", func(t *testing.T) {
			out := normalize(t, s, chatOf(calls, tool1, tool2), fallbackCaps())
			wantMessages(t, out.Messages, []pub_models.Message{
				calls, wantTool1, wantTool2,
				synthetic(label(t, img1), withData(img1, d1), label(t, img2), img2),
			})
		})
		t.Run("assistant and tool images share one message", func(t *testing.T) {
			withGen := pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a", "b"), Parts: []pub_models.Part{gen}}
			out := normalize(t, s, chatOf(withGen, tool1, tool2), fallbackCaps())
			wantMessages(t, out.Messages, []pub_models.Message{
				{Role: "assistant", ToolCalls: toolCalls("a", "b"), Parts: []pub_models.Part{text("[image " + hg + " generated, attached below]")}},
				wantTool1, wantTool2,
				synthetic(label(t, gen), withData(gen, d3), label(t, img1), withData(img1, d1), label(t, img2), img2),
			})
			assertSendable(t, out.Messages, models.ToolResultFallback)
		})
	})
}

// Proves Normalize.7, AC4.
func TestNormalize_ToolNative(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		data := pngData(8)
		img := stored(t, s, data, "image/png", pub_models.OriginTool)
		calls := pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a")}
		out := normalize(t, s, chatOf(calls, toolMsg("a", text("result"), img), msg("user", text("next"))), nativeCaps())
		wantMessages(t, out.Messages, []pub_models.Message{
			calls,
			toolMsg("a", text("result"), label(t, img), withData(img, data)),
			msg("user", text("next")),
		})
		assertSendable(t, out.Messages, models.ToolResultNative)
	})
}

// Proves Normalize.2, Normalize.3, I4, AC5.
func TestNormalize_Refusals(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		png := stored(t, s, pngData(9), "image/png", pub_models.OriginUser)
		gifStored := stored(t, s, gifData(10), "image/gif", pub_models.OriginTool)
		gifPending := pending(gifData(11), "image/gif", pub_models.OriginUser)
		genPending := pending(pngData(21), "image/png", pub_models.OriginGenerated)
		absentUser := pub_models.ImagePart{Asset: hexSum(pngData(22)) + ".png", MIME: "image/png", Origin: pub_models.OriginUser}
		pngJPEG := models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: models.ToolResultNative, MIMEs: []string{"image/png", "image/jpeg"}}
		cases := []struct {
			name   string
			chat   pub_models.Chat
			caps   models.ImageCapabilities
			reason string
		}{
			{"none, user image", chatOf(msg("user", text("x"), png)), models.ImageCapabilities{Input: models.ImageInputNone, ToolResult: models.ToolResultNative, MIMEs: allMIMEs()}, "route accepts no images"},
			{"none, user image after tool and generated images", chatOf(msg("assistant", genPending), toolMsg("a", gifStored), msg("user", text("x"), gifPending)), models.ImageCapabilities{}, "route accepts no images"},
			{"none, user image refused before any load", chatOf(toolMsg("a", gifStored), msg("user", absentUser)), models.ImageCapabilities{}, "route accepts no images"},
			{"gif stored in tool", chatOf(msg("user", text("x"), png), toolMsg("a", gifStored)), pngJPEG, "mime image/gif not accepted (image " + handle(t, gifStored) + ")"},
			{"gif pending in user", chatOf(msg("user", gifPending)), pngJPEG, "mime image/gif not accepted (image " + handle(t, gifPending) + ")"},
		}
		for _, tc := range cases {
			out, err := s.norm.Normalize(tc.chat, tc.caps)
			var iu *claierr.ImageUnsupportedError
			if !errors.As(err, &iu) {
				t.Fatalf("%s: err = %v, want *claierr.ImageUnsupportedError", tc.name, err)
			}
			if iu.Reason != tc.reason {
				t.Fatalf("%s: Reason = %q, want %q", tc.name, iu.Reason, tc.reason)
			}
			if !errors.Is(err, claierr.ErrImageUnsupported) {
				t.Fatalf("%s: err does not match claierr.ErrImageUnsupported", tc.name)
			}
			if !reflect.DeepEqual(out, pub_models.Chat{}) {
				t.Fatalf("%s: a refusal returned a partial chat: %s", tc.name, outline(out.Messages))
			}
		}
	})
}

// Proves Normalize.2, TL1.
func TestNormalize_NoImageRouteDegrades(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		toolImg := stored(t, s, gifData(23), "image/gif", pub_models.OriginTool)
		absent := pub_models.ImagePart{Asset: hexSum(pngData(24)) + ".png", MIME: "image/png", Origin: pub_models.OriginGenerated}
		gen := pending(pngData(25), "image/png", pub_models.OriginGenerated)
		build := func() pub_models.Chat {
			return chatOf(
				pub_models.Message{Role: "system", Content: "sys"},
				msg("user", text("snap, then draw")),
				pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a"), Parts: []pub_models.Part{text("drawing"), gen}},
				toolMsg("a", text("snapshot"), toolImg),
				msg("assistant", text("earlier"), absent),
				msg("user", text("next")),
			)
		}
		want := []pub_models.Message{
			{Role: "system", Content: "sys"},
			msg("user", text("snap, then draw")),
			{Role: "assistant", ToolCalls: toolCalls("a"), Parts: []pub_models.Part{text("drawing"), label(t, gen)}},
			toolMsg("a", text("snapshot"), label(t, toolImg)),
			msg("assistant", text("earlier"), label(t, absent)),
			msg("user", text("next")),
		}
		loads := 0
		norm := imagenorm.New(loaderFunc(func(ref string) (asset.Image, error) {
			loads++
			return s.store.Load(ref)
		}))
		for _, caps := range []models.ImageCapabilities{
			{},
			{Input: models.ImageInputNone, ToolResult: models.ToolResultFallback},
			{Input: models.ImageInputNone, ToolResult: models.ToolResultNative, MIMEs: []string{"image/jpeg"}},
		} {
			in := build()
			out, err := norm.Normalize(in, caps)
			if err != nil {
				t.Fatalf("caps %+v: Normalize: %v", caps, err)
			}
			wantMessages(t, out.Messages, want)
			if loads != 0 {
				t.Fatalf("caps %+v: %d loads, want none", caps, loads)
			}
			if !reflect.DeepEqual(in, build()) {
				t.Fatalf("caps %+v: input mutated\n got: %s", caps, outline(in.Messages))
			}
		}
	})
}

// Proves Normalize.9.
func TestNormalize_NoMutation(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		user := stored(t, s, pngData(12), "image/png", pub_models.OriginUser)
		gen := stored(t, s, pngData(13), "image/png", pub_models.OriginGenerated)
		build := func() pub_models.Chat {
			return chatOf(
				msg("user", text("look"), user, pending(jpegData(14), "image/jpeg", pub_models.OriginUser)),
				pub_models.Message{Role: "assistant", ToolCalls: toolCalls("a"), Parts: []pub_models.Part{text("here"), gen}},
				toolMsg("a", text("tool"), pending(pngData(15), "image/png", pub_models.OriginTool)),
				msg("user", text("next")),
			)
		}
		for _, caps := range []models.ImageCapabilities{fallbackCaps(), nativeCaps()} {
			in := build()
			out := normalize(t, s, in, caps)
			if !reflect.DeepEqual(in, build()) {
				t.Fatalf("input mutated\n got: %s\nwant: %s", outline(in.Messages), outline(build().Messages))
			}
			assertSendable(t, out.Messages, caps.ToolResult)
		}
	})
}

type loaderFunc func(ref string) (asset.Image, error)

func (f loaderFunc) Load(ref string) (asset.Image, error) { return f(ref) }

// Proves Normalize.10.
func TestNormalize_LoadFails(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		missingData, corruptData := pngData(16), pngData(17)
		missing := pub_models.ImagePart{Asset: hexSum(missingData) + ".png", MIME: "image/png", Origin: pub_models.OriginUser}
		corrupt := pub_models.ImagePart{Asset: hexSum(corruptData) + ".png", MIME: "image/png", Origin: pub_models.OriginUser}
		s.plant(t, corrupt.Asset, pngData(18))
		for _, tc := range []struct {
			img  pub_models.ImagePart
			want error
		}{
			{missing, asset.ErrAssetNotFound},
			{corrupt, asset.ErrAssetCorrupt},
		} {
			out, err := s.norm.Normalize(chatOf(msg("user", text("x"), tc.img)), fallbackCaps())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if h := handle(t, tc.img); !strings.Contains(err.Error(), "image "+h) {
				t.Fatalf("error %q does not name handle %s", err, h)
			}
			if !reflect.DeepEqual(out, pub_models.Chat{}) {
				t.Fatalf("a failed load returned a partial chat: %s", outline(out.Messages))
			}
		}
	})
	t.Run("typed loader error", func(t *testing.T) {
		pathErr := &fs.PathError{Op: "read", Path: "assets/x.png", Err: fs.ErrPermission}
		norm := imagenorm.New(loaderFunc(func(string) (asset.Image, error) { return asset.Image{}, pathErr }))
		img := pub_models.ImagePart{Asset: hexSum(pngData(19)) + ".png", MIME: "image/png", Origin: pub_models.OriginTool}
		_, err := norm.Normalize(chatOf(toolMsg("a", img)), nativeCaps())
		var got *fs.PathError
		if !errors.As(err, &got) || got != pathErr {
			t.Fatalf("err = %v, want the loader's *fs.PathError", err)
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("err = %v, want fs.ErrPermission", err)
		}
		if h := handle(t, img); !strings.Contains(err.Error(), "image "+h) {
			t.Fatalf("error %q does not name handle %s", err, h)
		}
	})
}

func hexSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNormalize_InvalidInput(t *testing.T) {
	forEachSubject(t, func(t *testing.T, s subject) {
		img := pending(pngData(20), "image/png", pub_models.OriginUser)
		_, err := s.norm.Normalize(chatOf(msg("user", pub_models.ImagePart{MIME: "image/png"})), nativeCaps())
		if !errors.Is(err, pub_models.ErrImageEmpty) {
			t.Fatalf("empty image: err = %v, want pub_models.ErrImageEmpty", err)
		}
		_, err = s.norm.Normalize(chatOf(toolMsg("a", pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginTool})), models.ImageCapabilities{})
		if !errors.Is(err, pub_models.ErrImageEmpty) {
			t.Fatalf("empty image on a route that accepts none: err = %v, want pub_models.ErrImageEmpty", err)
		}
		for _, caps := range []models.ImageCapabilities{
			{Input: models.ImageInput(9), ToolResult: models.ToolResultNative, MIMEs: allMIMEs()},
			{Input: models.ImageInputInline, ToolResult: models.ToolResultImages(9), MIMEs: allMIMEs()},
		} {
			out, err := s.norm.Normalize(chatOf(msg("user", img)), caps)
			if err == nil || !strings.Contains(err.Error(), "capability") {
				t.Fatalf("caps %+v: err = %v, want an unknown-capability error", caps, err)
			}
			if !reflect.DeepEqual(out, pub_models.Chat{}) {
				t.Fatalf("caps %+v: returned a partial chat", caps)
			}
		}
	})
}
