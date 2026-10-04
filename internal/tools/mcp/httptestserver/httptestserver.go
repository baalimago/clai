// Package httptestserver is the fake streamable-HTTP MCP server this
// worklog's transport phase introduces and every later phase consumes
// (worklog 2026-10-02-mcp-connection-cost, phase 4, README shared
// interfaces). It serves on a loopback listener and is configurable per
// test to answer a POST with JSON or an event stream, to supply or omit a
// session header, to accept or reject the server-initiated GET stream, to
// answer with an authorization challenge whose resource_metadata points at
// a caller-supplied URL, and to answer a tool call only when a given bearer
// token is presented. No later phase introduces a second MCP-over-HTTP
// fake; the authorization phase configures this one.
package httptestserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// SessionHeader is the header name a streamable-HTTP server uses to assign
// and later require a session identifier.
const SessionHeader = "Mcp-Session-Id"

// ProtocolVersionHeader is the header name the client advertises its
// negotiated protocol version on.
const ProtocolVersionHeader = "Mcp-Protocol-Version"

// sessionToken is the fixed session id this fixture assigns when
// AssignSession is enabled. A real server would mint one per connection;
// one fixed value is enough to prove the client carries it back.
const sessionToken = "test-session-token"

// Tool describes one tool this fixture advertises from tools/list.
type Tool struct {
	Name        string
	Description string
}

// Config is the fixture's mutable behaviour. It carries no lock of its
// own: Server.Configure and Server.snapshot are the only ways to read or
// write it, both under Server's mutex, so a Config value itself is always
// safe to copy.
type Config struct {
	// ResponseSSE selects how every successful POST response (other than an
	// accepted-with-no-body notification) is framed: SSE when true, a plain
	// JSON body otherwise.
	ResponseSSE bool

	AssignSession            bool
	RejectSessionAfterInit   bool
	RejectGetStream          bool
	LegacyOnly               bool
	ChallengeAlways          bool
	ChallengeResourceMetaURL string
	RequireBearerToken       string
	FailStatus               int
	FailMessage              string
	UnsupportedContentType   bool
	OversizedResponseBytes   int
	MalformedSSEFrame        bool
	TruncateSSEStream        bool
	Tools                    []Tool
	UnknownToolName          string
	// HangForever never answers a POST: the connection is accepted but no
	// response is ever written, for a connect-bound expiry test.
	HangForever bool
	// RequireProtocolVersion, when set, fails a POST whose
	// Mcp-Protocol-Version header does not match exactly.
	RequireProtocolVersion string
	// RedirectPostTo answers every POST with a 307 to this URL instead of
	// handling it. Go resends a POST body across a 307, so this is the
	// shape that would hand the JSON-RPC request, and the bearer token
	// decorating it, to the target (sign-off review, B2).
	RedirectPostTo string
	// HoldPostStreamOpen flushes the ResponseSSE result frame and then holds
	// the POST response body open (bounded, like HangForever) instead of
	// ending it, exercising the sign-off review's B1: the specification
	// permits a server to keep the stream open after answering ("SHOULD"
	// close it, not "MUST"), so a conformant client must resolve the call
	// once the frame arrives rather than when the body ends.
	HoldPostStreamOpen bool
}

// Server is the fixture: an httptest.Server plus the Config its handlers
// read. URL is the endpoint a Conn under test is configured against.
type Server struct {
	URL string
	srv *httptest.Server

	mu  sync.Mutex
	cfg Config

	streamsMu sync.Mutex
	streams   []*activeStream

	obsMu              sync.Mutex
	lastSessionHeader  string
	lastNonInitSeen    bool
	postCount          int
	postedResponses    map[int]json.RawMessage
	lastDeletedSession string
	deleteCount        int
	getStreamCount     int
	lastEventIDHeader  string
}

type activeStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	// drop lets a test simulate the server-initiated stream dying without
	// cancelling the request's own context, for R2-11's reconnect proof.
	drop chan struct{}
}

// New builds and starts a Server. Its default configuration answers a
// plain JSON-RPC handshake with one "echo" tool, no session, and allows the
// server-initiated GET stream.
func New() *Server {
	s := &Server{cfg: Config{Tools: []Tool{{Name: "echo", Description: "echo text"}}}, postedResponses: make(map[int]json.RawMessage)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.srv = httptest.NewServer(mux)
	s.URL = s.srv.URL
	return s
}

// Close shuts the fixture down.
func (s *Server) Close() { s.srv.Close() }

// Configure runs fn under the config's lock, so a test can flip behaviour
// (including mid-test, after an initial handshake already completed)
// without racing a concurrent request handler.
func (s *Server) Configure(fn func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
}

func (s *Server) snapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// LastSessionHeaderSeen reports the Mcp-Session-Id header value the most
// recent non-initialize POST carried, and whether any such request has
// arrived yet.
func (s *Server) LastSessionHeaderSeen() (string, bool) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastSessionHeader, s.lastNonInitSeen
}

// PostRequestCount reports how many POST requests this fixture has
// answered so far, so a warm-cache test can assert that a cache hit issues
// no further request to the endpoint until a tool is actually called
// (R1-15).
func (s *Server) PostRequestCount() int {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.postCount
}

// StreamCount reports how many server-initiated GET streams are currently
// open, so a test can wait for one before pushing a notification.
func (s *Server) StreamCount() int {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return len(s.streams)
}

// PushNotification writes one JSON-RPC notification frame to every
// currently open server-initiated GET stream. It is how a test simulates a
// server pushing notifications/tools/list_changed.
func (s *Server) PushNotification(method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("httptestserver: encode notification: %w", err)
	}
	s.broadcastSSE(body)
	return nil
}

// PushServerRequest writes one JSON-RPC request frame (carrying id) to
// every currently open server-initiated GET stream, simulating a server
// that itself issues a request over the duplex the GET stream opens (e.g.
// roots/list). The client's POST'd answer is observable through
// PostedResponse.
func (s *Server) PushServerRequest(id int, method string) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method})
	if err != nil {
		return fmt.Errorf("httptestserver: encode server request: %w", err)
	}
	s.broadcastSSE(body)
	return nil
}

// broadcastSSE writes body as one SSE frame, with no id: field, to every
// currently open server-initiated GET stream, shared by every Push*
// method that does not need one.
func (s *Server) broadcastSSE(body []byte) {
	s.broadcastSSEWithID("", body)
}

// broadcastSSEWithID is broadcastSSE's general form: id, when non-empty,
// is written as the event's id: field (R2-11's resumption token).
func (s *Server) broadcastSSEWithID(id string, body []byte) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	for _, st := range s.streams {
		if id != "" {
			fmt.Fprintf(st.w, "id: %s\n", id)
		}
		writeSSEFrame(st.w, body)
		st.flusher.Flush()
	}
}

// PostedResponse reports the JSON-RPC response body the client POSTed for
// id, if any has arrived yet.
func (s *Server) PostedResponse(id int) (json.RawMessage, bool) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	raw, ok := s.postedResponses[id]
	return raw, ok
}

// DeleteCount reports how many DELETE requests this fixture has answered,
// and LastDeletedSession reports the Mcp-Session-Id header the most recent
// one carried (R2-10): the streamable-HTTP spec's session-termination
// signal.
func (s *Server) DeleteCount() int {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.deleteCount
}

func (s *Server) LastDeletedSession() (string, bool) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastDeletedSession, s.deleteCount > 0
}

// GetStreamRequestCount reports how many times a client has opened the
// server-initiated GET stream (R2-11): a reconnect after a drop issues a
// second one.
func (s *Server) GetStreamRequestCount() int {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.getStreamCount
}

// LastEventIDHeaderSeen reports the Last-Event-ID header value the most
// recent GET for the server-initiated stream carried, empty on the first
// connection.
func (s *Server) LastEventIDHeaderSeen() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastEventIDHeader
}

// PushNotificationWithID writes one JSON-RPC notification frame carrying
// an SSE id: field to every currently open server-initiated GET stream, so
// a test can later assert the client reconnects with that id as
// Last-Event-ID (R2-11).
func (s *Server) PushNotificationWithID(id, method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("httptestserver: encode notification: %w", err)
	}
	s.broadcastSSEWithID(id, body)
	return nil
}

// DropStreams ends every currently open server-initiated stream without
// cancelling its request context, simulating a dropped connection (a load
// balancer or idle timeout) rather than the client itself closing down, so
// a reconnect attempt is the only way the stream continues (R2-11).
func (s *Server) DropStreams() {
	s.streamsMu.Lock()
	streams := s.streams
	s.streams = nil
	s.streamsMu.Unlock()
	for _, st := range streams {
		close(st.drop)
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	case http.MethodDelete:
		s.handleDelete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshot()

	s.obsMu.Lock()
	s.getStreamCount++
	s.lastEventIDHeader = r.Header.Get("Last-Event-ID")
	s.obsMu.Unlock()

	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cfg.LegacyOnly {
		// The legacy handshake: the client is expected to open the stream
		// first and learn a separate POST endpoint from an "endpoint"
		// event. This fixture never sends one; dialHttp only needs the
		// status and content type to recognise the signal.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		return
	}
	if cfg.RejectGetStream {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	st := &activeStream{w: w, flusher: flusher, drop: make(chan struct{})}
	s.streamsMu.Lock()
	s.streams = append(s.streams, st)
	s.streamsMu.Unlock()
	defer s.removeStream(st)

	select {
	case <-r.Context().Done():
	case <-st.drop:
	}
}

// handleDelete answers the client's session-termination request
// (R2-10): the streamable-HTTP spec has a client holding a session id send
// DELETE with Mcp-Session-Id to end it. Recorded for test inspection, then
// answered 200 unconditionally; this fixture tracks no session state to
// reject against.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.obsMu.Lock()
	s.lastDeletedSession = r.Header.Get(SessionHeader)
	s.deleteCount++
	s.obsMu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) removeStream(target *activeStream) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	kept := s.streams[:0]
	for _, st := range s.streams {
		if st != target {
			kept = append(kept, st)
		}
	}
	s.streams = kept
}

// postedResponseID reports the id of a JSON-RPC response body (one with no
// "method" member and either "result" or "error"), distinguishing it from
// an ordinary request or notification this fixture otherwise dispatches.
func postedResponseID(body []byte) (int, bool) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(body, &generic); err != nil {
		return 0, false
	}
	if _, hasMethod := generic["method"]; hasMethod {
		return 0, false
	}
	idRaw, hasID := generic["id"]
	if !hasID {
		return 0, false
	}
	_, hasResult := generic["result"]
	_, hasError := generic["error"]
	if !hasResult && !hasError {
		return 0, false
	}
	var id int
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return 0, false
	}
	return id, true
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	s.obsMu.Lock()
	s.postCount++
	s.obsMu.Unlock()
	cfg := s.snapshot()

	if cfg.RedirectPostTo != "" {
		http.Redirect(w, r, cfg.RedirectPostTo, http.StatusTemporaryRedirect)
		return
	}
	if cfg.LegacyOnly {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cfg.HangForever {
		// Bounded, not truly infinite: if the client's cancellation never
		// closes the underlying connection (a Transport implementation
		// detail this fixture must not depend on), the handler still
		// returns on its own so the server itself can shut down cleanly.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		return
	}
	if cfg.RequireProtocolVersion != "" && r.Header.Get(ProtocolVersionHeader) != cfg.RequireProtocolVersion {
		http.Error(w, fmt.Sprintf("unexpected protocol version %q", r.Header.Get(ProtocolVersionHeader)), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if id, ok := postedResponseID(body); ok {
		// A response the client is POSTing to a server-initiated request
		// this fixture pushed on the GET stream (R1-13): no "method"
		// member, only "id" plus "result" or "error". Accepted, no body,
		// and recorded for the test to inspect.
		s.obsMu.Lock()
		s.postedResponses[id] = append(json.RawMessage(nil), body...)
		s.obsMu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if cfg.FailStatus != 0 {
		http.Error(w, cfg.FailMessage, cfg.FailStatus)
		return
	}

	if challenge := challengeFor(cfg, r); challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if req.Method != "initialize" {
		s.obsMu.Lock()
		s.lastSessionHeader = r.Header.Get(SessionHeader)
		s.lastNonInitSeen = true
		s.obsMu.Unlock()
	}

	if req.Method != "initialize" && cfg.RejectSessionAfterInit && r.Header.Get(SessionHeader) != "" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if cfg.UnsupportedContentType {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not a supported mcp response")
		return
	}

	if req.ID == nil {
		// A notification carries no id: accepted, no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := dispatch(cfg, req)

	resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	respBody, err := json.Marshal(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if cfg.OversizedResponseBytes > 0 {
		respBody = padJSON(respBody, cfg.OversizedResponseBytes)
	}

	if cfg.AssignSession && req.Method == "initialize" {
		w.Header().Set(SessionHeader, sessionToken)
	}

	if cfg.MalformedSSEFrame {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message\ndata: {not valid json\n\n")
		return
	}
	if cfg.TruncateSSEStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// A frame for an unrelated id, then the connection ends before the
		// awaited response ever arrives.
		writeSSEFrame(w, []byte(`{"jsonrpc":"2.0","id":999999,"result":{}}`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	if cfg.ResponseSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSEFrame(w, respBody)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if cfg.HoldPostStreamOpen {
			// Bounded, not truly infinite, matching HangForever's own
			// rationale: the handler must still return on its own so the
			// server can shut down cleanly if the client's cancellation
			// never closes the underlying connection promptly.
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody)
}

// challengeFor reports the WWW-Authenticate header value to answer with,
// or "" when the request is authorized (or no authorization is required).
func challengeFor(cfg Config, r *http.Request) string {
	needsToken := cfg.RequireBearerToken != ""
	authorized := needsToken && r.Header.Get("Authorization") == "Bearer "+cfg.RequireBearerToken
	if authorized {
		return ""
	}
	if !cfg.ChallengeAlways && !needsToken {
		return ""
	}
	meta := cfg.ChallengeResourceMetaURL
	if meta == "" {
		return `Bearer realm="OAuth"`
	}
	return fmt.Sprintf(`Bearer realm="OAuth", resource_metadata=%q`, meta)
}

func dispatch(cfg Config, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]any{"name": "httptestserver", "version": "test"},
		}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []string{"text"},
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "description": "text to echo"},
					},
				},
			})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		return callTool(cfg, req.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func callTool(cfg Config, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	if cfg.UnknownToolName != "" && p.Name == cfg.UnknownToolName {
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
	}
	text, _ := p.Arguments["text"].(string)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	}, nil
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeSSEFrame(w http.ResponseWriter, data []byte) {
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
}

func padJSON(body []byte, minLen int) []byte {
	if len(body) >= minLen {
		return body
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return body
	}
	decoded["_pad"] = ""
	for {
		out, err := json.Marshal(decoded)
		if err != nil {
			return body
		}
		if len(out) >= minLen {
			return out
		}
		decoded["_pad"] = decoded["_pad"].(string) + strings.Repeat("x", minLen-len(out))
	}
}
