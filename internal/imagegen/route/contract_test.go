package route_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/pkg/claierr"
)

const testKey = "test-key"

type reply struct {
	status int
	body   string
}

// backendCase is one ImageGenerator implementation, reached through route.NewGenerator.
type backendCase struct {
	name, model, env string
	success          string // testdata fixture of a successful response
	declared         [2]string
	empty            string
	refusal          reply
	wire             func(asset.Image) []byte // how an input travels in the request body
	wantMIME         string
	productionHost   string
}

func base64Of(img asset.Image) []byte {
	return []byte(base64.StdEncoding.EncodeToString(img.Data))
}

func backendCases() []backendCase {
	return []backendCase{
		{
			name: "openai", model: "gpt-image-1-mini", env: "OPENAI_API_KEY",
			success:  "openai-images-generations.json",
			declared: [2]string{`"output_format": "png"`, `"output_format": "jpeg"`},
			empty:    `{"created":1,"data":[]}`,
			refusal: reply{http.StatusBadRequest, `{"error":{"message":"Your request was rejected by the safety system.",` +
				`"type":"image_generation_user_error","param":null,"code":"moderation_blocked"}}`},
			wire:           func(img asset.Image) []byte { return img.Data },
			wantMIME:       "image/png",
			productionHost: "api.openai.com",
		},
		{
			name: "openrouter", model: "or:google/gemini-3.1-flash-lite-image", env: "OPENROUTER_API_KEY",
			success:  "openrouter-images.json",
			declared: [2]string{`"media_type": "image/jpeg"`, `"media_type": "image/png"`},
			empty:    `{"created":0,"data":[]}`,
			refusal: reply{http.StatusForbidden, `{"error":{"code":403,"message":"Your input was flagged for \"violence\"",` +
				`"metadata":{"reasons":["violence"],"flagged_input":"draw...","provider_name":"Google","model_slug":"google/gemini-3.1-flash-lite-image"}}}`},
			wire:           base64Of,
			wantMIME:       "image/jpeg",
			productionHost: "openrouter.ai",
		},
		{
			name: "gemini", model: "gemini-3.1-flash-image", env: "GEMINI_API_KEY",
			success:        "gemini-generate-content.json",
			declared:       [2]string{`"mimeType": "image/png"`, `"mimeType": "image/jpeg"`},
			empty:          `{"candidates":[{"content":{"role":"model","parts":[{"text":"No image this time."}]},"finishReason":"STOP"}]}`,
			refusal:        reply{http.StatusOK, `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`},
			wire:           base64Of,
			wantMIME:       "image/png",
			productionHost: "generativelanguage.googleapis.com",
		},
	}
}

func backendNamed(t *testing.T, name string) backendCase {
	t.Helper()
	for _, bc := range backendCases() {
		if bc.name == name {
			return bc
		}
	}
	t.Fatalf("no backend case %q", name)
	return backendCase{}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func probeImage(t *testing.T) asset.Image {
	t.Helper()
	return asset.Image{Data: fixture(t, "probe-glyph-K7Q.png"), MIME: "image/png"}
}

type exchange struct {
	method, path string
	header       http.Header
	body         []byte
}

// fakeVendor records every request and answers with its current reply, or with respond.
type fakeVendor struct {
	url     string
	mu      sync.Mutex
	got     []exchange
	current reply
}

func serve(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *fakeVendor {
	t.Helper()
	v := &fakeVendor{}
	if respond == nil {
		respond = v.answer
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		v.mu.Lock()
		v.got = append(v.got, exchange{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
		v.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	v.url = srv.URL
	return v
}

func serveReply(t *testing.T, status int, body []byte) *fakeVendor {
	t.Helper()
	v := serve(t, nil)
	v.set(status, string(body))
	return v
}

func (v *fakeVendor) set(status int, body string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.current = reply{status: status, body: body}
}

func (v *fakeVendor) answer(w http.ResponseWriter, _ *http.Request) {
	v.mu.Lock()
	r := v.current
	v.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.status)
	_, _ = io.WriteString(w, r.body)
}

func (v *fakeVendor) requests() []exchange {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]exchange(nil), v.got...)
}

func (v *fakeVendor) only(t *testing.T) exchange {
	t.Helper()
	got := v.requests()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	return got[0]
}

func newGenerator(t *testing.T, bc backendCase, baseURL, configDir string) imagegen.ImageGenerator {
	t.Helper()
	t.Setenv(bc.env, testKey)
	g, err := route.NewGenerator(imagegen.GeneratorConfig{Model: bc.model, ConfigDir: configDir, BaseURL: baseURL})
	if err != nil {
		t.Fatalf("NewGenerator(%q): %v", bc.model, err)
	}
	return g
}

func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	out := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- b
	}()
	orig := os.Stdout
	os.Stdout = w
	run()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Errorf("close pipe: %v", err)
	}
	b := <-out
	_ = r.Close()
	return string(b)
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("Generate left %s in %s", e.Name(), dir)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// scenario is what a subject's generator answers in a shared row.
type scenario int

const (
	answerImage    scenario = iota // one image of the subject's wantMIME
	answerMismatch                 // one image whose declared type is not its bytes'
	answerEmpty                    // no image
)

// contractSubject is one ImageGenerator under the shared rows: a backend through
// route.NewGenerator over a fake vendor, or the imagegentest.Fake every test uses.
type contractSubject struct {
	name     string
	wantMIME string
	// answering returns a generator whose every call answers the scenario.
	answering func(t *testing.T, s scenario, configDir string) imagegen.ImageGenerator
	// blocking returns a generator whose call waits for ctx to end; arrived closes once
	// a call is under way, released once the wait saw ctx end.
	blocking func(t *testing.T) (g imagegen.ImageGenerator, arrived, released <-chan struct{})
	// backend is set for the HTTP backends, which also run the wire rows.
	backend *backendCase
}

func backendSubject(bc backendCase) contractSubject {
	return contractSubject{
		name:     bc.name,
		wantMIME: bc.wantMIME,
		backend:  &bc,
		answering: func(t *testing.T, s scenario, configDir string) imagegen.ImageGenerator {
			t.Helper()
			success := fixture(t, bc.success)
			body := success
			switch s {
			case answerImage:
			case answerMismatch:
				body = bytes.Replace(success, []byte(bc.declared[0]), []byte(bc.declared[1]), 1)
				if bytes.Equal(body, success) {
					t.Fatalf("fixture %s does not declare %s", bc.success, bc.declared[0])
				}
			case answerEmpty:
				body = []byte(bc.empty)
			}
			v := serveReply(t, http.StatusOK, body)
			return newGenerator(t, bc, v.url, configDir)
		},
		blocking: func(t *testing.T) (imagegen.ImageGenerator, <-chan struct{}, <-chan struct{}) {
			t.Helper()
			arrived, released := make(chan struct{}), make(chan struct{})
			v := serve(t, func(_ http.ResponseWriter, r *http.Request) {
				close(arrived)
				<-r.Context().Done()
				close(released)
			})
			return newGenerator(t, bc, v.url, t.TempDir()), arrived, released
		},
	}
}

func fakeSubject(t *testing.T) contractSubject {
	probe := probeImage(t)
	closed := make(chan struct{})
	close(closed)
	return contractSubject{
		name:     "fake",
		wantMIME: probe.MIME,
		answering: func(_ *testing.T, s scenario, _ string) imagegen.ImageGenerator {
			switch s {
			case answerMismatch:
				return &imagegentest.Fake{Images: []imagegen.GeneratedImage{{Image: asset.Image{Data: probe.Data, MIME: "image/jpeg"}}}}
			case answerEmpty:
				return &imagegentest.Fake{}
			}
			return &imagegentest.Fake{Images: []imagegen.GeneratedImage{{Image: probe}}}
		},
		// The fake waits in a select, so a cancel before or during the wait ends it alike.
		blocking: func(*testing.T) (imagegen.ImageGenerator, <-chan struct{}, <-chan struct{}) {
			return &imagegentest.Fake{Images: []imagegen.GeneratedImage{{Image: probe}}, Latency: time.Hour}, closed, closed
		},
	}
}

func contractSubjects(t *testing.T) []contractSubject {
	subjects := make([]contractSubject, 0, len(backendCases())+1)
	for _, bc := range backendCases() {
		subjects = append(subjects, backendSubject(bc))
	}
	return append(subjects, fakeSubject(t))
}

func TestImageGeneratorContract(t *testing.T) {
	ctx := context.Background()
	for _, s := range contractSubjects(t) {
		t.Run(s.name, func(t *testing.T) {
			t.Run("Generate", func(t *testing.T) {
				images, err := s.answering(t, answerImage, t.TempDir()).Generate(ctx, imagegen.ImageRequest{Prompt: "a green triangle"})
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				if len(images) != 1 {
					t.Fatalf("images = %d, want 1", len(images))
				}
				sniffed, err := asset.Sniff(images[0].Image.Data)
				if err != nil {
					t.Fatalf("sniff: %v", err)
				}
				if images[0].Image.MIME != sniffed || sniffed != s.wantMIME {
					t.Errorf("MIME = %q, sniffed %q, want %q", images[0].Image.MIME, sniffed, s.wantMIME)
				}
			})

			t.Run("MIMEMismatch", func(t *testing.T) {
				images, err := s.answering(t, answerMismatch, t.TempDir()).Generate(ctx, imagegen.ImageRequest{Prompt: "p"})
				var mismatch *asset.MIMEMismatchError
				if !errors.As(err, &mismatch) {
					t.Fatalf("err = %v, want *asset.MIMEMismatchError", err)
				}
				if mismatch.Sniffed != s.wantMIME {
					t.Errorf("Sniffed = %q, want %q", mismatch.Sniffed, s.wantMIME)
				}
				if images != nil {
					t.Errorf("returned %d images alongside the mismatch", len(images))
				}
			})

			t.Run("Empty", func(t *testing.T) {
				images, err := s.answering(t, answerEmpty, t.TempDir()).Generate(ctx, imagegen.ImageRequest{Prompt: "p"})
				if !errors.Is(err, imagegen.ErrNoImage) {
					t.Fatalf("err = %v, want ErrNoImage", err)
				}
				if images != nil {
					t.Errorf("returned %d images", len(images))
				}
			})

			t.Run("Cancelled", func(t *testing.T) {
				g, arrived, released := s.blocking(t)
				cctx, cancel := context.WithCancel(ctx)
				defer cancel()
				errc := make(chan error, 1)
				go func() {
					_, err := g.Generate(cctx, imagegen.ImageRequest{Prompt: "p"})
					errc <- err
				}()
				waitFor(t, arrived, "the request to arrive")
				cancel()
				select {
				case err := <-errc:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("err = %v, want context.Canceled", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Generate hung after cancel")
				}
				waitFor(t, released, "the server to see the request aborted")
			})

			t.Run("NoSideEffects", func(t *testing.T) {
				home, configDir := t.TempDir(), t.TempDir()
				t.Setenv("HOME", home)
				var err error
				stdout := captureStdout(t, func() {
					_, err = s.answering(t, answerImage, configDir).Generate(ctx, imagegen.ImageRequest{Prompt: "p", Inputs: []asset.Image{probeImage(t)}})
				})
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want empty", stdout)
				}
				assertEmptyDir(t, home)
				assertEmptyDir(t, configDir)
			})

			if s.backend != nil {
				wireRows(t, *s.backend)
			}
		})
	}
}

// wireRows are the clauses only an HTTP backend can break: Generate.3 and Generate.6.
func wireRows(t *testing.T, bc backendCase) {
	ctx := context.Background()
	input := probeImage(t)
	success := fixture(t, bc.success)

	t.Run("HTTPErrors", func(t *testing.T) {
		v := serve(t, nil)
		g := newGenerator(t, bc, v.url, t.TempDir())
		for _, c := range []struct {
			name   string
			status int
			body   string
			want   error
		}{
			{"auth", http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided"}}`, claierr.ErrAuthFailed},
			{"credits", http.StatusPaymentRequired, `{"error":{"message":"Insufficient credits"}}`, claierr.ErrLikelyInsufficientCredits},
			{"rate limit", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached"}}`, claierr.ErrRateLimited},
			{"model not found", http.StatusNotFound, `{"error":{"message":"The model does not exist"}}`, claierr.ErrModelNotFound},
			{"content filtered", bc.refusal.status, bc.refusal.body, claierr.ErrContentFiltered},
		} {
			v.set(c.status, c.body)
			images, err := g.Generate(ctx, imagegen.ImageRequest{Prompt: "p"})
			if !errors.Is(err, c.want) {
				t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
				continue
			}
			var api claierr.APIErrorer
			if !errors.As(err, &api) || api.API().StatusCode != c.status {
				t.Errorf("%s: err %v carries no APIError with status %d", c.name, err, c.status)
			}
			if images != nil {
				t.Errorf("%s: returned %d images", c.name, len(images))
			}
		}
	})

	t.Run("Base64OnlyOnWire", func(t *testing.T) {
		v := serveReply(t, http.StatusOK, success)
		images, err := newGenerator(t, bc, v.url, t.TempDir()).Generate(ctx, imagegen.ImageRequest{Prompt: "edit", Inputs: []asset.Image{input}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !bytes.Contains(v.only(t).body, bc.wire(input)) {
			t.Error("request body does not carry the input in its wire encoding")
		}
		if mime, err := asset.Sniff(images[0].Image.Data); err != nil || mime != bc.wantMIME {
			t.Errorf("returned image holds no %s bytes: %v", bc.wantMIME, err)
		}
	})

	t.Run("Transport", func(t *testing.T) {
		t.Setenv(bc.env, testKey)
		g, err := route.NewGenerator(imagegen.GeneratorConfig{
			Model: bc.model, ConfigDir: t.TempDir(), BaseURL: "http://vendor.invalid",
			Client: &http.Client{Transport: failingTransport{}},
		})
		if err != nil {
			t.Fatalf("NewGenerator: %v", err)
		}
		if _, err := g.Generate(ctx, imagegen.ImageRequest{Prompt: "p"}); !errors.Is(err, claierr.ErrTransport) {
			t.Errorf("err = %v, want ErrTransport", err)
		}
	})

	t.Run("Undecodable", func(t *testing.T) {
		v := serveReply(t, http.StatusOK, []byte("<html>not json</html>"))
		_, err := newGenerator(t, bc, v.url, t.TempDir()).Generate(ctx, imagegen.ImageRequest{Prompt: "p"})
		if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
			t.Errorf("err = %v, want ErrUnexpectedProviderResponse", err)
		}
	})
}

// recordingTransport answers every request with body and records its URL, so no
// request leaves the process.
type recordingTransport struct {
	body []byte
	mu   sync.Mutex
	urls []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.urls = append(rt.urls, r.URL.Scheme+"://"+r.URL.Host)
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(rt.body)),
		Request:    r,
	}, nil
}

func TestImageGenerators_DefaultBaseURL(t *testing.T) {
	for _, bc := range backendCases() {
		t.Run(bc.name, func(t *testing.T) {
			t.Setenv(bc.env, testKey)
			rt := &recordingTransport{body: fixture(t, bc.success)}
			g, err := route.NewGenerator(imagegen.GeneratorConfig{Model: bc.model, ConfigDir: t.TempDir(), Client: &http.Client{Transport: rt}})
			if err != nil {
				t.Fatalf("NewGenerator: %v", err)
			}
			if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "p"}); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if want := "https://" + bc.productionHost; len(rt.urls) != 1 || rt.urls[0] != want {
				t.Errorf("requests went to %v, want [%s]", rt.urls, want)
			}
		})
	}
}

type multipartRequest struct {
	fields map[string]string
	images []asset.Image
}

func parseMultipart(t *testing.T, ex exchange) multipartRequest {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(ex.header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, want multipart/form-data: %v", ex.header.Get("Content-Type"), err)
	}
	got := multipartRequest{fields: map[string]string{}}
	reader := multipart.NewReader(bytes.NewReader(ex.body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "image[]" {
			got.images = append(got.images, asset.Image{Data: data, MIME: part.Header.Get("Content-Type")})
			continue
		}
		got.fields[part.FormName()] = string(data)
	}
}

func jsonBody(t *testing.T, ex exchange) map[string]any {
	t.Helper()
	if ct := ex.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(ex.body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return body
}

func jpegImage(t *testing.T) asset.Image {
	t.Helper()
	var resp struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(fixture(t, "openrouter-images.json"), &resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
	if err != nil {
		t.Fatalf("decode fixture image: %v", err)
	}
	return asset.Image{Data: data, MIME: "image/jpeg"}
}

func TestOpenAIImages_Routing(t *testing.T) {
	generations, edits := fixture(t, "openai-images-generations.json"), fixture(t, "openai-images-edits.json")
	v := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/images/edits" {
			_, _ = w.Write(edits)
			return
		}
		_, _ = w.Write(generations)
	})
	g := newGenerator(t, backendNamed(t, "openai"), v.url, t.TempDir())
	inputs := []asset.Image{probeImage(t), jpegImage(t)}
	if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "a red square"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "make it blue", Inputs: inputs}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got := v.requests()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2", len(got))
	}
	for _, ex := range got {
		if ex.method != http.MethodPost || ex.header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("%s %s with Authorization %q", ex.method, ex.path, ex.header.Get("Authorization"))
		}
	}

	if got[0].path != "/v1/images/generations" {
		t.Errorf("first path = %q, want /v1/images/generations", got[0].path)
	}
	body := jsonBody(t, got[0])
	if body["model"] != "gpt-image-1-mini" || body["prompt"] != "a red square" {
		t.Errorf("generations body = %v", body)
	}

	if got[1].path != "/v1/images/edits" {
		t.Errorf("second path = %q, want /v1/images/edits", got[1].path)
	}
	edit := parseMultipart(t, got[1])
	if edit.fields["model"] != "gpt-image-1-mini" || edit.fields["prompt"] != "make it blue" {
		t.Errorf("edits fields = %v", edit.fields)
	}
	if len(edit.images) != len(inputs) {
		t.Fatalf("image[] parts = %d, want %d", len(edit.images), len(inputs))
	}
	for i, img := range edit.images {
		if img.MIME != inputs[i].MIME || !bytes.Equal(img.Data, inputs[i].Data) {
			t.Errorf("image[%d] = %s, %d bytes; want %s, %d bytes", i, img.MIME, len(img.Data), inputs[i].MIME, len(inputs[i].Data))
		}
	}
}

func TestOpenAIImages_PerModelConfig(t *testing.T) {
	bc := backendNamed(t, "openai")
	generations := fixture(t, "openai-images-generations.json")
	params := func(t *testing.T, model, configDir string, req imagegen.ImageRequest) map[string]any {
		t.Helper()
		v := serveReply(t, http.StatusOK, generations)
		bc.model = model
		if _, err := newGenerator(t, bc, v.url, configDir).Generate(context.Background(), req); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return jsonBody(t, v.only(t))
	}
	want := func(t *testing.T, got map[string]any, field string, value any) {
		t.Helper()
		if got[field] != value {
			t.Errorf("%s = %v, want %v (body %v)", field, got[field], value, got)
		}
	}

	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()
		conf := `{"model":"gpt-image-1-mini","n":1,"size":"1024x1536","quality":"low","output":{"type":"local","dir":"/tmp","prefix":"clai"}}`
		if err := os.WriteFile(filepath.Join(dir, "openai_dalle_gpt-image-1-mini.json"), []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		got := params(t, "gpt-image-1-mini", dir, imagegen.ImageRequest{Prompt: "p", Size: "1536x1024"})
		want(t, got, "quality", "low")
		want(t, got, "size", "1536x1024")
		want(t, got, "n", float64(1))
		want(t, params(t, "gpt-image-1-mini", dir, imagegen.ImageRequest{Prompt: "p"}), "size", "1024x1536")
	})

	t.Run("defaults", func(t *testing.T) {
		got := params(t, "gpt-image-1-mini", t.TempDir(), imagegen.ImageRequest{Prompt: "p"})
		want(t, got, "quality", "auto")
		want(t, got, "size", "1024x1024")
		want(t, got, "n", float64(1))
		if _, ok := got["response_format"]; ok {
			t.Errorf("gpt-image body carries response_format: %v", got)
		}
		dalle := params(t, "dall-e-3", t.TempDir(), imagegen.ImageRequest{Prompt: "p"})
		want(t, dalle, "quality", "hd")
		want(t, dalle, "response_format", "b64_json")
	})

	t.Run("malformed", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "openai_dalle_gpt-image-1-mini.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(bc.env, testKey)
		if _, err := route.NewGenerator(imagegen.GeneratorConfig{Model: "gpt-image-1-mini", ConfigDir: dir}); err == nil {
			t.Error("NewGenerator accepted a malformed per-model config")
		}
		unreadable := t.TempDir()
		if err := os.Mkdir(filepath.Join(unreadable, "openai_dalle_gpt-image-1-mini.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := route.NewGenerator(imagegen.GeneratorConfig{Model: "gpt-image-1-mini", ConfigDir: unreadable}); err == nil {
			t.Error("NewGenerator accepted an unreadable per-model config")
		}
	})
}

func TestOpenAIImages_DallEResponse(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(probeImage(t).Data)
	v := serveReply(t, http.StatusOK, []byte(`{"created":1,"data":[{"b64_json":"`+png+`","revised_prompt":"a flat red square"}]}`))
	bc := backendNamed(t, "openai")
	bc.model = "dall-e-3"
	images, err := newGenerator(t, bc, v.url, t.TempDir()).Generate(context.Background(), imagegen.ImageRequest{Prompt: "a red square"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	if images[0].Image.MIME != "image/png" || images[0].RevisedPrompt != "a flat red square" {
		t.Errorf("image %s with revised prompt %q, want a PNG with the revised prompt", images[0].Image.MIME, images[0].RevisedPrompt)
	}
}

func TestOpenRouterImages_References(t *testing.T) {
	bc := backendNamed(t, "openrouter")
	input := probeImage(t)
	v := serveReply(t, http.StatusOK, fixture(t, bc.success))
	g := newGenerator(t, bc, v.url, t.TempDir())
	if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "p", Inputs: []asset.Image{input}}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Generate without inputs: %v", err)
	}
	got := v.requests()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2", len(got))
	}
	if got[0].path != "/api/v1/images" || got[0].header.Get("Authorization") != "Bearer "+testKey {
		t.Errorf("request %s with Authorization %q", got[0].path, got[0].header.Get("Authorization"))
	}
	var body struct {
		Model           string   `json:"model"`
		Prompt          string   `json:"prompt"`
		InputReferences []string `json:"input_references"`
	}
	if err := json.Unmarshal(got[0].body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Model != "google/gemini-3.1-flash-lite-image" || body.Prompt != "p" {
		t.Errorf("model %q, prompt %q", body.Model, body.Prompt)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(input.Data)
	if len(body.InputReferences) != 1 || body.InputReferences[0] != want {
		t.Errorf("input_references = %d entries, want one data URL of the input", len(body.InputReferences))
	}
	if _, ok := jsonBody(t, got[1])["input_references"]; ok {
		t.Error("a request without inputs carries input_references")
	}
}

func TestGeminiImages_Inputs(t *testing.T) {
	bc := backendNamed(t, "gemini")
	input := probeImage(t)
	v := serveReply(t, http.StatusOK, fixture(t, bc.success))
	g := newGenerator(t, bc, v.url, t.TempDir())
	if _, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "p", Inputs: []asset.Image{input}}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := v.only(t)
	if got.path != "/v1beta/models/gemini-3.1-flash-image:generateContent" || got.header.Get("x-goog-api-key") != testKey {
		t.Errorf("request %s with key %q", got.path, got.header.Get("x-goog-api-key"))
	}
	var body struct {
		Contents []struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 {
		t.Fatalf("contents = %+v, want one content with a text and an inlineData part", body.Contents)
	}
	parts := body.Contents[0].Parts
	if parts[0].Text != "p" {
		t.Errorf("text part = %q, want the prompt", parts[0].Text)
	}
	inline := parts[1].InlineData
	if inline == nil || inline.MimeType != "image/png" || inline.Data != base64.StdEncoding.EncodeToString(input.Data) {
		t.Error("inlineData part does not carry the input as base64")
	}
}

func TestGeminiImages_Refusals(t *testing.T) {
	bc := backendNamed(t, "gemini")
	v := serve(t, nil)
	g := newGenerator(t, bc, v.url, t.TempDir())
	for _, body := range []string{
		`{"promptFeedback":{"blockReason":"SAFETY"}}`,
		`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"IMAGE_SAFETY"}]}`,
	} {
		v.set(http.StatusOK, body)
		_, err := g.Generate(context.Background(), imagegen.ImageRequest{Prompt: "p"})
		if !errors.Is(err, claierr.ErrContentFiltered) || !errors.Is(err, imagegen.ErrNoImage) {
			t.Errorf("%s: err = %v, want ErrContentFiltered and ErrNoImage", strings.TrimSpace(body), err)
		}
	}
}
