package text

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func pngOf(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.Pix[0] = shade
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func handleOf(t *testing.T, data []byte) string {
	t.Helper()
	h, err := pub_models.ImagePart{Data: data}.Handle()
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	return h
}

func nativeCaps() models.ImageCapabilities {
	return models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: models.ToolResultNative, MIMEs: []string{"image/png"}}
}

func fallbackCaps() models.ImageCapabilities {
	return models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: models.ToolResultFallback, MIMEs: []string{"image/png"}}
}

// turnModel is a scripted ImageEncoder: request n streams steps[n], and every request
// passes models.CheckImageParts first, as a real encoder does.
type turnModel struct {
	mu       sync.Mutex
	caps     models.ImageCapabilities
	steps    [][]models.CompletionEvent
	requests []pub_models.Chat
	tools    []string
}

func (m *turnModel) Setup() error { return nil }

func (m *turnModel) ImageCapabilities() models.ImageCapabilities { return m.caps }

func (m *turnModel) RegisterTool(tool pub_models.LLMTool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = append(m.tools, tool.Specification().Name)
}

func (m *turnModel) StreamCompletions(_ context.Context, c pub_models.Chat) (chan models.CompletionEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := models.CheckImageParts(m.caps, "turn", "turn", c.Messages); err != nil {
		return nil, err
	}
	n := len(m.requests)
	m.requests = append(m.requests, c)
	events := []models.CompletionEvent{"done", models.StopEvent{}}
	if n < len(m.steps) {
		events = m.steps[n]
	}
	ch := make(chan models.CompletionEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (m *turnModel) received() []pub_models.Chat {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.requests)
}

func (m *turnModel) registered() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.tools)
}

type turnCounter struct {
	*turnModel
	counted []pub_models.Chat
}

func (c *turnCounter) CountInputTokens(_ context.Context, chat pub_models.Chat) (int, error) {
	c.counted = append(c.counted, chat)
	return 1, nil
}

// recordingPersister is the real chat.Persister over its own config dir, recording each Promote.
type recordingPersister struct {
	chat.Persister
	promoted []pub_models.Chat
}

func newRecordingPersister(confDir string, store asset.AssetStorer) *recordingPersister {
	return &recordingPersister{Persister: chat.NewPersister(confDir, store)}
}

func (p *recordingPersister) Promote(c pub_models.Chat) error {
	p.promoted = append(p.promoted, c)
	return p.Persister.Promote(c)
}

type failingPutStore struct {
	*assettest.Memory
	err error
}

func (s failingPutStore) Put([]byte, string) (string, error) { return "", s.err }

type turnOpener struct {
	paths []string
	err   error
}

func (o *turnOpener) Open(path string) error {
	o.paths = append(o.paths, path)
	return o.err
}

func pngGenerator(data []byte) *imagegentest.Fake {
	return &imagegentest.Fake{Images: []imagegen.GeneratedImage{{Image: asset.Image{Data: data, MIME: "image/png"}}}}
}

// partsTool is an MCP-like PartsCaller returning its fixed parts, or the non-nil error
// fail returns for the input.
type partsTool struct {
	name  string
	parts []pub_models.Part
	fail  func(pub_models.Input) error
}

func (p partsTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: p.name}
}

func (p partsTool) Call(in pub_models.Input) (string, error) {
	parts, err := p.CallParts(context.Background(), in)
	if err != nil {
		return "", err
	}
	return pub_models.Message{Parts: parts}.Describe(), nil
}

func (p partsTool) CallParts(_ context.Context, in pub_models.Input) ([]pub_models.Part, error) {
	if p.fail != nil {
		if err := p.fail(in); err != nil {
			return nil, err
		}
	}
	return p.parts, nil
}

func call(id, name string, in pub_models.Input) pub_models.Call {
	return pub_models.Call{ID: id, Name: name, Inputs: &in}
}

// turnFixture is one querier, its session runner and its output.
type turnFixture struct {
	configDir string
	out       *strings.Builder
	stderr    *strings.Builder
	q         *Querier[models.StreamCompleter]
}

func newTurnFixture(t *testing.T, model models.StreamCompleter, tools ...pub_models.LLMTool) *turnFixture {
	t.Helper()
	f := &turnFixture{configDir: t.TempDir(), out: &strings.Builder{}, stderr: &strings.Builder{}}
	run := map[string]pub_models.LLMTool{}
	for _, tool := range tools {
		run[tool.Specification().Name] = tool
	}
	f.q = &Querier[models.StreamCompleter]{
		Raw:       true,
		out:       f.out,
		configDir: f.configDir,
		Model:     model,
		tooling:   tooling{run: run},
	}
	return f
}

// diskTurn wires the real children over a disk store in the fixture's config dir.
func (f *turnFixture) diskTurn(opts present.Options) *ImageTurn {
	store := asset.NewStore(filepath.Join(f.configDir, "conversations", "assets"))
	if opts.Out == nil {
		opts.Out = f.out
	}
	turn := &ImageTurn{
		Store:      store,
		Normalizer: imagenorm.New(store),
		Presenter:  present.New(opts),
		Persister:  chat.NewPersister(f.configDir, store),
		Stderr:     f.stderr,
	}
	f.q.imageTurn = turn
	return turn
}

func (f *turnFixture) run(t *testing.T, msgs ...pub_models.Message) (*QuerySession, error) {
	t.Helper()
	session := &QuerySession{Chat: pub_models.Chat{Messages: msgs}, ShouldSaveReply: true, Raw: f.q.rawDisplay()}
	runner := sessionRunner[models.StreamCompleter]{
		querier:      f.q,
		finalizer:    sessionFinalizer[models.StreamCompleter]{querier: f.q},
		toolExecutor: toolExecutor[models.StreamCompleter]{querier: f.q},
	}
	return session, runner.Run(context.Background(), session)
}

func (f *turnFixture) assetPath(data []byte, ext string) string {
	h, _ := pub_models.ImagePart{Data: data}.Handle()
	entries, _ := os.ReadDir(filepath.Join(f.configDir, "conversations", "assets"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), h) && strings.HasSuffix(e.Name(), "."+ext) {
			return filepath.Join(f.configDir, "conversations", "assets", e.Name())
		}
	}
	return ""
}

func (f *turnFixture) assets(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.configDir, "conversations", "assets"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func (f *turnFixture) promoted(t *testing.T) pub_models.Chat {
	t.Helper()
	p := filepath.Join(f.configDir, "conversations", "globalScope.json")
	c, err := chat.NewPersister(f.configDir, assettest.NewMemory()).Load(p)
	if err != nil {
		t.Fatalf("load promoted chat: %v", err)
	}
	return c
}

func lineCount(out, line string) int {
	n := 0
	for l := range strings.SplitSeq(out, "\n") {
		if l == line {
			n++
		}
	}
	return n
}

func imagesOf(parts []pub_models.Part) []pub_models.ImagePart {
	var out []pub_models.ImagePart
	for _, p := range parts {
		if img, ok := p.(pub_models.ImagePart); ok {
			out = append(out, img)
		}
	}
	return out
}

func texts(parts []pub_models.Part) []string {
	var out []string
	for _, p := range parts {
		if tp, ok := p.(pub_models.TextPart); ok {
			out = append(out, tp.Text)
		}
	}
	return out
}

func lastOfRole(t *testing.T, c pub_models.Chat, role string) pub_models.Message {
	t.Helper()
	m, _, err := c.LastOfRole(role)
	if err != nil {
		t.Fatalf("no %s message in %+v", role, c.Messages)
	}
	return m
}

func TestTurn_DefaultImageTurn(t *testing.T) {
	toolPNG, genPNG := pngOf(t, 10), pngOf(t, 20)
	snap := partsTool{name: "snap", parts: []pub_models.Part{
		pub_models.TextPart{Text: "snapshot"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: toolPNG},
	}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "snap", nil), models.StopEvent{}},
		{"here", models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, snap)

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "snap it"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	toolAsset, genAsset := f.assetPath(toolPNG, "png"), f.assetPath(genPNG, "png")
	if toolAsset == "" || genAsset == "" {
		t.Fatalf("want both images under <configDir>/conversations/assets, got %v", f.assets(t))
	}
	requests := model.received()
	if len(requests) != 2 {
		t.Fatalf("want 2 requests, got %d", len(requests))
	}
	toolMsg := lastOfRole(t, requests[1], "tool")
	want := []string{"snapshot", "[image " + handleOf(t, toolPNG) + "]"}
	if got := texts(toolMsg.Parts); !slices.Equal(got, want) {
		t.Errorf("request 2 tool texts = %q, want %q", got, want)
	}
	if imgs := imagesOf(toolMsg.Parts); len(imgs) != 1 || !bytes.Equal(imgs[0].Data, toolPNG) {
		t.Errorf("request 2 tool image = %+v, want the loaded tool image", imgs)
	}
	promoted := f.promoted(t)
	stored := imagesOf(lastOfRole(t, promoted, "tool").Parts)
	if len(stored) != 1 || stored[0].Asset != filepath.Base(toolAsset) || stored[0].Data != nil {
		t.Errorf("promoted tool image = %+v, want ref %s", stored, filepath.Base(toolAsset))
	}
	if n := lineCount(f.out.String(), genAsset); n != 1 {
		t.Errorf("want the generated image presented once as its bare path, got %d in %q", n, f.out.String())
	}
	if n := lineCount(f.out.String(), toolAsset); n != 0 {
		t.Errorf("a tool-origin image must not be presented: %q", f.out.String())
	}
}

func TestTurn_StepStartPutFails(t *testing.T) {
	model := &turnModel{caps: nativeCaps()}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	putErr := errors.New("disk full")
	turn.Store = failingPutStore{Memory: assettest.NewMemory(), err: putErr}

	_, err := f.run(t, pub_models.Message{Role: "user", Parts: []pub_models.Part{
		pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: pngOf(t, 1)},
	}})
	if !errors.Is(err, putErr) {
		t.Fatalf("Run err = %v, want the store error", err)
	}
	if n := len(model.received()); n != 0 {
		t.Fatalf("want zero requests, got %d", n)
	}
}

func TestTurn_NoGenerator(t *testing.T) {
	model := &turnModel{caps: nativeCaps()}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	turn.Generation = ImageGenSetup{Requested: true}

	_, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if !errors.Is(err, ErrNoImageGenerator) {
		t.Fatalf("Run err = %v, want ErrNoImageGenerator", err)
	}
	if n := len(model.received()); n != 0 {
		t.Fatalf("want zero requests, got %d", n)
	}
}

func TestTurn_IncompleteImageTurn(t *testing.T) {
	model := &turnModel{caps: nativeCaps()}
	f := newTurnFixture(t, model)
	f.q.imageTurn = &ImageTurn{}

	_, err := f.run(t, pub_models.Message{Role: "user", Content: "hi"})
	if !errors.Is(err, errIncompleteImageTurn) {
		t.Fatalf("Run err = %v, want errIncompleteImageTurn", err)
	}
	for _, member := range []string{"Store", "Normalizer", "Presenter", "Persister", "Stderr"} {
		if !strings.Contains(err.Error(), member) {
			t.Errorf("err = %v, want it to name %s", err, member)
		}
	}
	if n := len(model.received()); n != 0 {
		t.Fatalf("want zero requests, got %d", n)
	}
}

func TestTurn_TextOnlyUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
	}{{"nil ImageTurn", false}, {"set ImageTurn", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var session pub_models.Chat
			run := runTextOnlyTurn(t, func(q *Querier[*anthropic.Claude], configDir string) {
				if !tc.set {
					return
				}
				store := asset.NewStore(filepath.Join(configDir, "conversations", "assets"))
				q.imageTurn = &ImageTurn{
					Store:      store,
					Normalizer: imagenorm.New(store),
					Presenter:  present.New(present.Options{Out: q.out}),
					Persister:  chat.NewPersister(configDir, store),
					Stderr:     io.Discard,
				}
			}, &session)
			if len(run.bodies) != 2 {
				t.Fatalf("want 2 requests, got %d", len(run.bodies))
			}
			assertTextOnlyGolden(t, run)
			toolMsg := lastOfRole(t, session, "tool")
			if toolMsg.Parts != nil || toolMsg.Content != "a.txt" {
				t.Errorf("tool message = %+v, want Content only", toolMsg)
			}
		})
	}
}

func TestTurn_PartsCallerWithoutImageIsContent(t *testing.T) {
	textOnly := partsTool{name: "snap", parts: []pub_models.Part{pub_models.TextPart{Text: "a"}, pub_models.TextPart{Text: "b"}}}
	errBroke := errors.New("snap broke")
	broken := partsTool{name: "snap", fail: func(pub_models.Input) error { return errBroke }}
	_, callErr := broken.Call(nil)
	if _, partsErr := broken.CallParts(t.Context(), nil); callErr != errBroke || partsErr != errBroke {
		t.Fatalf("Call err %v, CallParts err %v; want %v unchanged from both (CallParts.3)", callErr, partsErr, errBroke)
	}
	for _, tc := range []struct {
		name string
		tool pub_models.LLMTool
	}{{"text parts", textOnly}, {"error", broken}} {
		t.Run(tc.name, func(t *testing.T) {
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{{call("c1", "snap", nil), models.StopEvent{}}}}
			f := newTurnFixture(t, model, tc.tool)
			f.diskTurn(present.Options{})
			session, err := f.run(t, pub_models.Message{Role: "user", Content: "snap"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			want := tools.InvokeWith(context.Background(), call("c1", "snap", nil), map[string]pub_models.LLMTool{"snap": tc.tool})
			toolMsg := lastOfRole(t, session.Chat, "tool")
			if toolMsg.Parts != nil || toolMsg.Content != want {
				t.Errorf("tool message = %+v, want Content %q", toolMsg, want)
			}
		})
	}
}

func TestTurn_AttachmentHandleResolves(t *testing.T) {
	attachment := pngOf(t, 30)
	gen := pngGenerator(pngOf(t, 31))
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "make it blue", "input_images": []any{handleOf(t, attachment)}}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New

	session, err := f.run(t, pub_models.Message{Role: "user", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "edit this"},
		pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: attachment},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	toolMsg := lastOfRole(t, session.Chat, "tool")
	if strings.Contains(toolMsg.Describe(), "ERROR") {
		t.Fatalf("generate_image failed: %s", toolMsg.Describe())
	}
	if requests := gen.Requests(); len(requests) != 1 || len(requests[0].Inputs) != 1 || !bytes.Equal(requests[0].Inputs[0].Data, attachment) {
		t.Fatalf("generator requests = %+v, want the attachment's bytes", requests)
	}
}

func TestTurn_NativeImagePersisted(t *testing.T) {
	genPNG := pngOf(t, 40)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{"Here it is.", models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	opener := &turnOpener{}
	f.diskTurn(present.Options{Opener: opener})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.assets(t); len(got) != 1 {
		t.Fatalf("want one asset, got %v", got)
	}
	path := f.assetPath(genPNG, "png")
	if n := lineCount(f.out.String(), path); n != 1 {
		t.Errorf("want one presented line, got %d in %q", n, f.out.String())
	}
	if !strings.Contains(f.out.String(), "Here it is.\n"+path+"\n") {
		t.Errorf("want the line on its own row after the text, got %q", f.out.String())
	}
	msg := lastOfRole(t, f.promoted(t), "assistant")
	if msg.Content != "" || len(msg.Parts) != 2 {
		t.Fatalf("assistant message = %+v, want [TextPart, ImagePart]", msg)
	}
	if tp, ok := msg.Parts[0].(pub_models.TextPart); !ok || tp.Text != "Here it is." {
		t.Errorf("part 0 = %+v, want the text", msg.Parts[0])
	}
	img, ok := msg.Parts[1].(pub_models.ImagePart)
	if !ok || img.Origin != pub_models.OriginGenerated || img.Asset != filepath.Base(path) || img.MIME != "image/png" || img.Data != nil {
		t.Errorf("part 1 = %+v, want the stored generated image", msg.Parts[1])
	}
	raw, err := os.ReadFile(filepath.Join(f.configDir, "conversations", "globalScope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(genPNG))) {
		t.Error("the persisted chat holds image bytes")
	}
}

func TestTurn_ImagesInArrivalOrder(t *testing.T) {
	first, second := pngOf(t, 41), pngOf(t, 42)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{
			models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: first}},
			"between",
			models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: second}},
			"after",
			models.StopEvent{},
		},
	}}
	f := newTurnFixture(t, model)
	f.diskTurn(present.Options{})
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw two"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	msg := lastOfRole(t, session.Chat, "assistant")
	got := make([]string, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		got = append(got, p.Describe())
	}
	want := []string{"[image " + handleOf(t, first) + "]", "between", "[image " + handleOf(t, second) + "]", "after"}
	if !slices.Equal(got, want) {
		t.Errorf("assistant parts = %q, want %q", got, want)
	}
}

func TestTurn_ImageOnlyReply(t *testing.T) {
	genPNG := pngOf(t, 43)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.diskTurn(present.Options{})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msg := lastOfRole(t, f.promoted(t), "assistant")
	if imgs := imagesOf(msg.Parts); len(msg.Parts) != 1 || len(imgs) != 1 {
		t.Errorf("assistant message = %+v, want the image alone", msg)
	}
}

func TestTurn_NativeImageWithToolCall(t *testing.T) {
	genPNG := pngOf(t, 44)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, call("c1", "ls", nil), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, fixedTool{name: "ls", out: "a.txt"})
	f.diskTurn(present.Options{})
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw, then list"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var carrier pub_models.Message
	for _, m := range session.Chat.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			carrier = m
		}
	}
	if imgs := imagesOf(carrier.Parts); len(imgs) != 1 || imgs[0].Asset == "" {
		t.Fatalf("tool-call message = %+v, want it to carry the stored image", carrier)
	}
	second := model.received()[1]
	if !strings.Contains(lastOfRole(t, second, "user").Describe(), "[images from the previous turn]") {
		t.Errorf("request 2 must carry the assistant image in a synthetic user message: %+v", second.Messages)
	}
}

func TestTurn_PendingGeneratedToolImage(t *testing.T) {
	genPNG := pngOf(t, 52)
	drawer := partsTool{name: "drawer", parts: []pub_models.Part{
		pub_models.TextPart{Text: "drew"},
		pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG},
	}}
	steps := [][]models.CompletionEvent{{call("c1", "drawer", nil), models.StopEvent{}}}

	t.Run("stored, presented and prefixed", func(t *testing.T) {
		model := &turnModel{caps: nativeCaps(), steps: steps}
		f := newTurnFixture(t, model, drawer)
		f.q.tooling.maxCalls = new(5)
		f.diskTurn(present.Options{})
		session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		path := f.assetPath(genPNG, "png")
		if lineCount(f.out.String(), path) != 1 {
			t.Errorf("want the generated image presented once, got %q", f.out.String())
		}
		msg := lastOfRole(t, session.Chat, "tool")
		if got := texts(msg.Parts); len(got) != 2 || !strings.HasPrefix(got[0], "[ Tool calls remaining: 5 ]") || got[1] != "drew" {
			t.Errorf("tool texts = %q, want the budget prefix, then the tool's text", got)
		}
		if imgs := imagesOf(msg.Parts); len(imgs) != 1 || imgs[0].Asset != filepath.Base(path) || imgs[0].Data != nil {
			t.Errorf("tool image = %+v, want it stored", imgs)
		}
	})

	t.Run("store fails", func(t *testing.T) {
		putErr := errors.New("disk full")
		model := &turnModel{caps: nativeCaps(), steps: steps}
		f := newTurnFixture(t, model, drawer)
		f.diskTurn(present.Options{}).Store = failingPutStore{Memory: assettest.NewMemory(), err: putErr}
		if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); !errors.Is(err, putErr) {
			t.Fatalf("Run err = %v, want the store error", err)
		}
	})
}

func TestTurn_ImageLineAboveRollingWindow(t *testing.T) {
	genPNG := pngOf(t, 53)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.diskTurn(present.Options{})
	f.q.activityViewport = utils.NewActivityViewport(80, 5, 24)
	f.q.activityViewport.AppendText("working")
	if err := f.q.activityViewport.Render(f.out); err != nil {
		t.Fatal(err)
	}
	before := f.out.Len()

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := f.out.String()[before:]
	path := f.assetPath(genPNG, "png")
	line := strings.Index(after, path+"\n")
	if line < 0 {
		t.Fatalf("no image line in %q", after)
	}
	if !strings.Contains(after[:line], "\x1b[") || !strings.Contains(after[line:], "working") {
		t.Errorf("want the window cleared before the line and redrawn after it, got %q", after)
	}
}

func TestTurn_ClientToolImage(t *testing.T) {
	genPNG, viewPNG := pngOf(t, 50), pngOf(t, 51)
	gen := pngGenerator(genPNG)
	view := partsTool{name: "view_image", parts: []pub_models.Part{
		pub_models.TextPart{Text: "viewed"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: viewPNG},
	}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a fox"}), call("c2", "view_image", nil), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, view)
	turn := f.diskTurn(present.Options{})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New

	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw a fox, then look at it"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := model.registered(); !slices.Equal(got, []string{"generate_image"}) {
		t.Errorf("registered tools = %v, want generate_image", got)
	}
	genPath, viewPath := f.assetPath(genPNG, "png"), f.assetPath(viewPNG, "png")
	if n := lineCount(f.out.String(), genPath); n != 1 {
		t.Errorf("want the generated image presented once, got %d in %q", n, f.out.String())
	}
	if viewPath == "" || lineCount(f.out.String(), viewPath) != 0 {
		t.Errorf("view_image's image must be stored and never presented (path %q): %q", viewPath, f.out.String())
	}
	var genMsg pub_models.Message
	for _, m := range session.Chat.Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			genMsg = m
		}
	}
	if imgs := imagesOf(genMsg.Parts); len(imgs) != 1 || imgs[0].Origin != pub_models.OriginGenerated || imgs[0].Asset == "" {
		t.Fatalf("generate_image tool message = %+v, want Parts with the stored image", genMsg)
	}
	requests := model.received()
	if len(requests) != 2 {
		t.Fatalf("want 2 requests, got %d", len(requests))
	}
	var sent pub_models.Message
	for _, m := range requests[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			sent = m
		}
	}
	label := "[image " + handleOf(t, genPNG) + "]"
	parts := sent.Parts
	if len(parts) < 2 || parts[len(parts)-2].Describe() != label {
		t.Fatalf("request 2 tool message = %+v, want the image after its label %s", sent, label)
	}
	if img, ok := parts[len(parts)-1].(pub_models.ImagePart); !ok || !bytes.Equal(img.Data, genPNG) {
		t.Errorf("request 2 tool image = %+v, want the loaded bytes", parts[len(parts)-1])
	}
}

func TestTurn_FallbackNotPersisted(t *testing.T) {
	toolPNG := pngOf(t, 60)
	snap := partsTool{name: "snap", parts: []pub_models.Part{
		pub_models.TextPart{Text: "snapshot"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: toolPNG},
	}}
	model := &turnModel{caps: fallbackCaps(), steps: [][]models.CompletionEvent{{call("c1", "snap", nil), models.StopEvent{}}}}
	f := newTurnFixture(t, model, snap)
	f.diskTurn(present.Options{})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "snap"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	second := model.received()[1]
	synthetic := second.Messages[len(second.Messages)-1]
	if synthetic.Role != "user" || len(synthetic.Parts) == 0 || synthetic.Parts[0].Describe() != "[images from the previous turn]" {
		t.Fatalf("request 2 must end with the synthetic user message, got %+v", synthetic)
	}
	for i, m := range f.promoted(t).Messages {
		for _, text := range texts(m.Parts) {
			if strings.HasPrefix(text, "[image") || strings.HasPrefix(text, "[images") {
				t.Errorf("promoted message %d holds a label or note %q", i, text)
			}
		}
	}
	if got := len(f.promoted(t).Messages); got != 4 {
		t.Errorf("promoted chat has %d messages, want user, tool call, tool, answer", got)
	}
}

func TestTurn_CountTokensNormalized(t *testing.T) {
	data := pngOf(t, 70)
	mem := assettest.NewMemory()
	ref, err := mem.Put(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	model := &turnCounter{turnModel: &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "ls", nil), models.StopEvent{}},
	}}}
	f := newTurnFixture(t, model, fixedTool{name: "ls", out: "a.txt"})
	f.q.stoploss = &Stoploss{MaxTokens: 1 << 30}
	turn := f.diskTurn(present.Options{})
	turn.Store, turn.Normalizer = mem, imagenorm.New(mem)

	if _, err := f.run(t, pub_models.Message{Role: "user", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "what is it?"},
		pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginUser},
	}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.counted) != 1 {
		t.Fatalf("want one count, got %d", len(model.counted))
	}
	user := lastOfRole(t, model.counted[0], "user")
	if got := texts(user.Parts); !slices.Equal(got, []string{"what is it?", "[image " + ref[:6] + "]"}) {
		t.Errorf("counted user texts = %q, want the label", got)
	}
	if imgs := imagesOf(user.Parts); len(imgs) != 1 || !bytes.Equal(imgs[0].Data, data) {
		t.Errorf("counted image = %+v, want loaded bytes", imgs)
	}
}

func TestTurn_CountTokensNormalizeRefusal(t *testing.T) {
	gif := partsTool{name: "snap", parts: []pub_models.Part{
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/gif", Data: []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")},
	}}
	model := &turnCounter{turnModel: &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "snap", nil), models.StopEvent{}},
	}}}
	f := newTurnFixture(t, model, gif)
	f.q.stoploss = &Stoploss{MaxTokens: 1 << 30}
	f.diskTurn(present.Options{})

	_, err := f.run(t, pub_models.Message{Role: "user", Content: "snap"})
	var unsupported *claierr.ImageUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Run err = %v, want *claierr.ImageUnsupportedError", err)
	}
	if len(model.counted) != 0 {
		t.Errorf("the counter received %d chats, want none", len(model.counted))
	}
}

// Gen.1–2: a requested run registers the client tool on a completer with a tool box,
// and a completer without one runs as for any tool selection, without it.
func TestTurn_ImageGeneration(t *testing.T) {
	plain := &chanModel{events: make(chan models.CompletionEvent, 2)}
	plain.events <- "done"
	plain.events <- models.StopEvent{}
	f := newTurnFixture(t, plain)
	turn := f.diskTurn(present.Options{})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 81)).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("no tool box: Run: %v", err)
	}
	if _, inRun := f.q.tooling.run["generate_image"]; inRun {
		t.Error("no tool box: generate_image must not be in the toolset")
	}

	for _, requested := range []bool{true, false} {
		f := newTurnFixture(t, &turnModel{caps: nativeCaps()})
		turn := f.diskTurn(present.Options{})
		turn.Generation = ImageGenSetup{Requested: requested, Config: imagegen.GeneratorConfig{Model: "gpt-image-1"}}
		turn.NewGenerator = pngGenerator(pngOf(t, 80)).New
		model := f.q.Model.(*turnModel)

		if _, err := f.run(t, pub_models.Message{Role: "user", Content: "hi"}); err != nil {
			t.Fatalf("requested %v: Run: %v", requested, err)
		}
		registered := slices.Contains(model.registered(), "generate_image")
		_, inRun := f.q.tooling.run["generate_image"]
		if registered != requested || inRun != requested {
			t.Errorf("requested %v: generate_image registered = %v, in toolset = %v", requested, registered, inRun)
		}
		if f.stderr.Len() != 0 {
			t.Errorf("requested %v: stderr = %q, want nothing", requested, f.stderr.String())
		}
	}
}

func TestTurn_FailureKeepsImage(t *testing.T) {
	genPNG := pngOf(t, 90)
	boom := errors.New("stream broke")
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{"partial", models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, boom},
	}}
	f := newTurnFixture(t, model)
	f.diskTurn(present.Options{})

	_, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want the stream error", err)
	}
	path := f.assetPath(genPNG, "png")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the asset is gone: %v", statErr)
	}
	if lineCount(f.out.String(), path) != 1 {
		t.Errorf("want the presented line kept, got %q", f.out.String())
	}
	imgs := imagesOf(lastOfRole(t, f.promoted(t), "assistant").Parts)
	if len(imgs) != 1 || imgs[0].Asset != filepath.Base(path) {
		t.Errorf("failed-run persist = %+v, want the stored part", imgs)
	}
}

func TestTurn_PutFails(t *testing.T) {
	putErr := errors.New("disk full")
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: pngOf(t, 91)}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	turn.Store = failingPutStore{Memory: assettest.NewMemory(), err: putErr}

	_, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if !errors.Is(err, putErr) {
		t.Fatalf("Run err = %v, want the store error", err)
	}
	if strings.Contains(f.out.String(), ".png") {
		t.Errorf("nothing may be presented: %q", f.out.String())
	}
}

func TestTurn_OpenFailedNotice(t *testing.T) {
	genPNG := pngOf(t, 92)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, "done", models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	opener := &turnOpener{err: errors.New("no display")}
	f.diskTurn(present.Options{Terminal: true, AutoOpen: true, Opener: opener})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(opener.paths) != 1 {
		t.Fatalf("want one open, got %v", opener.paths)
	}
	if got := strings.Count(f.stderr.String(), "no display"); got != 1 {
		t.Errorf("stderr = %q, want one notice", f.stderr.String())
	}
}

func TestTurn_PresentErrorEndsTurn(t *testing.T) {
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: pngOf(t, 93)}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	turn.Presenter = present.New(present.Options{})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err == nil {
		t.Fatal("Run succeeded, want the Present error")
	}
}

func TestTurn_PromoteUsed(t *testing.T) {
	model := &turnModel{caps: nativeCaps()}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	persisterDir := t.TempDir()
	persister := newRecordingPersister(persisterDir, turn.Store)
	turn.Persister = persister

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(persister.promoted) != 1 {
		t.Fatalf("Promote called %d times, want once", len(persister.promoted))
	}
	if got := lastOfRole(t, persister.promoted[0], "assistant").Content; got != "done" {
		t.Errorf("promoted answer = %q, want done", got)
	}
	if _, err := os.Stat(filepath.Join(f.configDir, "conversations", "globalScope.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("chat.SaveAsPreviousQuery wrote globalScope.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(persisterDir, "conversations", "globalScope.json")); err != nil {
		t.Errorf("Promote wrote no globalScope.json: %v", err)
	}
}
