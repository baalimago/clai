package e2etest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const tokensPerMessage = 10

// RecordedRequest is one request body the stand-in received.
type RecordedRequest struct {
	Body     string
	Model    string
	Messages []json.RawMessage
	// Rest holds every top-level member but messages.
	Rest map[string]json.RawMessage
}

// UsageReport is the usage the stand-in answered one request with.
type UsageReport struct {
	Prompt, Cached, Total int
}

// CachingProvider is a stand-in that records every request and reports usage with prefix caching.
type CachingProvider struct {
	url    string
	claude bool
	reply  string

	mu        sync.Mutex
	requests  []RecordedRequest
	usages    []UsageReport
	hold      int
	inflight  int
	maxFlight int
	failFrom  int
	failCode  int
}

// StartClaudeProvider is StartCachingProvider speaking the Messages API.
func StartClaudeProvider(t testing.TB, reply string) *CachingProvider {
	t.Helper()
	return start(t, reply, true)
}

// StartCachingProvider serves the chat-completions shape until the test ends.
func StartCachingProvider(t testing.TB, reply string) *CachingProvider {
	t.Helper()
	return start(t, reply, false)
}

func start(t testing.TB, reply string, claude bool) *CachingProvider {
	t.Helper()
	p := &CachingProvider{reply: reply, claude: claude, failFrom: -1}
	original := http.DefaultTransport
	http.DefaultTransport = LoopbackOnly{Base: original}
	t.Cleanup(func() { http.DefaultTransport = original })
	server := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(server.Close)
	p.url = server.URL
	return p
}

const holdTimeout = 10 * time.Second

// HoldUntil makes every answer wait until n requests are in flight at once, or until a timeout.
func (p *CachingProvider) HoldUntil(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold = n
}

// MaxInFlight is the most requests the stand-in has had open at the same time.
func (p *CachingProvider) MaxInFlight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxFlight
}

func (p *CachingProvider) enter() {
	p.mu.Lock()
	p.inflight++
	p.maxFlight = max(p.maxFlight, p.inflight)
	hold := p.hold
	p.mu.Unlock()
	deadline := time.Now().Add(holdTimeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		reached := p.maxFlight >= hold
		p.mu.Unlock()
		if reached {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *CachingProvider) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inflight--
}

// FailFrom answers request i and every later one with status.
func (p *CachingProvider) FailFrom(i, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failFrom, p.failCode = i, status
}

// WriteModel points model (a gpt-* name) at the stand-in for the runs using confDir and sets a dummy key.
func (p *CachingProvider) WriteModel(t testing.TB, confDir, model string) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "stub-key")
	p.writeConfig(t, confDir, "openai_gpt_"+model, model, "/v1/chat/completions")
}

// WriteClaudeModel writes anthropic_claude_<model>.json and sets ANTHROPIC_API_KEY.
func (p *CachingProvider) WriteClaudeModel(t testing.TB, confDir, model string) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "stub-key")
	p.writeConfig(t, confDir, "anthropic_claude_"+model, model, "/v1/messages")
}

// WriteOpenRouterModel points an openrouter model (written "or:<model>") at the stand-in.
func (p *CachingProvider) WriteOpenRouterModel(t testing.TB, confDir, model string) {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "stub-key")
	p.writeConfig(t, confDir, "openrouter_chat_"+strings.ReplaceAll(model, "/", "_"), model, "/api/v1/chat/completions")
	target, err := url.Parse(p.url)
	if err != nil {
		t.Fatalf("parse stand-in url: %v", err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = hostRewrite{base: original, from: "openrouter.ai", to: target.Host}
	t.Cleanup(func() { http.DefaultTransport = original })
}

type hostRewrite struct {
	base     http.RoundTripper
	from, to string
}

func (h hostRewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != h.from {
		return h.base.RoundTrip(r)
	}
	out := r.Clone(r.Context())
	out.URL.Scheme = "http"
	out.URL.Host = h.to
	out.Host = h.to
	return h.base.RoundTrip(out)
}

func (p *CachingProvider) writeConfig(t testing.TB, confDir, name, model, route string) {
	t.Helper()
	conf, err := json.Marshal(map[string]any{
		"model": model,
		"url":   p.url + route,
		"price": map[string]any{"input_usd_per_token": 0.001, "input_cached_usd_per_token": 0.0005, "output_usd_per_token": 0.002},
	})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, name+".json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}

// Requests returns the bodies in arrival order.
func (p *CachingProvider) Requests() []RecordedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]RecordedRequest(nil), p.requests...)
}

// Usage is the prompt, cached and total tokens reported for request i.
func (p *CachingProvider) Usage(i int) UsageReport {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.usages[i]
}

func decodeRequest(body string) (RecordedRequest, error) {
	rec := RecordedRequest{Body: body}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		return rec, err
	}
	if err := json.Unmarshal(top["messages"], &rec.Messages); err != nil {
		return rec, fmt.Errorf("messages: %w", err)
	}
	if err := json.Unmarshal(top["model"], &rec.Model); err != nil {
		return rec, fmt.Errorf("model: %w", err)
	}
	delete(top, "messages")
	rec.Rest = top
	return rec, nil
}

func sharedPrefix(a, b []json.RawMessage) int {
	n := 0
	for n < len(a) && n < len(b) && bytes.Equal(a[n], b[n]) {
		n++
	}
	return n
}

// record stores the request and returns its index, its usage and whether it must fail.
func (p *CachingProvider) record(rec RecordedRequest) (int, UsageReport, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	best := 0
	for _, earlier := range p.requests {
		best = max(best, sharedPrefix(rec.Messages, earlier.Messages))
	}
	usage := UsageReport{Prompt: tokensPerMessage * len(rec.Messages)}
	_, hasField := rec.Rest["cache_control"]
	if !p.claude || hasField {
		usage.Cached = tokensPerMessage * best
	}
	usage.Total = usage.Prompt + tokensPerMessage
	idx := len(p.requests)
	p.requests = append(p.requests, rec)
	p.usages = append(p.usages, usage)
	status := 0
	if p.failFrom >= 0 && idx >= p.failFrom {
		status = p.failCode
	}
	return idx, usage, status
}

func (p *CachingProvider) serve(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec, err := decodeRequest(string(raw))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, usage, status := p.record(rec)
	p.enter()
	defer p.leave()
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"stand-in refuses this request","type":"server_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if p.claude {
		p.writeClaude(w, usage)
		return
	}
	p.writeChat(w, usage)
}

func (p *CachingProvider) writeChat(w io.Writer, u UsageReport) {
	content, _ := json.Marshal(p.reply)
	fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", content)
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d,\"prompt_tokens_details\":{\"cached_tokens\":%d}}}\n\n",
		u.Prompt, tokensPerMessage, u.Total, u.Cached)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (p *CachingProvider) writeClaude(w io.Writer, u UsageReport) {
	text, _ := json.Marshal(p.reply)
	event := func(name, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data) }
	event("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":%d,"output_tokens":1}}}`, u.Prompt-u.Cached, u.Cached))
	event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, text))
	event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, tokensPerMessage))
	event("message_stop", `{"type":"message_stop"}`)
}
