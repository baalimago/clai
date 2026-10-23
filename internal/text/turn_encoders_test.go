package text

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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/asset/assettest"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/berget"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/huggingface"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// encoderRow is one vendor route of phase 5's capability table, aimed at a test server.
// An empty reply marks a route that sends no HTTP request.
type encoderRow struct {
	name  string
	reply string
	open  func(t *testing.T, url string) models.StreamCompleter
}

const (
	chatCompletionsDone = "data: [DONE]\n\n"
	responsesDone       = "data: {\"type\":\"response.completed\"}\n\n"
	anthropicDone       = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
)

func setTurnVendorKeys(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "BERGET_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
		huggingface.EnvAPITokenKey, "INCEPTION_API_KEY", "MISTRAL_API_KEY", "NOVITA_API_KEY",
		"OLLAMA_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY",
	} {
		t.Setenv(k, "test-key")
	}
}

// setUp sets up c, then aims it at url.
func setUp[C models.StreamCompleter](c C, aim func(C, string)) func(*testing.T, string) models.StreamCompleter {
	return func(t *testing.T, url string) models.StreamCompleter {
		t.Helper()
		if err := c.Setup(); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		aim(c, url)
		return c
	}
}

// aimGeneric sets up c, then aims its embedded chat-completions client at url.
func aimGeneric[C models.StreamCompleter](c C, inner func(C) *generic.StreamCompleter) func(*testing.T, string) models.StreamCompleter {
	return setUp(c, func(c C, url string) { inner(c).URL = url })
}

func inlineEncoderRows() []encoderRow {
	textOnly := func(context.Context, string) ([]string, error) { return []string{"text"}, nil }
	return []encoderRow{
		{"openai", responsesDone, setUp(&openai.ChatGPT{Model: "gpt-4.1-mini"}, func(c *openai.ChatGPT, url string) { c.URL = url + "/v1/responses" })},
		{"openai-chat-completions", chatCompletionsDone, setUp(&openai.ChatGPT{Model: "gpt-4.1-mini", URL: openai.ChatURL}, func(c *openai.ChatGPT, url string) { c.URL = url + "/v1/chat/completions" })},
		{"anthropic", anthropicDone, setUp(&anthropic.Claude{Model: "claude-sonnet-5-5", MaxTokens: 1024, AnthropicVersion: "2023-06-01"}, func(c *anthropic.Claude, url string) { c.URL = url + "/v1/messages" })},
		{"berget", chatCompletionsDone, aimGeneric(&berget.Berget{Model: "google/gemma-4-31B-it"}, func(v *berget.Berget) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"deepseek", chatCompletionsDone, aimGeneric(&deepseek.Deepseek{Model: "deepseek-chat"}, func(v *deepseek.Deepseek) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"gemini", chatCompletionsDone, aimGeneric(&gemini.Gemini{Model: "gemini-3.1-flash-lite"}, func(v *gemini.Gemini) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"huggingface", chatCompletionsDone, aimGeneric(&huggingface.HuggingFaceChat{Model: huggingface.DefaultModelName}, func(v *huggingface.HuggingFaceChat) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"mistral", chatCompletionsDone, aimGeneric(&mistral.Mistral{Model: "mistral-medium-latest"}, func(v *mistral.Mistral) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"novita", chatCompletionsDone, aimGeneric(&novita.Novita{Model: "moonshotai/kimi-k3"}, func(v *novita.Novita) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"ollama", chatCompletionsDone, aimGeneric(&ollama.Ollama{Model: "llama3"}, func(v *ollama.Ollama) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"openrouter", chatCompletionsDone, aimGeneric(&openrouter.OpenRouter{Model: "or:google/gemini-2.5-flash", Modalities: textOnly}, func(v *openrouter.OpenRouter) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"xai", chatCompletionsDone, aimGeneric(&xai.XAI{Model: "grok-4"}, func(v *xai.XAI) *generic.StreamCompleter { return &v.StreamCompleter })},
		{"mock", "", setUp(&vendors.Mock{}, func(*vendors.Mock, string) {})},
	}
}

// recordingServer answers every request with reply and keeps the bodies.
func recordingServer(t *testing.T, reply string) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// memoryTurn wires the real normalizer over an in-memory store, persisting under confDir.
func memoryTurn(store *assettest.Memory, out io.Writer, confDir string) *ImageTurn {
	return &ImageTurn{
		Store:      store,
		Normalizer: imagenorm.New(store),
		Presenter:  present.New(present.Options{Out: out}),
		Persister:  newRecordingPersister(confDir, store),
		Stderr:     io.Discard,
	}
}

// assertPromotedRefs checks that the turn promoted a chat whose image is a ref, never its bytes.
func assertPromotedRefs(t *testing.T, confDir string, image []byte) {
	t.Helper()
	saved, err := os.ReadFile(filepath.Join(confDir, "conversations", "globalScope.json"))
	if err != nil {
		t.Fatalf("the turn promoted nothing: %v", err)
	}
	sum := sha256.Sum256(image)
	if !bytes.Contains(saved, []byte(`"`+hex.EncodeToString(sum[:])+`.png"`)) || bytes.Contains(saved, []byte(base64.StdEncoding.EncodeToString(image))) {
		t.Fatalf("promoted chat = %s, want the image as its ref and no base64", saved)
	}
}

func TestTurn_NormalizedAcceptedByEncoders(t *testing.T) {
	setTurnVendorKeys(t)
	userPNG, genPNG, toolPNG := pngOf(t, 100), pngOf(t, 101), pngOf(t, 102)
	store := assettest.NewMemory()
	refs := map[string]string{}
	for name, data := range map[string][]byte{"user": userPNG, "generated": genPNG} {
		ref, err := store.Put(data, "image/png")
		if err != nil {
			t.Fatal(err)
		}
		refs[name] = ref
	}
	conversation := []pub_models.Message{
		{Role: "system", Content: "Describe images."},
		{Role: "user", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "what is this?"},
			pub_models.ImagePart{Asset: refs["user"], MIME: "image/png", Origin: pub_models.OriginUser},
		}},
		{Role: "assistant", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "a sketch of it:"},
			pub_models.ImagePart{Asset: refs["generated"], MIME: "image/png", Origin: pub_models.OriginGenerated},
		}},
		{Role: "user", Content: "look closer"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{
			ID: "call_1", Name: "view_image", Type: "function", Inputs: &pub_models.Input{"path": "a.png"},
			Function: pub_models.Specification{Name: "view_image", Arguments: `{"path":"a.png"}`},
		}}},
		{Role: "tool", ToolCallID: "call_1", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "loaded"},
			pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginTool, Data: toolPNG},
		}},
	}
	for _, row := range inlineEncoderRows() {
		t.Run(row.name, func(t *testing.T) {
			srv, bodies := recordingServer(t, row.reply)
			out := &strings.Builder{}
			persisted := t.TempDir()
			q := &Querier[models.StreamCompleter]{Raw: true, out: out, configDir: t.TempDir(), Model: row.open(t, srv.URL), imageTurn: memoryTurn(store, out, persisted)}
			session := &QuerySession{Chat: pub_models.Chat{Messages: append([]pub_models.Message(nil), conversation...)}, ShouldSaveReply: true, Raw: true}
			runner := sessionRunner[models.StreamCompleter]{
				querier:      q,
				finalizer:    sessionFinalizer[models.StreamCompleter]{querier: q},
				toolExecutor: toolExecutor[models.StreamCompleter]{querier: q},
			}
			err := runner.Run(context.Background(), session)
			var unsupported *claierr.ImageUnsupportedError
			if errors.Is(err, models.ErrImageNotLoaded) || errors.As(err, &unsupported) {
				t.Fatalf("the encoder refused the normalized chat: %v", err)
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			assertPromotedRefs(t, persisted, toolPNG)
			sent := bodies()
			if row.reply == "" {
				if len(sent) != 0 {
					t.Fatalf("want no HTTP request, got %d", len(sent))
				}
				return
			}
			if len(sent) != 1 {
				t.Fatalf("want one request, got %d", len(sent))
			}
			for name, data := range map[string][]byte{"user": userPNG, "generated": genPNG, "tool": toolPNG} {
				if !strings.Contains(sent[0], base64.StdEncoding.EncodeToString(data)) {
					t.Errorf("the request lacks the %s image", name)
				}
			}
		})
	}
}

func TestTurn_NormalizeRefusal(t *testing.T) {
	setTurnVendorKeys(t)
	srv, bodies := recordingServer(t, chatCompletionsDone)
	route := aimGeneric(&inception.Inception{Model: "mercury"}, func(v *inception.Inception) *generic.StreamCompleter { return &v.StreamCompleter })(t, srv.URL)
	store := assettest.NewMemory()
	out := &strings.Builder{}
	q := &Querier[models.StreamCompleter]{Raw: true, out: out, configDir: t.TempDir(), Model: route, imageTurn: memoryTurn(store, out, t.TempDir())}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Parts: []pub_models.Part{
		pub_models.TextPart{Text: "what is it?"},
		pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: pngOf(t, 110)},
	}}}}}
	runner := sessionRunner[models.StreamCompleter]{
		querier:      q,
		finalizer:    sessionFinalizer[models.StreamCompleter]{querier: q},
		toolExecutor: toolExecutor[models.StreamCompleter]{querier: q},
	}
	err := runner.Run(context.Background(), session)
	var unsupported *claierr.ImageUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Run err = %v, want *claierr.ImageUnsupportedError", err)
	}
	if n := len(bodies()); n != 0 {
		t.Fatalf("want zero HTTP requests, got %d", n)
	}
}

func TestTurn_NoImageRouteToolImage(t *testing.T) {
	setTurnVendorKeys(t)
	toolPNG := pngOf(t, 120)
	label := "[image " + handleOf(t, toolPNG) + "]"
	snap := partsTool{name: "snap", parts: []pub_models.Part{
		pub_models.TextPart{Text: "snapshot"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: toolPNG},
	}}
	none := (&inception.Inception{}).ImageCapabilities()
	model := &turnModel{caps: none, steps: [][]models.CompletionEvent{{call("c1", "snap", nil), models.StopEvent{}}}}
	f := newTurnFixture(t, model, snap)
	f.diskTurn(present.Options{})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "snap"}); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	requests := model.received()
	if len(requests) != 2 {
		t.Fatalf("turn 1: want 2 requests, got %d", len(requests))
	}
	sent := lastOfRole(t, requests[1], "tool")
	if got := texts(sent.Parts); !slices.Equal(got, []string{"snapshot", label}) || len(imagesOf(sent.Parts)) != 0 {
		t.Fatalf("turn 1 request 2 tool message = %+v, want its text and %s, without the image", sent, label)
	}
	promoted := f.promoted(t)
	if imgs := imagesOf(lastOfRole(t, promoted, "tool").Parts); len(imgs) != 1 || imgs[0].Asset == "" {
		t.Fatalf("persisted tool message holds %+v, want the stored image", imgs)
	}

	srv, bodies := recordingServer(t, chatCompletionsDone)
	f.q.Model = aimGeneric(&inception.Inception{Model: "mercury"}, func(v *inception.Inception) *generic.StreamCompleter { return &v.StreamCompleter })(t, srv.URL)
	next := append(slices.Clone(promoted.Messages), pub_models.Message{Role: "user", Content: "and now?"})
	if _, err := f.run(t, next...); err != nil {
		t.Fatalf("turn 2 over the persisted tool image: %v", err)
	}
	sentBodies := bodies()
	if len(sentBodies) != 1 {
		t.Fatalf("turn 2: want one request, got %d", len(sentBodies))
	}
	content, err := json.Marshal("snapshot\n" + label)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sentBodies[0], string(content)) {
		t.Errorf("turn 2 body lacks the tool result %s: %s", content, sentBodies[0])
	}
	if strings.Contains(sentBodies[0], base64.StdEncoding.EncodeToString(toolPNG)) {
		t.Error("turn 2 sent the image bytes to a route that accepts no images")
	}
}

func TestTurn_RefusalNamesRoute(t *testing.T) {
	t.Setenv("INCEPTION_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_KEY", "")
	srv, bodies := recordingServer(t, chatCompletionsDone)
	mercury := func(t *testing.T, turn *ImageTurn) *Querier[*inception.Inception] {
		t.Helper()
		q, err := NewQuerier(t.Context(), Configurations{
			Model: "mercury", ConfigDir: t.TempDir(), Out: io.Discard, SkipAmbientMcpServers: true,
			CostWarnf: func(string, ...any) {}, ImageTurn: turn,
		}, &inception.Inception{Model: "mercury"})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		q.Model.StreamCompleter.URL = srv.URL
		return &q
	}
	assertNamed := func(t *testing.T, err error, reason string) {
		t.Helper()
		var refusal *claierr.ImageUnsupportedError
		if !errors.As(err, &refusal) {
			t.Fatalf("err = %v, want *claierr.ImageUnsupportedError", err)
		}
		if refusal.Vendor != "inception" || refusal.Model != "mercury" || refusal.Reason != reason {
			t.Fatalf("refusal = %+v, want vendor inception, model mercury, reason %q", *refusal, reason)
		}
		if !strings.Contains(err.Error(), `vendor "inception", model "mercury"`) {
			t.Fatalf("err %q does not name the vendor and the model", err)
		}
	}

	t.Run("user image", func(t *testing.T) {
		_, err := mercury(t, nil).TextQuery(t.Context(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "what is it?"},
			pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: pngOf(t, 121)},
		}}}})
		assertNamed(t, err, "route accepts no images")
	})
	if n := len(bodies()); n != 0 {
		t.Fatalf("want zero HTTP requests, got %d", n)
	}
}
