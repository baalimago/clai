package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type stubStore struct {
	*assettest.Memory
	putErr   error
	sweepErr error
	sweeps   int
}

func (s *stubStore) Put(data []byte, mime string) (string, error) {
	if s.putErr != nil {
		return "", s.putErr
	}
	return s.Memory.Put(data, mime)
}

func (s *stubStore) Sweep(live map[string]struct{}) (asset.SweepCount, error) {
	s.sweeps++
	if s.sweepErr != nil {
		return 0, s.sweepErr
	}
	return s.Memory.Sweep(live)
}

func testPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: shade, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func pngRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + ".png"
}

func pending(data []byte) pub_models.ImagePart {
	return pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: data}
}

func imageChat(id string, images ...[]byte) pub_models.Chat {
	parts := []pub_models.Part{pub_models.TextPart{Text: "look"}}
	for _, img := range images {
		parts = append(parts, pending(img))
	}
	return pub_models.Chat{
		ID: id,
		Messages: []pub_models.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Parts: parts},
			{Role: "assistant", Content: "seen"},
		},
	}
}

func newDiskPersister(t *testing.T) (p Persister, confDir, convDir, assetDir string) {
	t.Helper()
	confDir = t.TempDir()
	convDir = conversationsDir(confDir)
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}
	assetDir = filepath.Join(convDir, "assets")
	return NewPersister(confDir, asset.NewStore(assetDir)), confDir, convDir, assetDir
}

func assetNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read asset dir: %v", err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertAssets(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := assetNames(t, dir)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("asset files = %v, want %v", got, want)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// assertRefsOnly fails unless the file references ref and holds no image bytes.
func assertRefsOnly(t *testing.T, path, ref string) {
	t.Helper()
	b := string(mustRead(t, path))
	if !strings.Contains(b, `"asset": "`+ref+`"`) {
		t.Fatalf("%s does not reference %s:\n%s", filepath.Base(path), ref, b)
	}
	for _, banned := range []string{"image_url", "base64", "data:image"} {
		if strings.Contains(b, banned) {
			t.Fatalf("%s holds %q:\n%s", filepath.Base(path), banned, b)
		}
	}
}

func writeLegacyChat(t *testing.T, path string, dataURL string) {
	t.Helper()
	legacy := map[string]any{
		"created": "2025-06-01T10:00:00Z",
		"id":      strings.TrimSuffix(filepath.Base(path), ".json"),
		"messages": []any{
			map[string]any{"role": "system", "content": "s"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is this?"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			}},
		},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy chat: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write legacy chat: %v", err)
	}
}

func copyLegacyFixture(t *testing.T, dst string) {
	t.Helper()
	if err := os.WriteFile(dst, mustRead(t, "testdata/legacy-image-conversation.json"), 0o644); err != nil {
		t.Fatalf("copy legacy fixture: %v", err)
	}
}

func indexRow(t *testing.T, convDir, id string) chatIndexRow {
	t.Helper()
	rows, err := readChatIndex(convDir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no index row for %s", id)
	return chatIndexRow{}
}

func TestPersister_SaveStoresPending(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	img := testPNG(t, 1)
	in := imageChat("with-image", img)

	out, err := p.Save(in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	ref := pngRef(img)
	assertAssets(t, assetDir, ref)
	assertRefsOnly(t, filepath.Join(convDir, "with-image.json"), ref)

	stored, ok := out.Messages[1].Parts[1].(pub_models.ImagePart)
	if !ok || stored.Asset != ref || stored.Data != nil {
		t.Fatalf("returned part = %#v, want the ref %s with Data nil", out.Messages[1].Parts[1], ref)
	}
	if kept := in.Messages[1].Parts[1].(pub_models.ImagePart); kept.Asset != "" || !bytes.Equal(kept.Data, img) {
		t.Fatalf("Save mutated the caller's chat: %#v", kept)
	}
}

func TestPersister_SavePutFails(t *testing.T) {
	confDir := t.TempDir()
	convDir := conversationsDir(confDir)
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	putErr := errors.New("disk full")
	p := NewPersister(confDir, &stubStore{Memory: assettest.NewMemory(), putErr: putErr})
	textOnly := pub_models.Chat{ID: "kept", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if _, err := p.Save(textOnly); err != nil {
		t.Fatalf("Save text-only: %v", err)
	}
	path := filepath.Join(convDir, "kept.json")
	before := mustRead(t, path)
	indexBefore := mustRead(t, chatIndexPath(convDir))

	_, err := p.Save(imageChat("kept", testPNG(t, 2)))
	if !errors.Is(err, putErr) {
		t.Fatalf("Save error = %v, want the Put error", err)
	}
	if after := mustRead(t, path); !bytes.Equal(after, before) {
		t.Fatalf("chat file changed after a failed Put:\n%s", after)
	}
	if after := mustRead(t, chatIndexPath(convDir)); !bytes.Equal(after, indexBefore) {
		t.Fatal("chat index changed after a failed Put")
	}
}

func TestPersister_GroupKeyAndIndex(t *testing.T) {
	p, _, convDir, _ := newDiskPersister(t)
	img := testPNG(t, 3)
	ref := pngRef(img)
	label := "[image " + ref[:6] + "]"

	imageOnly := pub_models.Chat{ID: "image-only", Messages: []pub_models.Message{
		{Role: "user", Parts: []pub_models.Part{pending(img)}},
	}}
	out, err := p.Save(imageOnly)
	if err != nil {
		t.Fatalf("Save image-only: %v", err)
	}
	if out.GroupKey == "" || out.GroupKey != ComputeGroupKeyFromText(label) {
		t.Fatalf("image-only GroupKey = %q, want the key of %q", out.GroupKey, label)
	}
	row := indexRow(t, convDir, "image-only")
	if row.FirstUserMessage != label || row.GroupKey != out.GroupKey {
		t.Fatalf("index row = %q / %q, want %q / %q", row.FirstUserMessage, row.GroupKey, label, out.GroupKey)
	}

	mixed, err := p.Save(imageChat("mixed", img))
	if err != nil {
		t.Fatalf("Save mixed: %v", err)
	}
	if want := ComputeGroupKeyFromText("look" + label); mixed.GroupKey != want {
		t.Fatalf("mixed GroupKey = %q, want the key of the concatenated Describe %q", mixed.GroupKey, want)
	}
	if row := indexRow(t, convDir, "mixed"); row.FirstUserMessage != "look\n"+label {
		t.Fatalf("mixed FirstUserMessage = %q, want the message Describe", row.FirstUserMessage)
	}

	text, err := p.Save(pub_models.Chat{ID: "text", Messages: []pub_models.Message{{Role: "user", Content: "hello there"}}})
	if err != nil {
		t.Fatalf("Save text-only: %v", err)
	}
	const preWorklogKey = "12998c017066eb0d2a70b94e6ed3192985855ce390f321bbdb832022888bd251"
	if text.GroupKey != preWorklogKey {
		t.Fatalf("text-only GroupKey = %q, want the pre-worklog %q", text.GroupKey, preWorklogKey)
	}
	if row := indexRow(t, convDir, "text"); row.FirstUserMessage != "hello there" || row.GroupKey != preWorklogKey {
		t.Fatalf("text-only index row = %+v", row)
	}
}

func TestPersister_LegacyMigratesOnSave(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	path := filepath.Join(convDir, "legacy-image.json")
	copyLegacyFixture(t, path)
	before := mustRead(t, path)

	c, err := p.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	img, ok := c.Messages[1].Parts[0].(pub_models.ImagePart)
	if !ok || img.Asset != "" || len(img.Data) == 0 || img.MIME != "image/png" || img.Origin != pub_models.OriginUser {
		t.Fatalf("legacy part = %#v, want a pending user PNG", c.Messages[1].Parts[0])
	}
	if !bytes.Equal(mustRead(t, path), before) {
		t.Fatal("Load rewrote the legacy file")
	}
	assertAssets(t, assetDir)

	if _, err := p.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ref := pngRef(img.Data)
	assertRefsOnly(t, path, ref)
	assertAssets(t, assetDir, ref)
	again, err := p.Load(path)
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if got := again.Messages[1].Parts[0].(pub_models.ImagePart); got.Asset != ref || got.Data != nil {
		t.Fatalf("migrated part = %#v, want ref %s", got, ref)
	}
}

func TestPersister_LegacyMIMEMismatch(t *testing.T) {
	p, _, convDir, _ := newDiskPersister(t)

	mismatch := filepath.Join(convDir, "mismatch.json")
	writeLegacyChat(t, mismatch, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(testPNG(t, 4)))
	c, err := p.Load(mismatch)
	var mm *asset.MIMEMismatchError
	if !errors.As(err, &mm) || mm.Declared != "image/jpeg" || mm.Sniffed != "image/png" {
		t.Fatalf("Load error = %v, want *asset.MIMEMismatchError{jpeg, png}", err)
	}
	if !strings.Contains(err.Error(), mismatch) || !strings.Contains(err.Error(), "message 1") {
		t.Fatalf("error %q does not name the path and message index", err)
	}
	if len(c.Messages) != 0 {
		t.Fatalf("Load returned a chat alongside its error: %+v", c)
	}

	notImage := filepath.Join(convDir, "not-image.json")
	writeLegacyChat(t, notImage, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("plain text")))
	c, err = p.Load(notImage)
	if !errors.Is(err, asset.ErrNotImage) {
		t.Fatalf("Load error = %v, want asset.ErrNotImage", err)
	}
	if !strings.Contains(err.Error(), notImage) || !strings.Contains(err.Error(), "message 1") {
		t.Fatalf("error %q does not name the path and message index", err)
	}
	if len(c.Messages) != 0 {
		t.Fatalf("Load returned a chat alongside its error: %+v", c)
	}
}

func TestFreeWriters_StorePending(t *testing.T) {
	confDir := t.TempDir()
	convDir := conversationsDir(confDir)
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(convDir, "assets")
	path := filepath.Join(convDir, "legacy-image.json")
	copyLegacyFixture(t, path)
	c, err := FromPath(path)
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	ref := pngRef(c.Messages[1].Parts[0].(pub_models.ImagePart).Data)

	if err := Save(convDir, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertRefsOnly(t, path, ref)

	summarized := c
	summarized.ID = "summarized"
	if err := SaveWithoutIndex(convDir, summarized); err != nil {
		t.Fatalf("SaveWithoutIndex: %v", err)
	}
	assertRefsOnly(t, filepath.Join(convDir, "summarized.json"), ref)

	if err := SaveAsPreviousQuery(confDir, c); err != nil {
		t.Fatalf("SaveAsPreviousQuery with id: %v", err)
	}
	fresh := c
	fresh.ID = ""
	if err := SaveAsPreviousQuery(confDir, fresh); err != nil {
		t.Fatalf("SaveAsPreviousQuery without id: %v", err)
	}

	globalPath := filepath.Join(convDir, globalScopeFile)
	copyLegacyFixture(t, globalPath)
	if _, err := LoadGlobalScope(confDir); err != nil {
		t.Fatalf("LoadGlobalScope id rewrite: %v", err)
	}

	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		written++
		assertRefsOnly(t, filepath.Join(convDir, e.Name()), ref)
	}
	if written != 4 {
		t.Fatalf("found %d conversation files, want legacy, summarized, promoted and globalScope", written)
	}
	assertAssets(t, assetDir, ref)
}

func TestPersister_LoadKeepsRefs(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	if _, err := p.Save(imageChat("refs", testPNG(t, 5))); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ref := assetNames(t, assetDir)[0]
	if err := os.Remove(filepath.Join(assetDir, ref)); err != nil {
		t.Fatal(err)
	}

	c, err := p.Load(filepath.Join(convDir, "refs.json"))
	if err != nil {
		t.Fatalf("Load with a missing asset: %v", err)
	}
	if got := c.Messages[1].Parts[1].(pub_models.ImagePart); got.Asset != ref || got.Data != nil {
		t.Fatalf("part = %#v, want the ref %s without bytes", got, ref)
	}
}

func TestPersister_LoadCodecError(t *testing.T) {
	p, _, convDir, _ := newDiskPersister(t)

	remote := filepath.Join(convDir, "remote.json")
	writeLegacyChat(t, remote, "https://example.com/cat.png")
	_, err := p.Load(remote)
	if !errors.Is(err, pub_models.ErrLegacyImageURL) || !strings.Contains(err.Error(), remote) {
		t.Fatalf("Load error = %v, want ErrLegacyImageURL naming %s", err, remote)
	}

	unknown := filepath.Join(convDir, "unknown.json")
	body := `{"id":"unknown","messages":[{"role":"user","content":[{"type":"audio"}]}]}`
	if err := os.WriteFile(unknown, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.Load(unknown)
	if !errors.Is(err, pub_models.ErrUnknownPartType) || !strings.Contains(err.Error(), unknown) {
		t.Fatalf("Load error = %v, want ErrUnknownPartType naming %s", err, unknown)
	}
}

func TestPersister_PromoteShares(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	img := testPNG(t, 6)
	ref := pngRef(img)

	if err := p.Promote(imageChat("", img)); err != nil {
		t.Fatalf("Promote without id: %v", err)
	}
	files := conversationFiles(t, convDir)
	if len(files) != 2 || !slices.Contains(files, globalScopeFile) {
		t.Fatalf("conversation files = %v, want globalScope and one promoted copy", files)
	}
	for _, f := range files {
		assertRefsOnly(t, filepath.Join(convDir, f), ref)
	}
	assertAssets(t, assetDir, ref)

	if err := p.Promote(imageChat("kept", img)); err != nil {
		t.Fatalf("Promote with id: %v", err)
	}
	if files := conversationFiles(t, convDir); len(files) != 3 || !slices.Contains(files, "kept.json") {
		t.Fatalf("conversation files = %v, want the chat's own file and no new promoted copy", files)
	}
	assertRefsOnly(t, filepath.Join(convDir, "kept.json"), ref)
	assertRefsOnly(t, filepath.Join(convDir, globalScopeFile), ref)
	assertAssets(t, assetDir, ref)
}

func conversationFiles(t *testing.T, convDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestPersister_DeleteSweeps(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	x, y := testPNG(t, 7), testPNG(t, 8)
	refX, refY := pngRef(x), pngRef(y)
	if _, err := p.Save(imageChat("a", x, y)); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if _, err := p.Save(imageChat("b", x)); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	global := imageChat("", x)
	global.Messages = global.Messages[1:]
	if err := p.Promote(global); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	assertAssets(t, assetDir, refX, refY)

	n, err := p.Delete("a")
	if err != nil || n != 1 {
		t.Fatalf("Delete a = %d, %v; want 1, nil", n, err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "a.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a.json still exists: %v", err)
	}
	assertAssets(t, assetDir, refX)

	if n, err := p.Delete("b"); err != nil || n != 0 {
		t.Fatalf("Delete b = %d, %v; want 0 while globalScope holds X", n, err)
	}
	assertAssets(t, assetDir, refX)

	if n, err := p.Delete(globalScopeChatID); err != nil || n != 1 {
		t.Fatalf("Delete globalScope = %d, %v; want 1", n, err)
	}
	assertAssets(t, assetDir)
}

func TestPersister_DeleteSweepFails(t *testing.T) {
	confDir := t.TempDir()
	convDir := conversationsDir(confDir)
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sweepErr := errors.New("permission denied")
	p := NewPersister(confDir, &stubStore{Memory: assettest.NewMemory(), sweepErr: sweepErr})
	if _, err := p.Save(imageChat("doomed", testPNG(t, 9))); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := p.Delete("doomed")
	var sf *SweepFailedError
	if !errors.As(err, &sf) || sf.ChatID != "doomed" || !errors.Is(err, sweepErr) {
		t.Fatalf("Delete error = %v, want *SweepFailedError wrapping the sweep error", err)
	}
	if !strings.Contains(err.Error(), "doomed") || !strings.Contains(err.Error(), sweepErr.Error()) {
		t.Fatalf("error %q does not name the chat and its cause", err)
	}
	if _, err := os.Stat(filepath.Join(convDir, "doomed.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the chat file was restored or kept: %v", err)
	}

	disk, _, diskConv, assetDir := newDiskPersister(t)
	kept, gone := testPNG(t, 12), testPNG(t, 13)
	if _, err := disk.Save(imageChat("kept", kept)); err != nil {
		t.Fatalf("Save kept: %v", err)
	}
	if _, err := disk.Save(imageChat("gone", gone)); err != nil {
		t.Fatalf("Save gone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(diskConv, "corrupt.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Delete("gone"); !errors.As(err, &sf) || sf.ChatID != "gone" {
		t.Fatalf("Delete beside an unreadable conversation = %v, want *SweepFailedError", err)
	}
	assertAssets(t, assetDir, pngRef(kept), pngRef(gone))
}

// blockReasoningSidecars puts a file where the reasoning directory belongs, so
// removing any chat's sidecar fails.
func blockReasoningSidecars(t *testing.T, convDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(convDir, "reasoning"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPersister_DeleteSidecarFails(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	kept, orphan := testPNG(t, 15), testPNG(t, 16)
	if _, err := p.Save(imageChat("kept", kept)); err != nil {
		t.Fatalf("Save kept: %v", err)
	}
	if _, err := p.Save(imageChat("doomed", orphan)); err != nil {
		t.Fatalf("Save doomed: %v", err)
	}
	blockReasoningSidecars(t, convDir)

	n, err := p.Delete("doomed")
	var sidecar *sidecarNotRemovedError
	if !errors.As(err, &sidecar) || sidecar.ChatID != "doomed" || sidecar.Cause == nil {
		t.Fatalf("Delete error = %v, want *sidecarNotRemovedError for doomed", err)
	}
	var sf *SweepFailedError
	if errors.As(err, &sf) || errors.Is(err, ErrChatNotFound) {
		t.Fatalf("Delete error = %v, want only the sidecar failure", err)
	}
	if !strings.Contains(err.Error(), "doomed") || !strings.Contains(err.Error(), sidecar.Cause.Error()) {
		t.Fatalf("error %q does not name the chat and its cause", err)
	}
	if n != 1 {
		t.Fatalf("Delete swept %d assets, want the orphan", n)
	}
	if _, err := os.Stat(filepath.Join(convDir, "doomed.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("doomed.json still exists: %v", err)
	}
	assertAssets(t, assetDir, pngRef(kept))

	sweepErr := errors.New("permission denied")
	failing := NewPersister(filepath.Dir(convDir), &stubStore{Memory: assettest.NewMemory(), sweepErr: sweepErr})
	_, err = failing.Delete("kept")
	if !errors.As(err, &sf) || !errors.Is(err, sweepErr) || !errors.As(err, &sidecar) {
		t.Fatalf("Delete with both failing = %v, want *SweepFailedError and the sidecar failure", err)
	}
}

func TestPersister_DeleteUnknown(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(conversationsDir(confDir), 0o755); err != nil {
		t.Fatal(err)
	}
	store := &stubStore{Memory: assettest.NewMemory()}
	p := NewPersister(confDir, store)
	for _, id := range []string{"missing", "../escape", ""} {
		if _, err := p.Delete(id); !errors.Is(err, ErrChatNotFound) {
			t.Fatalf("Delete(%q) error = %v, want ErrChatNotFound", id, err)
		}
	}
	if store.sweeps != 0 {
		t.Fatalf("Sweep ran %d times for unknown ids", store.sweeps)
	}
}

func TestPersister_ForeignCloneStored(t *testing.T) {
	img := testPNG(t, 10)
	ref := pngRef(img)
	root := filepath.Join(t.TempDir(), "projects")
	session := strings.Join([]string{
		`{"type":"user","sessionId":"shot","cwd":"/work","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[` +
			`{"type":"text","text":"see this screenshot"},` +
			`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(img) + `"}}]}}`,
		`{"type":"assistant","sessionId":"shot","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"a red pixel"}]}}`,
	}, "\n") + "\n"
	if err := os.MkdirAll(filepath.Join(root, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proj", "shot.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}

	cq, confDir := newTestHandler(t)
	assetDir := filepath.Join(cq.convDir, "assets")
	foreign, err := anthropic.SourceReader{Root: root}.Read(context.Background(), nil, "shot")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if img, ok := foreign.Messages[1].Parts[1].(pub_models.ImagePart); !ok || img.Asset != "" {
		t.Fatalf("foreign part = %#v, want a pending image", foreign.Messages[1].Parts[1])
	}
	assertAssets(t, assetDir)

	clone := foreign
	clone.ID = "clone"
	p := NewPersister(confDir, asset.NewStore(assetDir))
	if _, err := p.Save(clone); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertRefsOnly(t, filepath.Join(cq.convDir, "clone.json"), ref)
	assertAssets(t, assetDir, ref)

	cloned, err := cq.cloneForeignChat(foreign)
	if err != nil {
		t.Fatalf("cloneForeignChat: %v", err)
	}
	assertRefsOnly(t, filepath.Join(cq.convDir, cloned.ID+".json"), ref)
	assertAssets(t, assetDir, ref)
}

func TestPersister_TextOnlyUnchanged(t *testing.T) {
	plain, tools := textOnlyChats()

	p, _, convDir, assetDir := newDiskPersister(t)
	for _, c := range []pub_models.Chat{plain, tools} {
		if _, err := p.Save(c); err != nil {
			t.Fatalf("Save %s: %v", c.ID, err)
		}
	}
	if got := snapshotConvDir(t, convDir, plain.ID, tools.ID); got != readGolden(t, "save.golden") {
		t.Fatalf("Save output differs from the pre-worklog golden:\n%s", got)
	}
	assertNoAssetDir(t, assetDir)

	p, _, convDir, assetDir = newDiskPersister(t)
	fresh := plain
	fresh.ID = ""
	if err := p.Promote(fresh); err != nil {
		t.Fatalf("Promote without id: %v", err)
	}
	if got := snapshotConvDir(t, convDir); got != readGolden(t, "promote-new.golden") {
		t.Fatalf("Promote output differs from the pre-worklog golden:\n%s", got)
	}
	assertNoAssetDir(t, assetDir)

	p, _, convDir, assetDir = newDiskPersister(t)
	if err := p.Promote(tools); err != nil {
		t.Fatalf("Promote with id: %v", err)
	}
	if got := snapshotConvDir(t, convDir, tools.ID); got != readGolden(t, "promote-existing.golden") {
		t.Fatalf("Promote output differs from the pre-worklog golden:\n%s", got)
	}
	assertNoAssetDir(t, assetDir)
}

func assertNoAssetDir(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a text-only write touched %s: %v", dir, err)
	}
}

func TestPersister_IndexSkipsAssets(t *testing.T) {
	p, _, convDir, assetDir := newDiskPersister(t)
	if _, err := p.Save(imageChat("indexed", testPNG(t, 11))); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "stray.json"), []byte(`{"id":"stray"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(chatIndexPath(convDir)); err != nil {
		t.Fatal(err)
	}

	rows, err := rebuildChatIndex(convDir, 0, "test")
	if err != nil {
		t.Fatalf("rebuildChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "indexed" {
		t.Fatalf("index rows = %+v, want only the conversation", rows)
	}
}
