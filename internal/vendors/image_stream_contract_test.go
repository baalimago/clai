package vendors_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	openRouterStreamFixture = "openrouter-chat-image-stream.sse"
	openRouterImageModel    = "google/gemini-3.1-flash-lite-image"
	openRouterTextModel     = "anthropic/claude-haiku-4.5"
	redactedReasoningMarker = "REDACTED-reasoning-details"
)

type emitterRequest struct {
	method, path string
	body         []byte
}

// emitterServer serves stream on every path but the OpenRouter catalog, which serves
// catalog, or fails when catalog is nil.
type emitterServer struct {
	url  string
	mu   sync.Mutex
	reqs []emitterRequest
}

func serveEmitter(t *testing.T, stream, catalog []byte) *emitterServer {
	t.Helper()
	s := &emitterServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, emitterRequest{method: r.Method, path: r.URL.Path, body: body})
		s.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/api/v1/models") {
			if catalog == nil {
				http.Error(w, "catalog down", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(catalog)
			return
		}
		_, _ = w.Write(stream)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *emitterServer) posts() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for _, r := range s.reqs {
		if r.method == http.MethodPost {
			out = append(out, r.body)
		}
	}
	return out
}

func (s *emitterServer) catalogFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reqs {
		if r.method == http.MethodGet && r.path == "/api/v1/models" {
			n++
		}
	}
	return n
}

func (s *emitterServer) onePost(t *testing.T) []byte {
	t.Helper()
	posts := s.posts()
	if len(posts) != 1 {
		t.Fatalf("POST requests = %d, want 1", len(posts))
	}
	return posts[0]
}

func emitterFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "image_emitter", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func emitterGolden(t *testing.T, name string) []byte {
	t.Helper()
	return emitterFixture(t, name+".text.golden")
}

func newOpenRouterEmitter(t *testing.T, srv *emitterServer, model string, lookup openrouter.ModalityLookup) *openrouter.OpenRouter {
	t.Helper()
	o := &openrouter.OpenRouter{Model: "or:" + model, Modalities: lookup}
	if err := o.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	o.StreamCompleter.URL = srv.url + "/api/v1/chat/completions"
	return o
}

func catalogLookup(t *testing.T, srv *emitterServer) openrouter.ModalityLookup {
	t.Helper()
	return openrouter.CatalogModalities(srv.url, t.TempDir(), http.DefaultClient)
}

func drawChat() pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "Draw a red circle."}}}
}

func drain(t *testing.T, e models.StreamCompleter, chat pub_models.Chat) []models.CompletionEvent {
	t.Helper()
	ch, err := e.StreamCompletions(context.Background(), chat)
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	var evs []models.CompletionEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

// onlyImage asserts exactly one ImageEvent, followed later by a final StopEvent, and
// no error.
func onlyImage(t *testing.T, evs []models.CompletionEvent) models.ImageEvent {
	t.Helper()
	var (
		images []models.ImageEvent
		at     int
	)
	for i, ev := range evs {
		switch e := ev.(type) {
		case models.ImageEvent:
			images = append(images, e)
			at = i
		case error:
			t.Fatalf("event %d is an error: %v", i, e)
		}
	}
	if len(images) != 1 {
		t.Fatalf("ImageEvents = %d, want 1", len(images))
	}
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	if _, ok := evs[len(evs)-1].(models.StopEvent); !ok || at > len(evs)-2 {
		t.Fatalf("want the image before a final StopEvent, got %T last and the image at %d of %d", evs[len(evs)-1], at, len(evs))
	}
	return images[0]
}

// terminalError asserts the stream ends on an error and emits no image.
func terminalError(t *testing.T, evs []models.CompletionEvent) error {
	t.Helper()
	for i, ev := range evs {
		if _, ok := ev.(models.ImageEvent); ok {
			t.Fatalf("event %d is an image; want none", i)
		}
	}
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	err, ok := evs[len(evs)-1].(error)
	if !ok {
		t.Fatalf("last event = %T, want a terminal error", evs[len(evs)-1])
	}
	return err
}

func dataURLBytes(t *testing.T, stream []byte) []byte {
	t.Helper()
	_, rest, ok := bytes.Cut(stream, []byte(";base64,"))
	if !ok {
		t.Fatal("stream has no base64 data URL")
	}
	b64, _, _ := bytes.Cut(rest, []byte(`"`))
	data, err := base64.StdEncoding.DecodeString(string(b64))
	if err != nil {
		t.Fatalf("decode fixture data URL: %v", err)
	}
	return data
}

// holdsText reports whether any string or byte slice reachable from v contains marker.
func holdsText(v reflect.Value, marker string, seen map[uintptr]bool) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.Contains(v.String(), marker)
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return false
		}
		seen[v.Pointer()] = true
		return holdsText(v.Elem(), marker, seen)
	case reflect.Interface:
		return !v.IsNil() && holdsText(v.Elem(), marker, seen)
	case reflect.Struct:
		for _, field := range v.Fields() {
			if holdsText(field, marker, seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return bytes.Contains(v.Bytes(), []byte(marker))
		}
		for i := range v.Len() {
			if holdsText(v.Index(i), marker, seen) {
				return true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if holdsText(iter.Key(), marker, seen) || holdsText(iter.Value(), marker, seen) {
				return true
			}
		}
	}
	return false
}

// Proves O1, O2, O6 and AC3 on OpenRouter.
func TestOpenRouterImageStream(t *testing.T) {
	setVendorKeys(t)
	stream := emitterFixture(t, openRouterStreamFixture)
	if !bytes.Contains(stream, []byte(redactedReasoningMarker)) {
		t.Fatal("fixture lost its reasoning_details blob")
	}
	srv := serveEmitter(t, stream, emitterFixture(t, "openrouter-models-modalities.json"))
	o := newOpenRouterEmitter(t, srv, openRouterImageModel, catalogLookup(t, srv))

	evs := drain(t, o, drawChat())
	img := onlyImage(t, evs)
	if img.Image.MIME != "image/jpeg" || img.Image.Origin != pub_models.OriginGenerated || img.Image.Asset != "" {
		t.Fatalf("image = {MIME %q, Origin %q, Asset %q}, want a pending generated image/jpeg", img.Image.MIME, img.Image.Origin, img.Image.Asset)
	}
	if !bytes.Equal(img.Image.Data, dataURLBytes(t, stream)) {
		t.Fatal("image bytes differ from the decoded data URL")
	}
	if holdsText(reflect.ValueOf(evs), redactedReasoningMarker, map[uintptr]bool{}) {
		t.Fatal("an event carries the reasoning_details blob")
	}
	if holdsText(reflect.ValueOf(o), redactedReasoningMarker, map[uintptr]bool{}) {
		t.Fatal("the completer retains the reasoning_details blob")
	}
}

// Proves O8 for every image-output model of the modality fixture.
func TestOpenRouterRequest_Modalities(t *testing.T) {
	setVendorKeys(t)
	catalog := emitterFixture(t, "openrouter-models-modalities.json")
	for _, model := range []string{openRouterImageModel, "google/gemini-2.5-flash-image", "openai/gpt-5-image-mini"} {
		t.Run(model, func(t *testing.T) {
			srv := serveEmitter(t, []byte(chatReply), catalog)
			o := newOpenRouterEmitter(t, srv, model, catalogLookup(t, srv))
			drain(t, o, textChat())
			if body := srv.onePost(t); !bytes.Contains(body, []byte(`"modalities":["image","text"]`)) {
				t.Fatalf("body lacks the image modalities: %s", body)
			}
		})
	}
}

// Proves O8, O9, I10 and AC5: a text-only model's request is the pre-phase body.
func TestOpenRouterRequest_TextModelUnchanged(t *testing.T) {
	setVendorKeys(t)
	srv := serveEmitter(t, []byte(chatReply), emitterFixture(t, "openrouter-models-modalities.json"))
	o := newOpenRouterEmitter(t, srv, openRouterTextModel, catalogLookup(t, srv))
	for i, ev := range drain(t, o, textChat()) {
		if _, ok := ev.(models.ImageProgressEvent); ok {
			t.Fatalf("event %d is an ImageProgressEvent on a text-only model (O9)", i)
		}
	}
	if got, want := srv.onePost(t), emitterGolden(t, "openrouter-text-model"); !bytes.Equal(got, want) {
		t.Fatalf("text model body changed:\n got: %s\nwant: %s", got, want)
	}
	if srv.catalogFetches() != 1 {
		t.Fatalf("catalog fetches = %d, want 1", srv.catalogFetches())
	}
}

// Proves O8 and AC5: a failed lookup leaves Setup and the request untouched.
func TestOpenRouterLookupFails_TextUnchanged(t *testing.T) {
	setVendorKeys(t)
	errDown := errors.New("catalog unreachable")
	rows := []struct {
		name   string
		lookup func(*emitterServer) openrouter.ModalityLookup
		want   error
	}{
		{"lookup error", func(*emitterServer) openrouter.ModalityLookup {
			return func(context.Context, string) ([]string, error) { return nil, errDown }
		}, errDown},
		{"not in catalog", func(*emitterServer) openrouter.ModalityLookup {
			return func(_ context.Context, model string) ([]string, error) {
				return nil, errors.Join(openrouter.ErrModelNotInCatalog, errors.New(model))
			}
		}, openrouter.ErrModelNotInCatalog},
		{"catalog server fails", func(s *emitterServer) openrouter.ModalityLookup {
			return openrouter.CatalogModalities(s.url, t.TempDir(), http.DefaultClient)
		}, nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			srv := serveEmitter(t, []byte(chatReply), nil)
			calls := 0
			inner := row.lookup(srv)
			o := newOpenRouterEmitter(t, srv, openRouterImageModel, func(ctx context.Context, model string) ([]string, error) {
				calls++
				return inner(ctx, model)
			})
			if calls != 0 {
				t.Fatalf("Setup ran the lookup %d times, want 0", calls)
			}
			drain(t, o, textChat())
			if got, want := srv.onePost(t), emitterGolden(t, "openrouter-image-model"); !bytes.Equal(got, want) {
				t.Fatalf("body changed after a failed lookup:\n got: %s\nwant: %s", got, want)
			}
			if calls != 1 {
				t.Fatalf("lookup calls = %d, want 1 per completer", calls)
			}
		})
	}
}

// Proves O9: an image-output model's stream signals progress first, then the image.
func TestImageStream_Progress(t *testing.T) {
	setVendorKeys(t)
	evs := drain(t, openRouterImageEmitter(t, serveEmitter(t, emitterFixture(t, openRouterStreamFixture), nil)), drawChat())
	if _, ok := evs[0].(models.ImageProgressEvent); !ok {
		t.Fatalf("first event = %T, want ImageProgressEvent", evs[0])
	}
	progress, image := 0, -1
	for i, ev := range evs {
		switch ev.(type) {
		case models.ImageProgressEvent:
			progress++
		case models.ImageEvent:
			image = i
		}
	}
	if image < 0 || progress != 1 {
		t.Fatalf("%d progress events and image at %d, want one progress event before the image", progress, image)
	}
}

// Proves O2: the data-URL type disagrees with the bytes.
func TestImageStream_MIMEMismatch(t *testing.T) {
	setVendorKeys(t)
	stream := bytes.ReplaceAll(emitterFixture(t, openRouterStreamFixture), []byte("data:image/jpeg;base64,"), []byte("data:image/png;base64,"))
	err := terminalError(t, drain(t, openRouterImageEmitter(t, serveEmitter(t, stream, nil)), drawChat()))
	var mm *asset.MIMEMismatchError
	if !errors.As(err, &mm) || mm.Declared != "image/png" || mm.Sniffed != "image/jpeg" {
		t.Fatalf("err = %v, want *asset.MIMEMismatchError{image/png, image/jpeg}", err)
	}
}

// Proves O3: base64 that does not decode is terminal.
func TestImageStream_BadBase64(t *testing.T) {
	setVendorKeys(t)
	stream := bytes.ReplaceAll(emitterFixture(t, openRouterStreamFixture), []byte(";base64,/9j/"), []byte(";base64,*/9j/"))
	err := terminalError(t, drain(t, openRouterImageEmitter(t, serveEmitter(t, stream, nil)), drawChat()))
	var upr *claierr.UnexpectedProviderResponseError
	if !errors.As(err, &upr) {
		t.Fatalf("err = %v, want *claierr.UnexpectedProviderResponseError", err)
	}
}

// openRouterImageEmitter is an image-output model whose lookup reports image output.
func openRouterImageEmitter(t *testing.T, srv *emitterServer) *openrouter.OpenRouter {
	return newOpenRouterEmitter(t, srv, openRouterImageModel, func(context.Context, string) ([]string, error) {
		return []string{"image", "text"}, nil
	})
}

func modalityCacheFile(dir, model string) string {
	sum := sha256.Sum256([]byte(model))
	return filepath.Join(dir, "openrouter-modalities", hex.EncodeToString(sum[:])+".json")
}

// Proves ModalityLookup.1: a nil Modalities field reads the catalog beside the
// completer's own chat endpoint and caches under the clai cache dir.
func TestOpenRouterModalities_DefaultLookup(t *testing.T) {
	setVendorKeys(t)
	cache := t.TempDir()
	t.Setenv("CLAI_CACHE_DIR", cache)
	srv := serveEmitter(t, []byte(chatReply), emitterFixture(t, "openrouter-models-modalities.json"))
	o := newOpenRouterEmitter(t, srv, openRouterImageModel, nil)

	drain(t, o, textChat())
	if srv.catalogFetches() != 1 {
		t.Fatalf("catalog fetches from the test server = %d, want 1", srv.catalogFetches())
	}
	if body := srv.onePost(t); !bytes.Contains(body, []byte(`"modalities":["image","text"]`)) {
		t.Fatalf("body lacks the image modalities: %s", body)
	}
	cached, err := os.ReadFile(modalityCacheFile(cache, openRouterImageModel))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	var got []string
	if err := json.Unmarshal(cached, &got); err != nil || !reflect.DeepEqual(got, []string{"image", "text"}) {
		t.Fatalf("cache = %s (%v), want [\"image\",\"text\"]", cached, err)
	}
}

// Proves ModalityLookup.2: without a usable cache dir the lookup still answers,
// fetches every time and writes nothing.
func TestOpenRouterModalities_NoCacheDir(t *testing.T) {
	setVendorKeys(t)
	catalog := emitterFixture(t, "openrouter-models-modalities.json")
	for _, row := range []struct {
		name string
		env  func(t *testing.T) string // returns a path that must stay unchanged
	}{
		{"unset", func(t *testing.T) string {
			for _, k := range []string{"CLAI_CACHE_DIR", "XDG_CACHE_HOME", "HOME"} {
				t.Setenv(k, "")
			}
			return ""
		}},
		{"unusable", func(t *testing.T) string {
			blocker := filepath.Join(t.TempDir(), "not-a-dir")
			if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAI_CACHE_DIR", blocker)
			return blocker
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			blocker := row.env(t)
			srv := serveEmitter(t, []byte(chatReply), catalog)
			for range 2 {
				drain(t, newOpenRouterEmitter(t, srv, openRouterImageModel, nil), textChat())
			}
			if srv.catalogFetches() != 2 {
				t.Fatalf("catalog fetches = %d, want 2", srv.catalogFetches())
			}
			for i, body := range srv.posts() {
				if !bytes.Contains(body, []byte(`"modalities":["image","text"]`)) {
					t.Fatalf("request %d lacks the image modalities: %s", i, body)
				}
			}
			if _, err := os.Stat("openrouter-modalities"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a cache dir appeared in the working directory: %v", err)
			}
			if blocker == "" {
				return
			}
			if b, err := os.ReadFile(blocker); err != nil || string(b) != "file" {
				t.Fatalf("blocker = %q, %v; want it unchanged", b, err)
			}
			entries, err := os.ReadDir(filepath.Dir(blocker))
			if err != nil || len(entries) != 1 {
				t.Fatalf("blocker dir holds %d entries (%v), want only the blocker", len(entries), err)
			}
		})
	}
}
