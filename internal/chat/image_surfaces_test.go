package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func storedImageChat(t *testing.T, confDir, id string, data []byte, msgs ...pub_models.Message) pub_models.Chat {
	t.Helper()
	c := pub_models.Chat{ID: id, Created: time.Now(), Messages: msgs}
	saved, err := NewPersister(confDir, asset.NewStore(AssetsDir(confDir))).Save(c)
	if err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
	return saved
}

func TestReplayText_rendersImagePlaceholders(t *testing.T) {
	confDir := t.TempDir()
	store := asset.NewStore(AssetsDir(confDir))
	stored := testPNG(t, 1)
	ref, err := store.Put(stored, "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	legacy := testPNG(t, 2)
	msg := pub_models.Message{Role: "assistant", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "here"},
		pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginGenerated},
		pending(legacy),
	}}

	got, err := replayText(store, msg)
	if err != nil {
		t.Fatalf("replayText: %v", err)
	}
	path := filepath.Join(AssetsDir(confDir), ref)
	want := "here\n[image " + ref[:6] + "] " + path + "\n" + pending(legacy).Describe() + " (not stored yet)"
	if got.Content != want || got.Parts != nil {
		t.Fatalf("replayText = %q (parts %v), want %q", got.Content, got.Parts, want)
	}

	text := pub_models.Message{Role: "assistant", Content: "plain"}
	if got, err := replayText(store, text); err != nil || got.Content != "plain" {
		t.Fatalf("text-only replayText = %q, %v; want it unchanged", got.Content, err)
	}

	missing := pub_models.Message{Parts: []pub_models.Part{pub_models.ImagePart{Asset: pngRef(testPNG(t, 3)), MIME: "image/png"}}}
	if _, err := replayText(store, missing); !errors.Is(err, asset.ErrAssetNotFound) {
		t.Fatalf("missing asset: err = %v, want ErrAssetNotFound", err)
	}
}

func TestReplay_printsStoredImagePath(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	data := testPNG(t, 4)
	saved := storedImageChat(t, confDir, globalScopeChatID, data,
		pub_models.Message{Role: "user", Content: "draw"},
		pub_models.Message{Role: "assistant", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "done"},
			pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginGenerated, Data: data},
		}},
	)
	ref := saved.Messages[1].Parts[1].(pub_models.ImagePart).Asset

	var replayErr error
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		replayErr = Replay(true, false)
	})
	if replayErr != nil {
		t.Fatalf("Replay: %v", replayErr)
	}
	want := "done\n[image " + ref[:6] + "] " + filepath.Join(AssetsDir(confDir), ref) + "\n"
	if stdout != want {
		t.Fatalf("Replay stdout = %q, want %q", stdout, want)
	}
}

func TestLoadGlobalScope_legacyImageThroughPersister(t *testing.T) {
	confDir := t.TempDir()
	convDir := conversationsDir(confDir)
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	write := func(data []byte, mime string) {
		t.Helper()
		raw := map[string]any{"id": "globalScope", "created": time.Now(), "messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}},
				map[string]any{"type": "text", "text": "what is it"},
			}},
		}}
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := os.WriteFile(GlobalScopePath(confDir), b, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	data := testPNG(t, 5)
	write(data, "image/png")
	got, err := LoadPrevQuery(confDir)
	if err != nil {
		t.Fatalf("LoadPrevQuery: %v", err)
	}
	img, ok := got.Messages[0].Parts[0].(pub_models.ImagePart)
	if !ok || img.Asset != "" || string(img.Data) != string(data) {
		t.Fatalf("legacy part = %#v, want a pending image", got.Messages[0].Parts[0])
	}

	write([]byte("not an image at all"), "image/png")
	if _, err := LoadPrevQuery(confDir); !errors.Is(err, asset.ErrNotImage) {
		t.Fatalf("non-image legacy part: err = %v, want ErrNotImage from Persister.Load", err)
	}
}

func TestEditorText_keepsImages(t *testing.T) {
	img := pub_models.ImagePart{Asset: pngRef(testPNG(t, 6)), MIME: "image/png", Origin: pub_models.OriginUser}
	msg := pub_models.Message{Role: "user", Parts: []pub_models.Part{img, pub_models.TextPart{Text: "one"}, pub_models.TextPart{Text: "two"}}}
	if got := messageEditorText(msg); got != "one\ntwo" {
		t.Fatalf("messageEditorText = %q, want the text parts", got)
	}
	edited := withEditedText(msg, "new")
	if len(edited.Parts) != 2 || edited.Parts[0].(pub_models.ImagePart).Asset != img.Asset || edited.Parts[1] != (pub_models.TextPart{Text: "new"}) || edited.Content != "" {
		t.Fatalf("edited parts = %#v", edited.Parts)
	}

	for _, blank := range []string{"", " \n\t"} {
		if edited := withEditedText(msg, blank); len(edited.Parts) != 1 || edited.Parts[0].(pub_models.ImagePart).Asset != img.Asset {
			t.Fatalf("blank edit %q: parts = %#v, want the image alone", blank, edited.Parts)
		}
	}

	only := pub_models.Message{Role: "user", Parts: []pub_models.Part{img}}
	if got := messageEditorText(only); got != "" {
		t.Fatalf("image-only editor text = %q", got)
	}
	if edited := withEditedText(only, "caption"); len(edited.Parts) != 2 || edited.Parts[1] != (pub_models.TextPart{Text: "caption"}) {
		t.Fatalf("image-only edited parts = %#v", edited.Parts)
	}

	text := pub_models.Message{Role: "user", Content: "old"}
	if got := withEditedText(text, "fresh"); got.Content != "fresh" || got.Parts != nil {
		t.Fatalf("text edit = %#v", got)
	}
	if got := withEditedText(text, " \n"); got.Content != "" {
		t.Fatalf("blank text edit = %q, want no text", got.Content)
	}
}

func TestTextSurfaces_describeImages(t *testing.T) {
	cq, confDir := newTestHandler(t)
	data := testPNG(t, 7)
	saved := storedImageChat(t, confDir, "pic", data,
		pub_models.Message{Role: "system", Content: "sys"},
		pub_models.Message{Role: "user", Parts: []pub_models.Part{pending(data), pub_models.TextPart{Text: "find zebra"}}},
		pub_models.Message{Role: "assistant", Content: "a zebra"},
	)
	label := "[image " + saved.Messages[1].Parts[0].(pub_models.ImagePart).Asset[:6] + "]"

	read, _, err := ReadMessage(confDir, "pic", 1)
	if err != nil || read != "[user]\n"+label+"\nfind zebra" {
		t.Fatalf("ReadMessage = %q, %v", read, err)
	}
	inspected, err := InspectConversation(confDir, "pic", 0, 10, "user", "")
	if err != nil || !strings.Contains(inspected, label) {
		t.Fatalf("InspectConversation = %q, %v", inspected, err)
	}
	content, firstUser := searchableContent(saved)
	if !strings.Contains(content, label) || firstUser != label+"\nfind zebra" {
		t.Fatalf("searchableContent = %q / %q", content, firstUser)
	}

	cq.dims.Width = 200
	onlyImage := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Parts: []pub_models.Part{saved.Messages[1].Parts[0]}}}}
	if got := cq.chatInfoLabel(onlyImage); !strings.Contains(got, label) {
		t.Fatalf("chatInfoLabel = %q, want %q", got, label)
	}
}

func TestDeleteFromPrompt_sweepsUnreferencedAssets(t *testing.T) {
	cq, confDir := newTestHandler(t)
	cq.out = io.Discard
	data := testPNG(t, 8)
	msgs := []pub_models.Message{{Role: "user", Parts: []pub_models.Part{pending(data)}}}
	storedImageChat(t, confDir, "first", data, msgs...)
	storedImageChat(t, confDir, "second", data, msgs...)
	assetPath := filepath.Join(AssetsDir(confDir), pngRef(data))

	cq.prompt = "first"
	if err := cq.deleteFromPrompt(); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("asset still referenced by second must remain: %v", err)
	}
	cq.prompt = "second"
	if err := cq.deleteFromPrompt(); err != nil {
		t.Fatalf("delete second: %v", err)
	}
	if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced asset must be swept, stat err = %v", err)
	}
}

func TestDeleteFromPrompt_sidecarFailureWarns(t *testing.T) {
	cq, confDir := newTestHandler(t)
	cq.out = io.Discard
	data := testPNG(t, 17)
	storedImageChat(t, confDir, "doomed", data, pub_models.Message{Role: "user", Parts: []pub_models.Part{pending(data)}})
	convDir := conversationsDir(confDir)
	blockReasoningSidecars(t, convDir)

	cq.prompt = "doomed"
	var deleteErr error
	stdout := testboil.CaptureStdout(t, func(*testing.T) { deleteErr = cq.deleteFromPrompt() })
	if deleteErr != nil {
		t.Fatalf("deleteFromPrompt: %v, want the delete to succeed", deleteErr)
	}
	if !strings.Contains(stdout, `failed to remove reasoning sidecar for chat "doomed": remove reasoning dir`) || !strings.Contains(stdout, "deleted chat 'doomed'") {
		t.Fatalf("stdout = %q, want the sidecar warning, then the deletion", stdout)
	}
	if _, err := os.Stat(conversationPathFromDir(convDir, "doomed")); !os.IsNotExist(err) {
		t.Fatalf("the conversation must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(AssetsDir(confDir), pngRef(data))); !os.IsNotExist(err) {
		t.Fatalf("the orphan asset must be swept, stat err = %v", err)
	}
}
