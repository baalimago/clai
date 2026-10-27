package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/asset"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// mcpTool wraps a tool provided by an MCP server and implements tools.LLMTool.
// It holds a Connector rather than a Conn directly, so the connection behind
// an eager server (already resolved at setup) and a lazy one (resolved on
// first use) are called the same way.
type mcpTool struct {
	remoteName string
	spec       pub_models.Specification
	connector  Connector
	// timeout bounds one tool call, excluding connection resolution; 0
	// disables the bound (caller ctx only).
	timeout time.Duration
	// serverName names the owning server, for ResolveForCall's callers: the
	// auth-pending signal and the actionable tool result both name the
	// server, not the remote tool.
	serverName string
	// authResolver drives the one action a tool-call site may take once
	// ResolveForCall reports an authorization challenge; nil for a
	// command-based server, where the only lever is time (phase 6).
	authResolver AuthResolver
	// authTimeout bounds a mid-run authorization wait for this server,
	// resolved once at registration time per the auth-timeout parameter
	// (D22); 0 means fail fast with no wait.
	authTimeout time.Duration
}

// resolvedConnector wraps a Conn that is already connected, so a tool
// registered for an eager server holds a Connector like any other, with no
// resolution work left to do.
type resolvedConnector struct{ conn Conn }

func (r resolvedConnector) Conn(context.Context) (Conn, error) { return r.conn, nil }

// NewTool builds an LLMTool for one MCP tool, wired to connector. It is the
// registration seam RegisterTools uses for both a live handshake's tools and
// a schema cache hit's cached tool list, which carries no live Conn and so
// supplies a Connector instead. serverName, authResolver and authTimeout are
// phase 6's addition: they let the tool-call site (internal/text/tool_executor.go)
// surface and bound a mid-run authorization wait before tools.InvokeWith
// folds the typed error into a string. authResolver may be nil.
func NewTool(connector Connector, remoteName string, spec pub_models.Specification, timeout time.Duration, serverName string, authResolver AuthResolver, authTimeout time.Duration) pub_models.LLMTool {
	return &mcpTool{
		remoteName:   remoteName,
		spec:         spec,
		connector:    connector,
		timeout:      timeout,
		serverName:   serverName,
		authResolver: authResolver,
		authTimeout:  authTimeout,
	}
}

// ResolveForCall drives the tool's Connector to a terminal outcome or an
// AuthChallengeError, exposing the typed error and this tool's AuthResolver
// before tools.InvokeWith would fold the error into a string. The tool-call
// site uses this to raise and bound a human wait outside connector
// resolution (D20): ctx governs only this attempt, exactly like Call.
func (m *mcpTool) ResolveForCall(ctx context.Context) (serverName string, resolver AuthResolver, authTimeout time.Duration, err error) {
	_, err = m.connector.Conn(ctx)
	return m.serverName, m.authResolver, m.authTimeout, err
}

var _ pub_models.PartsCaller = (*mcpTool)(nil)

// CallWithContext sends an MCP tool/call request with context-aware channel operations.
// If ctx is cancelled before the response arrives, the call is aborted.
func (m *mcpTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	parts, err := m.CallParts(ctx, input)
	if err != nil {
		return "", err
	}
	return pub_models.Message{Parts: parts}.Describe(), nil
}

// Call delegates to CallWithContext using context.Background for backwards compatibility.
func (m *mcpTool) Call(input pub_models.Input) (string, error) {
	return m.CallWithContext(context.Background(), input)
}

// CallParts maps runs of text items to TextParts and image items to pending ImageParts (CallParts.M1–M3).
func (m *mcpTool) CallParts(ctx context.Context, input pub_models.Input) ([]pub_models.Part, error) {
	// Resolution runs under the connector's own run context, excluded from
	// the per-call timeout: charging process birth against a call budget
	// would make a lazy first call look like a hung tool.
	conn, err := m.connector.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp tool %q: %w", m.remoteName, err)
	}

	if m.timeout > 0 {
		// Bound the call with the server's own timeout so a hung server fails
		// the call even when the caller's context has no deadline.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.timeout)
		defer cancel()
	}
	nonNullableInp := withoutUnsetOptional(input, m.spec.Inputs)

	raw, err := conn.Call(ctx, "tools/call", map[string]any{
		"name":      m.remoteName,
		"arguments": nonNullableInp,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp tool %q: %w", m.remoteName, err)
	}

	var result struct {
		Content []contentItem `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp tool %q: decode result: %w", m.remoteName, err)
	}
	if result.IsError {
		var text strings.Builder
		for _, c := range result.Content {
			if c.Type == "text" {
				text.WriteString(c.Text)
			}
		}
		return nil, fmt.Errorf("mcp tool %q returned an error result: %s", m.remoteName, text.String())
	}
	return contentParts(result.Content), nil
}

// contentParts merges each run of text items into one TextPart, so a text-only
// result reads as their concatenation, as it did before images were read.
func contentParts(items []contentItem) []pub_models.Part {
	parts := make([]pub_models.Part, 0, len(items))
	var run strings.Builder
	inRun := false
	endRun := func() {
		if inRun {
			parts = append(parts, pub_models.TextPart{Text: run.String()})
			run.Reset()
			inRun = false
		}
	}
	for _, c := range items {
		switch c.Type {
		case "text":
			run.WriteString(c.Text)
			inRun = true
		case "image":
			endRun()
			parts = append(parts, c.imagePart())
		}
	}
	endRun()
	return parts
}

type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (c contentItem) imagePart() pub_models.Part {
	data, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil {
		return droppedImage(fmt.Errorf("corrupt base64: %w", err))
	}
	sniffed, err := asset.Sniff(data)
	if err != nil {
		return droppedImage(err)
	}
	if sniffed != c.MimeType {
		return droppedImage(&asset.MIMEMismatchError{Declared: c.MimeType, Sniffed: sniffed})
	}
	return pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: sniffed, Data: data}
}

func droppedImage(reason error) pub_models.TextPart {
	return pub_models.TextPart{Text: "[image dropped: " + reason.Error() + "]"}
}

func (m *mcpTool) Specification() pub_models.Specification {
	return m.spec
}

// withoutUnsetOptional leaves out each optional argument the model sent as ""
// or null. Models fill every property of a schema, and a server that
// validates a string's length refuses "" for an argument the model meant to
// leave unset.
func withoutUnsetOptional(input pub_models.Input, schema *pub_models.InputSchema) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		if (v == nil || v == "") && !schemaRequires(schema, k) {
			continue
		}
		out[k] = v
	}
	return out
}

func schemaRequires(schema *pub_models.InputSchema, name string) bool {
	return schema != nil && slices.Contains(schema.Required, name)
}
