package vendors_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/models/modelstest"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/berget"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/huggingface"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/jev"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type wireShape int

const (
	wireChatCompletions wireShape = iota
	wireResponses
	wireAnthropic
	wireJev
	wireMock
)

const (
	chatReply      = "data: [DONE]\n\n"
	responsesReply = "data: {\"type\":\"response.completed\"}\n\n"
	anthropicReply = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	jevReply       = `{"model":"jev-latest","answers":{"urgent":true},"usage":{"input_tokens":1,"output_tokens":1}}`
)

// readmeMIMEs are the keys of the README MIME-to-extension parameter, in order.
func readmeMIMEs() []string { return []string{"image/png", "image/jpeg", "image/gif", "image/webp"} }

type encoderCase struct {
	name   string
	vendor string
	model  string
	caps   models.ImageCapabilities
	wire   wireShape
	fresh  func() models.ImageEncoder
	aim    func(enc models.ImageEncoder, url string)
}

func aimEmbedded[P models.ImageEncoder](inner func(P) *generic.StreamCompleter) func(models.ImageEncoder, string) {
	return func(enc models.ImageEncoder, url string) { inner(enc.(P)).URL = url }
}

// encoderCases holds every ImageEncoder with its Local fact P5-1 row.
func encoderCases() []encoderCase {
	all := readmeMIMEs()
	noGIF := []string{"image/png", "image/jpeg", "image/webp"}
	inline := func(tr models.ToolResultImages, mimes []string) models.ImageCapabilities {
		return models.ImageCapabilities{Input: models.ImageInputInline, ToolResult: tr, MIMEs: mimes}
	}
	none := models.ImageCapabilities{Input: models.ImageInputNone, ToolResult: models.ToolResultFallback}
	native, fallback := models.ToolResultNative, models.ToolResultFallback
	return []encoderCase{
		{
			name: "openai", vendor: "openai", model: "gpt-4.1-mini", caps: inline(native, all), wire: wireResponses,
			fresh: func() models.ImageEncoder { return &openai.ChatGPT{Model: "gpt-4.1-mini"} },
			aim:   func(enc models.ImageEncoder, url string) { enc.(*openai.ChatGPT).URL = url + "/v1/responses" },
		},
		{
			name: "openai-chat-completions", vendor: "openai", model: "gpt-4.1-mini", caps: inline(fallback, all), wire: wireChatCompletions,
			fresh: func() models.ImageEncoder { return &openai.ChatGPT{Model: "gpt-4.1-mini", URL: openai.ChatURL} },
			aim:   func(enc models.ImageEncoder, url string) { enc.(*openai.ChatGPT).URL = url + "/v1/chat/completions" },
		},
		{
			name: "anthropic", vendor: "anthropic", model: "claude-sonnet-5-5", caps: inline(native, all), wire: wireAnthropic,
			fresh: func() models.ImageEncoder {
				return &anthropic.Claude{Model: "claude-sonnet-5-5", MaxTokens: 1024, AnthropicVersion: "2023-06-01"}
			},
			aim: func(enc models.ImageEncoder, url string) { enc.(*anthropic.Claude).URL = url + "/v1/messages" },
		},
		{
			name: "jev", vendor: "jev", model: "jev-latest", caps: none, wire: wireJev,
			fresh: func() models.ImageEncoder {
				j := &jev.Jev{Model: "jev-latest"}
				j.SetResponseFormat(&generic.ResponseFormat{Type: "json_schema", JSONSchema: &generic.JSONSchemaSpec{
					Name:   "triage",
					Schema: map[string]any{"urgent": map[string]any{"type": "noul", "instructions": "Is it urgent?"}},
				}})
				return j
			},
			aim: func(enc models.ImageEncoder, url string) { enc.(*jev.Jev).URL = url },
		},
		{
			name: "mock", vendor: "mock", model: "mock", caps: inline(native, all), wire: wireMock,
			fresh: func() models.ImageEncoder { return &vendors.Mock{} },
			aim:   func(models.ImageEncoder, string) {},
		},
		{
			name: "berget", vendor: "berget", model: "google/gemma-4-31B-it", caps: inline(fallback, noGIF),
			fresh: func() models.ImageEncoder { return &berget.Berget{Model: "google/gemma-4-31B-it"} },
			aim:   aimEmbedded(func(v *berget.Berget) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "deepseek", vendor: "deepseek", model: "deepseek-chat", caps: inline(fallback, all),
			fresh: func() models.ImageEncoder { return &deepseek.Deepseek{Model: "deepseek-chat"} },
			aim:   aimEmbedded(func(v *deepseek.Deepseek) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "gemini", vendor: "gemini", model: "gemini-3.1-flash-lite", caps: inline(fallback, noGIF),
			fresh: func() models.ImageEncoder { return &gemini.Gemini{Model: "gemini-3.1-flash-lite"} },
			aim:   aimEmbedded(func(v *gemini.Gemini) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "huggingface", vendor: "huggingface", model: huggingface.DefaultModelName, caps: inline(fallback, noGIF),
			fresh: func() models.ImageEncoder { return &huggingface.HuggingFaceChat{Model: huggingface.DefaultModelName} },
			aim:   aimEmbedded(func(v *huggingface.HuggingFaceChat) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "inception", vendor: "inception", model: "mercury", caps: none,
			fresh: func() models.ImageEncoder { return &inception.Inception{Model: "mercury"} },
			aim:   aimEmbedded(func(v *inception.Inception) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "mistral", vendor: "mistral", model: "mistral-medium-latest", caps: inline(native, all),
			fresh: func() models.ImageEncoder { return &mistral.Mistral{Model: "mistral-medium-latest"} },
			aim:   aimEmbedded(func(v *mistral.Mistral) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "novita", vendor: "novita", model: "moonshotai/kimi-k3", caps: inline(native, noGIF),
			fresh: func() models.ImageEncoder { return &novita.Novita{Model: "moonshotai/kimi-k3"} },
			aim:   aimEmbedded(func(v *novita.Novita) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			name: "ollama", vendor: "ollama", model: "llama3", caps: inline(fallback, noGIF),
			fresh: func() models.ImageEncoder { return &ollama.Ollama{Model: "llama3"} },
			aim:   aimEmbedded(func(v *ollama.Ollama) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
		{
			// The production URL shape keeps the default modality lookup on, so the zero-request
			// rows count its catalog fetch too.
			name: "openrouter", vendor: "openrouter", model: "google/gemini-2.5-flash", caps: inline(native, all),
			fresh: func() models.ImageEncoder { return &openrouter.OpenRouter{Model: "or:google/gemini-2.5-flash"} },
			aim: func(enc models.ImageEncoder, url string) {
				enc.(*openrouter.OpenRouter).StreamCompleter.URL = url + "/api/v1/chat/completions"
			},
		},
		{
			name: "xai", vendor: "xai", model: "grok-4", caps: inline(fallback, []string{"image/png", "image/jpeg"}),
			fresh: func() models.ImageEncoder { return &xai.XAI{Model: "grok-4"} },
			aim:   aimEmbedded(func(v *xai.XAI) *generic.StreamCompleter { return &v.StreamCompleter }),
		},
	}
}

func setVendorKeys(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "TYPESAFE_API_KEY", "BERGET_API_KEY", "DEEPSEEK_API_KEY",
		"GEMINI_API_KEY", huggingface.EnvAPITokenKey, "INCEPTION_API_KEY", "MISTRAL_API_KEY",
		"NOVITA_API_KEY", "OLLAMA_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY",
	} {
		t.Setenv(k, "test-key")
	}
}

type exchangeResult struct {
	bodies [][]byte
	paths  []string
	text   string
	err    error
}

// catalogPath is the OpenRouter model catalog its modality lookup fetches (phase 6 O8).
const catalogPath = "/api/v1/models"

const catalogReply = `{"data":[{"id":"google/gemini-2.5-flash","architecture":{"output_modalities":["text"]}}]}`

func replyFor(w wireShape, path string) string {
	switch {
	case path == catalogPath:
		return catalogReply
	case strings.HasSuffix(path, "/count_tokens"):
		return `{"input_tokens":3}`
	case w == wireResponses:
		return responsesReply
	case w == wireAnthropic:
		return anthropicReply
	case w == wireJev:
		return jevReply
	default:
		return chatReply
	}
}

// exchange sets up a fresh encoder for c, aims it at a recording server and streams chat.
// Every request counts, catalog fetches included; an empty cache dir makes each lookup fetch.
func exchange(t *testing.T, c encoderCase, chat pub_models.Chat) exchangeResult {
	t.Helper()
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
	var (
		mu  sync.Mutex
		res exchangeResult
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		mu.Lock()
		res.bodies = append(res.bodies, b)
		res.paths = append(res.paths, r.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, replyFor(c.wire, r.URL.Path))
	}))
	defer srv.Close()

	enc := c.fresh()
	if err := enc.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	c.aim(enc, srv.URL)
	ch, err := enc.StreamCompletions(context.Background(), chat)
	var text strings.Builder
	if err == nil {
		for ev := range ch {
			if s, ok := ev.(string); ok {
				text.WriteString(s)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	res.text = text.String()
	res.err = err
	return res
}

type probeImage struct {
	data  []byte
	b64   string
	asset string
}

func loadProbe(t *testing.T) probeImage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "image_encoder", "probe-glyph-K7Q.png"))
	if err != nil {
		t.Fatalf("read probe image: %v", err)
	}
	sum := sha256.Sum256(data)
	return probeImage{data: data, b64: base64.StdEncoding.EncodeToString(data), asset: hex.EncodeToString(sum[:]) + ".png"}
}

func (p probeImage) part(mime string) pub_models.ImagePart {
	return pub_models.ImagePart{Asset: p.asset, MIME: mime, Origin: pub_models.OriginUser, Data: p.data}
}

func (p probeImage) handle() string { return p.asset[:6] }

func textChat() pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "You are terse."},
		{Role: "user", Content: "list the files"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{
			ID: "call_1", Name: "ls", Type: "function", Inputs: &pub_models.Input{"directory": "."},
			Function: pub_models.Specification{Name: "ls", Arguments: `{"directory":"."}`},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "a.txt"},
		{Role: "assistant", Content: "One file: a.txt"},
		{Role: "user", Content: "thanks"},
	}}
}

// lastItem decodes body and returns the last element of its list under key.
func lastItem(t *testing.T, body []byte, key string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v\n%s", err, body)
	}
	list, ok := decoded[key].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("body has no %q list: %s", key, body)
	}
	item, ok := list[len(list)-1].(map[string]any)
	if !ok {
		t.Fatalf("last %q element is not an object: %s", key, body)
	}
	return item
}

func assertJSON(t *testing.T, got any, want string) {
	t.Helper()
	var wantV any
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}
	if !reflect.DeepEqual(got, wantV) {
		gotB, _ := json.Marshal(got)
		t.Fatalf("wire shape mismatch:\n got: %s\nwant: %s", gotB, want)
	}
}

// oneBody is the single completion request; a catalog fetch beside it is not one.
func oneBody(t *testing.T, res exchangeResult) []byte {
	t.Helper()
	if res.err != nil {
		t.Fatalf("StreamCompletions: %v", res.err)
	}
	var bodies [][]byte
	for i, body := range res.bodies {
		if res.paths[i] != catalogPath {
			bodies = append(bodies, body)
		}
	}
	if len(bodies) != 1 {
		t.Fatalf("completion requests = %d, want 1 (paths %q)", len(bodies), res.paths)
	}
	return bodies[0]
}

func textPart(w wireShape, text string) string {
	b, _ := json.Marshal(text)
	switch w {
	case wireResponses:
		return `{"type":"input_text","text":` + string(b) + `}`
	default:
		return `{"type":"text","text":` + string(b) + `}`
	}
}

func imagePart(w wireShape, img probeImage) string {
	switch w {
	case wireResponses:
		return `{"type":"input_image","image_url":"data:image/png;base64,` + img.b64 + `"}`
	case wireAnthropic:
		return `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + img.b64 + `"}}`
	default:
		return `{"type":"image_url","image_url":{"url":"data:image/png;base64,` + img.b64 + `"}}`
	}
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "image_encoder", name+".text.golden")
}

// Proves E1–E6, I3, I4, I8, ImageCapabilities.1 and AC1–AC5 per encoder.
func TestImageEncoderContract(t *testing.T) {
	setVendorKeys(t)
	img := loadProbe(t)
	for _, c := range encoderCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("TextUnchanged", func(t *testing.T) {
				res := exchange(t, c, textChat())
				var got []byte
				if c.wire == wireMock {
					if res.err != nil {
						t.Fatalf("StreamCompletions: %v", res.err)
					}
					got = []byte(res.text)
				} else {
					got = oneBody(t, res)
				}
				want, err := os.ReadFile(goldenPath(c.name))
				if err != nil {
					t.Fatalf("read recorded pre-worklog body: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("text-only body changed:\n got: %s\nwant: %s", got, want)
				}
			})

			t.Run("TextParts", func(t *testing.T) { assertTextParts(t, c) })
			if c.caps.Input == models.ImageInputInline {
				t.Run("UserImage", func(t *testing.T) { assertUserImage(t, c, img) })
			}
			if c.caps.Input == models.ImageInputInline && c.caps.ToolResult == models.ToolResultNative {
				t.Run("ToolResultNative", func(t *testing.T) { assertToolResultNative(t, c, img) })
			}

			modelstest.RunEncoderCases(t, modelstest.EncoderSubject{
				Vendor: c.vendor,
				Model:  c.model,
				Caps:   c.caps,
				Image:  img.part("image/png"),
				New:    c.fresh,
				Send: func(t *testing.T, chat pub_models.Chat) modelstest.Exchange {
					res := exchange(t, c, chat)
					return modelstest.Exchange{Err: res.err, Requests: len(res.bodies)}
				},
			})
		})
	}
}

// textPartsChat carries text-only Parts in every role.
func textPartsChat() pub_models.Chat {
	parts := func(texts ...string) []pub_models.Part {
		out := make([]pub_models.Part, 0, len(texts))
		for _, text := range texts {
			out = append(out, pub_models.TextPart{Text: text})
		}
		return out
	}
	chat := textChat()
	chat.Messages[0] = pub_models.Message{Role: "system", Parts: parts("Be brief.")}
	chat.Messages[1] = pub_models.Message{Role: "user", Parts: parts("list", "the files")}
	chat.Messages[3] = pub_models.Message{Role: "tool", ToolCallID: "call_1", Parts: parts("a.txt")}
	chat.Messages[4] = pub_models.Message{Role: "assistant", Parts: parts("One file")}
	chat.Messages[5] = pub_models.Message{Role: "user", Parts: parts("thanks")}
	return chat
}

func assertTextParts(t *testing.T, c encoderCase) {
	t.Helper()
	res := exchange(t, c, textPartsChat())
	if c.wire == wireMock {
		if res.err != nil || res.text != "done after tool for: thanks" {
			t.Fatalf("mock: text %q, err %v", res.text, res.err)
		}
		return
	}
	body := oneBody(t, res)
	want := []string{"Be brief.", "list", "the files", "a.txt", "One file", "thanks"}
	if c.wire == wireJev {
		want = []string{"list\\nthe files", "thanks"}
	}
	for _, text := range want {
		if !bytes.Contains(body, []byte(text)) {
			t.Fatalf("body lacks %q: %s", text, body)
		}
	}
}

func assertUserImage(t *testing.T, c encoderCase, img probeImage) {
	t.Helper()
	res := exchange(t, c, modelstest.UserImageChat(img.part("image/png")))
	if c.wire == wireMock {
		if res.err != nil {
			t.Fatalf("StreamCompletions: %v", res.err)
		}
		if want := "[image ab12cd]\n[image " + img.handle() + "]\nwhat?"; res.text != want {
			t.Fatalf("mock echo = %q, want %q", res.text, want)
		}
		return
	}
	body := oneBody(t, res)
	if bytes.Contains(body, []byte(img.asset)) {
		t.Fatalf("body carries the asset ref %q", img.asset)
	}
	key := "messages"
	if c.wire == wireResponses {
		key = "input"
	}
	want := "[" + textPart(c.wire, "[image ab12cd]") + "," + imagePart(c.wire, img) + "," + textPart(c.wire, "what?") + "]"
	assertJSON(t, lastItem(t, body, key)["content"], want)
}

func assertToolResultNative(t *testing.T, c encoderCase, img probeImage) {
	t.Helper()
	res := exchange(t, c, modelstest.ToolImageChat(img.part("image/png")))
	switch c.wire {
	case wireMock:
		if res.err != nil || res.text != "done after tool for: show me" {
			t.Fatalf("mock: text %q, err %v", res.text, res.err)
		}
	case wireResponses:
		item := lastItem(t, oneBody(t, res), "input")
		if item["type"] != "function_call_output" {
			t.Fatalf("last input item = %v, want function_call_output", item)
		}
		assertJSON(t, item["output"], "["+textPart(c.wire, "loaded")+","+imagePart(c.wire, img)+"]")
	case wireAnthropic:
		item := lastItem(t, oneBody(t, res), "messages")
		assertJSON(t, item["content"], `[{"type":"tool_result","content":[`+textPart(c.wire, "loaded")+","+imagePart(c.wire, img)+`],"tool_use_id":"call_1"}]`)
	default:
		item := lastItem(t, oneBody(t, res), "messages")
		if item["role"] != "tool" {
			t.Fatalf("last message = %v, want the tool message", item)
		}
		assertJSON(t, item["content"], "["+textPart(c.wire, "loaded")+","+imagePart(c.wire, img)+"]")
	}
}

// Proves E2 for CountInputTokens.
func TestAnthropicCountTokens_Images(t *testing.T) {
	setVendorKeys(t)
	img := loadProbe(t)
	i := slices.IndexFunc(encoderCases(), func(c encoderCase) bool { return c.name == "anthropic" })
	c := encoderCases()[i]
	// The count request is only sent for an anthropic.com URL with
	// print_input_count set; this one routes to the test server.
	c.aim = func(enc models.ImageEncoder, url string) {
		claude := enc.(*anthropic.Claude)
		claude.URL = url + "/api.anthropic.com/v1/messages"
		claude.PrintInputCount = true
	}
	res := exchange(t, c, modelstest.UserImageChat(img.part("image/png")))
	if res.err != nil {
		t.Fatalf("StreamCompletions: %v", res.err)
	}
	byPath := map[string][]byte{}
	for i, p := range res.paths {
		byPath[p] = res.bodies[i]
	}
	count, stream := byPath["/api.anthropic.com/v1/messages/count_tokens"], byPath["/api.anthropic.com/v1/messages"]
	if count == nil || stream == nil {
		t.Fatalf("want a count and a stream request, got paths %v", res.paths)
	}
	var countReq, streamReq struct {
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(count, &countReq); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stream, &streamReq); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(countReq.Messages, streamReq.Messages) {
		t.Fatalf("count messages differ from stream messages:\ncount:  %s\nstream: %s", countReq.Messages, streamReq.Messages)
	}
	if !bytes.Contains(countReq.Messages, []byte(img.b64)) || bytes.Contains(countReq.Messages, []byte(`"text":""`)) {
		t.Fatalf("count request must carry the image block and no empty text block: %s", countReq.Messages)
	}
}
