package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// imageFixture is a temp config, a work dir holding cat.png, and that image's identity.
type imageFixture struct {
	confDir string
	work    string
	png     []byte
	ref     string
	handle  string
}

func pngRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + ".png"
}

func newImageFixture(t *testing.T) imageFixture {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	work := e2etest.ChdirTemp(t)
	png := e2etest.PNG(t, 1)
	if err := os.WriteFile(filepath.Join(work, "cat.png"), png, 0o644); err != nil {
		t.Fatalf("write cat.png: %v", err)
	}
	ref := pngRef(png)
	return imageFixture{confDir: confDir, work: work, png: png, ref: ref, handle: ref[:6]}
}

func (f imageFixture) assetDir() string { return filepath.Join(f.confDir, "conversations", "assets") }

func (f imageFixture) globalScope(t *testing.T) pub_models.Chat {
	t.Helper()
	c, err := chat.FromPath(filepath.Join(f.confDir, "conversations", "globalScope.json"))
	if err != nil {
		t.Fatalf("load globalScope.json: %v", err)
	}
	return c
}

func runCaptured(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(args)
	})
	return status, stdout, stderr
}

func withStdin(t *testing.T, data []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
}

func assetFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func imageParts(c pub_models.Chat, role string) []pub_models.ImagePart {
	var out []pub_models.ImagePart
	for _, m := range c.Messages {
		if m.Role != role {
			continue
		}
		for _, p := range m.Parts {
			if img, ok := p.(pub_models.ImagePart); ok {
				out = append(out, img)
			}
		}
	}
	return out
}

// attach runs the AC1 query and checks it reached the mock as a labelled image and persisted a ref.
func (f imageFixture) attach(t *testing.T, args ...string) {
	t.Helper()
	status, stdout, stderr := runCaptured(t, args...)
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(stdout, "[image "+f.handle+"]") || !strings.Contains(stdout, "what breed?") {
		t.Fatalf("mock echo = %q, want the label [image %s] and the prompt", stdout, f.handle)
	}
	f.assertStoredUserImage(t)
}

func (f imageFixture) assertStoredUserImage(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.confDir, "conversations", "globalScope.json"))
	if err != nil {
		t.Fatalf("read globalScope.json: %v", err)
	}
	if strings.Contains(string(raw), base64.StdEncoding.EncodeToString(f.png)) {
		t.Fatal("conversation JSON holds base64 image bytes")
	}
	images := imageParts(f.globalScope(t), "user")
	if len(images) != 1 || images[0].Asset != f.ref || images[0].Origin != pub_models.OriginUser {
		t.Fatalf("user images = %+v, want one stored ref %s", images, f.ref)
	}
	if _, err := os.Stat(filepath.Join(f.assetDir(), f.ref)); err != nil {
		t.Fatalf("asset file: %v", err)
	}
}

func Test_goldenFile_QUERY_img_attach(t *testing.T) {
	f := newImageFixture(t)
	f.attach(t, "-r", "-img", "cat.png", "-cm", "test", "q", "what breed?")
}

func Test_goldenFile_QUERY_stdin_image(t *testing.T) {
	f := newImageFixture(t)
	withStdin(t, f.png)
	f.attach(t, "-r", "-cm", "test", "q", "what breed?")
	if c := f.globalScope(t); strings.Contains(c.Messages[len(c.Messages)-2].Describe(), "PNG") {
		t.Fatal("stdin image bytes leaked into the prompt text")
	}
}

func Test_goldenFile_QUERY_glob_mixed(t *testing.T) {
	f := newImageFixture(t)
	docs := filepath.Join(f.work, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.md"), []byte("alpha notes"), 0o644); err != nil {
		t.Fatalf("write a.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "b.png"), f.png, 0o644); err != nil {
		t.Fatalf("write b.png: %v", err)
	}
	// A pattern with a "/" names the glob chat "glob_docs/*", which today's promote cannot write.
	if err := os.Chdir(docs); err != nil {
		t.Fatalf("chdir docs: %v", err)
	}
	status, stdout, stderr := runCaptured(t, "-r", "-cm", "test", "-g", "*", "q", "summarize")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	c := f.globalScope(t)
	var mdMessage bool
	for _, m := range c.Messages {
		if strings.Contains(m.Content, `"fileName": "b.png"`) {
			t.Fatalf("the png was read as text: %q", m.Content)
		}
		if m.Role == "user" && m.Content == `{"fileName": "a.md", "data": "alpha notes"}` {
			mdMessage = true
		}
	}
	if !mdMessage {
		t.Fatalf("no glob file message for a.md in %+v", c.Messages)
	}
	last := c.Messages[len(c.Messages)-2]
	if len(last.Parts) != 2 || last.Parts[1] != (pub_models.TextPart{Text: "summarize"}) {
		t.Fatalf("prompt message = %+v, want the png image then the prompt", last)
	}
	if img, ok := last.Parts[0].(pub_models.ImagePart); !ok || img.Asset != f.ref {
		t.Fatalf("prompt message image = %+v, want ref %s", last.Parts[0], f.ref)
	}
}

func Test_goldenFile_QUERY_reply_carries_image(t *testing.T) {
	f := newImageFixture(t)
	f.attach(t, "-r", "-img", "cat.png", "-cm", "test", "q", "what breed?")

	// The mock refuses an image without bytes, so a clean reply proves the stored image was loaded and sent.
	status, stdout, stderr := runCaptured(t, "-r", "-re", "-cm", "test", "q", "and now?")
	if status != 0 || stdout != "and now?\n" {
		t.Fatalf("reply status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	c := f.globalScope(t)
	images := imageParts(c, "user")
	if len(images) != 1 || images[0].Asset != f.ref {
		t.Fatalf("replied chat images = %+v, want the stored ref %s carried", images, f.ref)
	}
	if got := c.Messages[len(c.Messages)-2].Content; got != "and now?" {
		t.Fatalf("reply prompt = %q", got)
	}
	if got := assetFiles(t, f.assetDir()); len(got) != 1 || got[0] != f.ref {
		t.Fatalf("assets = %v, want only %s", got, f.ref)
	}
}

func Test_goldenFile_QUERY_generate_image_client(t *testing.T) {
	f := newImageFixture(t)
	generated := e2etest.PNG(t, 9)
	fake := harness.UseFakeImageGenerator(t, generated)

	status, stdout, stderr := runCaptured(t, "-r", "-t", "generate_image", "-cm", "test", "q", "draw tool_generate_image")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	path := filepath.Join(f.assetDir(), pngRef(generated))
	if !strings.Contains(stdout, path+"\n") || strings.Contains(stdout, "\x1b") {
		t.Fatalf("stdout = %q, want the bare asset path %s", stdout, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("generated asset: %v", err)
	}
	configs, requests := fake.Configs(), fake.Requests()
	if len(configs) != 1 || configs[0].ConfigDir != f.confDir || configs[0].Model == "" {
		t.Fatalf("generator configs = %+v, want one with the config dir and the photoConfig model", configs)
	}
	if len(requests) != 1 || requests[0].Prompt != "mock image" {
		t.Fatalf("generator requests = %+v", requests)
	}
	images := imageParts(f.globalScope(t), "tool")
	if len(images) != 1 || images[0].Origin != pub_models.OriginGenerated || images[0].Asset != pngRef(generated) {
		t.Fatalf("tool images = %+v, want one generated part", images)
	}
}

func Test_goldenFile_QUERY_img_refused_inception(t *testing.T) {
	f := newImageFixture(t)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INCEPTION_API_KEY", "dummy")
	e2etest.WriteJSONFileAny(t, filepath.Join(f.confDir, "inception_mercury_mercury.json"), map[string]any{
		"model": "mercury",
		"url":   srv.URL,
	})

	status, stdout, stderr := runCaptured(t, "-r", "-cm", "mercury", "-img", "cat.png", "q", "what breed?")
	if status != 1 {
		t.Fatalf("status %d, want 1; stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(stderr, "image not supported by vendor") || !strings.Contains(stderr, "route accepts no images") {
		t.Fatalf("stderr = %q, want the ImageUnsupportedError text", stderr)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("vendor received %d requests, want 0", got)
	}
}

func Test_e2e_image_generator_guard(t *testing.T) {
	f := newImageFixture(t)
	status, stdout, stderr := runCaptured(t, "-r", "-t", "generate_image", "-cm", "test", "q", "draw tool_generate_image")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	var result string
	for _, m := range f.globalScope(t).Messages {
		if m.Role == "tool" {
			result = m.Content
		}
	}
	if !strings.HasPrefix(result, "ERROR:") || !strings.Contains(result, e2etest.ErrImageGeneratorRefused.Error()) {
		t.Fatalf("tool result = %q, want an ERROR naming the refusal", result)
	}
	if got := assetFiles(t, f.assetDir()); len(got) != 0 {
		t.Fatalf("assets = %v, want none", got)
	}
}

func Test_e2e_generate_image_tool_selection(t *testing.T) {
	f := newImageFixture(t)
	for _, selection := range []string{"generate_image", "*_image", "gen*"} {
		t.Run(selection, func(t *testing.T) {
			status, stdout, stderr := runCaptured(t, "-r", "-t", selection, "-cm", "test", "q", "hi")
			if status != 0 || stdout != "hi\n" {
				t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, warning := range []string{"unknown tool", "disabling tools", "which doesn't exist", "found none"} {
				if strings.Contains(stdout+stderr, warning) {
					t.Fatalf("tool selection warned %q: stdout=%q stderr=%q", warning, stdout, stderr)
				}
			}
			status, _, stderr = runCaptured(t, "-r", "-t", selection, "-cm", "test", "q", "tool_generate_image")
			if status != 0 {
				t.Fatalf("second run status %d, stderr=%q", status, stderr)
			}
			// The default deps refuse to generate, so reaching that refusal proves the
			// tool was registered and called.
			var called bool
			for _, m := range f.globalScope(t).Messages {
				called = called || (m.Role == "tool" && strings.Contains(m.Describe(), e2etest.ErrImageGeneratorRefused.Error()))
			}
			if !called {
				t.Fatalf("-t %q did not offer generate_image: tools must stay enabled", selection)
			}
		})
	}
}

func Test_e2e_query_img_no_match(t *testing.T) {
	f := newImageFixture(t)
	status, stdout, stderr := runCaptured(t, "-r", "-cm", "test", "-img", "nope/*.png", "q", "hi")
	if status != 1 || !strings.Contains(stderr, "nope/*.png") {
		t.Fatalf("status %d, stdout=%q stderr=%q; want 1 and the pattern named", status, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(f.confDir, "conversations", "globalScope.json")); !os.IsNotExist(err) {
		t.Fatalf("the mock was queried: globalScope.json stat err = %v", err)
	}
}

func Test_goldenFile_REPLAY_image_placeholder(t *testing.T) {
	f := newImageFixture(t)
	f.attach(t, "-r", "-img", "cat.png", "-cm", "test", "q", "what breed?")
	// replay prints the last message, and the mock answers in text, so the
	// stored image becomes a generated part of the last message, as native output writes it.
	c := f.globalScope(t)
	c.Messages[len(c.Messages)-1] = pub_models.Message{Role: "assistant", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "a tabby"},
		pub_models.ImagePart{Asset: f.ref, MIME: "image/png", Origin: pub_models.OriginGenerated},
	}}
	if err := chat.Save(filepath.Join(f.confDir, "conversations"), c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	status, stdout, stderr := runCaptured(t, "-r", "re")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := "a tabby\n[image " + f.handle + "] " + filepath.Join(f.assetDir(), f.ref) + "\n"
	if stdout != want {
		t.Fatalf("replay = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, base64.StdEncoding.EncodeToString(f.png)) {
		t.Fatal("replay printed base64")
	}
}

func Test_e2e_replay_legacy_pending(t *testing.T) {
	f := newImageFixture(t)
	e2etest.WriteJSONFileAny(t, filepath.Join(f.confDir, "conversations", "globalScope.json"), map[string]any{
		"id":      "globalScope",
		"created": "2026-01-01T00:00:00Z",
		"messages": []any{
			map[string]any{"role": "user", "content": "draw a cat"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "here"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(f.png)}},
			}},
		},
	})
	status, stdout, stderr := runCaptured(t, "-r", "re")
	if status != 0 || stdout != "here\n[image "+f.handle+"] (not stored yet)\n" {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
}

var piped = regexp.MustCompile(`^(.+/clai_[^/\s]+\.png)\n$`)

func Test_goldenFile_PHOTO_piped_bare_path(t *testing.T) {
	newImageFixture(t)
	generated := e2etest.PNG(t, 7)
	harness.UseFakeImageGenerator(t, generated)
	dir := t.TempDir()

	status, stdout, stderr := runCaptured(t, "photo", "-pd", dir, "a", "fox")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	m := piped.FindStringSubmatch(stdout)
	if m == nil || filepath.Dir(m[1]) != dir || strings.Contains(stdout, "\x1b") {
		t.Fatalf("stdout = %q, want exactly %s/clai_<rand>.png and a newline", stdout, dir)
	}
	saved, err := os.ReadFile(m[1])
	if err != nil || string(saved) != string(generated) {
		t.Fatalf("saved file: err=%v, bytes equal=%v", err, string(saved) == string(generated))
	}
}

func Test_e2e_photo_reply_no_previous_image(t *testing.T) {
	f := newImageFixture(t)
	fake := harness.UseFakeImageGenerator(t, e2etest.PNG(t, 3))
	dir := t.TempDir()
	run := func() {
		t.Helper()
		status, stdout, stderr := runCaptured(t, "-r", "-re", "photo", "-pd", dir, "x")
		if status != 0 || !strings.Contains(stdout, dir+"/clai_") {
			t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
		}
		requests := fake.Requests()
		if last := requests[len(requests)-1]; len(last.Inputs) != 0 {
			t.Fatalf("inputs = %d, want none", len(last.Inputs))
		}
	}
	run()
	e2etest.WriteJSONFileAny(t, filepath.Join(f.confDir, "conversations", "globalScope.json"), map[string]any{
		"id":       "globalScope",
		"created":  "2026-01-01T00:00:00Z",
		"messages": []any{map[string]any{"role": "user", "content": "text only"}},
	})
	run()
}

func Test_e2e_photo_reply_previous_image(t *testing.T) {
	f := newImageFixture(t)
	f.attach(t, "-r", "-img", "cat.png", "-cm", "test", "q", "what breed?")
	fake := harness.UseFakeImageGenerator(t, e2etest.PNG(t, 4))

	status, stdout, stderr := runCaptured(t, "-r", "-re", "photo", "-pd", t.TempDir(), "purple")
	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	requests := fake.Requests()
	if len(requests) != 1 || len(requests[0].Inputs) == 0 {
		t.Fatalf("requests = %+v, want one with the previous image", requests)
	}
	last := requests[0].Inputs[len(requests[0].Inputs)-1]
	if string(last.Data) != string(f.png) || last.MIME != "image/png" {
		t.Fatalf("last input = %s %d bytes, want the attached png", last.MIME, len(last.Data))
	}
}

func Test_goldenFile_CHAT_list_image_preview(t *testing.T) {
	f := newImageFixture(t)
	convDir := filepath.Join(f.confDir, "conversations")
	e2etest.SeedConv(t, convDir, pub_models.Chat{ID: "pic", Messages: []pub_models.Message{
		{Role: "user", Parts: []pub_models.Part{pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: f.png}}},
		e2etest.MsgAsst("a cat"),
	}})
	// The 80-column fallback width truncates every row label, so the row's preview
	// is read from the index the list renders, and from the selected row's view.
	stdout, status := harness.RunOne(t, f.confDir, "-n -r c l 0")
	if status != 0 || !strings.Contains(stdout, `summary: "[image `+f.handle+`]"`) {
		t.Fatalf("status %d, list = %q, want the [image %s] preview", status, stdout, f.handle)
	}
	index := e2etest.ReadStringFile(t, filepath.Join(convDir, "chat_index.cache"))
	if !strings.Contains(index, `"first_user_message":"[image `+f.handle+`]"`) {
		t.Fatalf("index = %s, want the row preview [image %s]", index, f.handle)
	}
}

func Test_e2e_chat_delete_sweeps_assets(t *testing.T) {
	f := newImageFixture(t)
	convDir := filepath.Join(f.confDir, "conversations")
	for _, id := range []string{"first", "second"} {
		e2etest.SeedConv(t, convDir, pub_models.Chat{ID: id, Messages: []pub_models.Message{
			{Role: "user", Parts: []pub_models.Part{pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: f.png}, pub_models.TextPart{Text: id}}},
		}})
	}
	asset := filepath.Join(f.assetDir(), f.ref)
	if _, status := harness.RunOne(t, f.confDir, "-r c d first"); status != 0 {
		t.Fatalf("delete first: status %d", status)
	}
	if _, err := os.Stat(asset); err != nil {
		t.Fatalf("the asset second still references must remain: %v", err)
	}
	if _, status := harness.RunOne(t, f.confDir, "-r c d second"); status != 0 {
		t.Fatalf("delete second: status %d", status)
	}
	if _, err := os.Stat(asset); !os.IsNotExist(err) {
		t.Fatalf("the unreferenced asset must be swept, stat err = %v", err)
	}
}

func Test_e2e_editor_keeps_images(t *testing.T) {
	f := newImageFixture(t)
	editor := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'new text' > \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editor)
	convDir := filepath.Join(f.confDir, "conversations")
	e2etest.SeedConv(t, convDir, pub_models.Chat{ID: "pic", Messages: []pub_models.Message{
		{Role: "user", Parts: []pub_models.Part{pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: f.png}, pub_models.TextPart{Text: "old text"}}},
		e2etest.MsgAsst("a cat"),
	}})

	if stdout, status := harness.RunOne(t, f.confDir, "-n -r c l 0 e 0 b"); status != 0 {
		t.Fatalf("status %d, stdout=%q", status, stdout)
	}
	edited, err := chat.FromPath(filepath.Join(convDir, "pic.json"))
	if err != nil {
		t.Fatalf("load edited chat: %v", err)
	}
	parts := edited.Messages[0].Parts
	if len(parts) != 2 || parts[1] != (pub_models.TextPart{Text: "new text"}) {
		t.Fatalf("edited parts = %+v, want the image then the new text", parts)
	}
	if img, ok := parts[0].(pub_models.ImagePart); !ok || img.Asset != f.ref {
		t.Fatalf("edited image = %+v, want ref %s kept", parts[0], f.ref)
	}
}

func TestDocs_ImagesArchitectureIndexed(t *testing.T) {
	index := e2etest.ReadStringFile(t, filepath.Join(e2etest.ModuleRoot(t), "architecture", "README.md"))
	if !strings.Contains(index, "](./images.md)") {
		t.Fatal("architecture/README.md does not link images.md")
	}
	if _, err := os.Stat(filepath.Join(e2etest.ModuleRoot(t), "architecture", "images.md")); err != nil {
		t.Fatalf("architecture/images.md: %v", err)
	}
}
