package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Chat struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
	// Source is a stable, human-readable origin label.
	// Empty ("") for native clai chats.
	// Examples: "claude-code", "codex", "cursor", "clai" (forked from another clai chat).
	Source string `json:"source,omitempty"`
	// SourceID is the originating tool's conversation identifier, or the
	// parent chat ID when Source == "clai" (fork).
	SourceID string `json:"source_id,omitempty"`
	Profile  string `json:"profile,omitempty"`
	// OriginDir is the canonical working directory the chat was first persisted
	// from. It is stamped once on first persist and never rewritten, enabling
	// directory-anchored conversation search. Empty for conversations saved
	// before origin stamping existed (forward-only).
	OriginDir string `json:"origin_dir,omitempty"`
	// GroupKey is the hex-encoded SHA-256 of the first user message's canonical
	// text content; a Parts message hashes its parts' Describe(), so an image-only
	// one has a key. Empty when no user message exists or the chat predates this
	// feature. Stamped once on first persist; never rewritten.
	GroupKey string `json:"group_key,omitempty"`
	// Title and Summary are model-generated labels; SummaryAt is provenance
	// only (when they were generated), never a staleness index.
	Title     string    `json:"title,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	SummaryAt time.Time `json:"summary_at,omitzero"`
	Messages  []Message `json:"messages"`
	// TokenUsage is the billable usage accumulated across all model calls in
	// the latest session. RecentTokenUsage is only the final model call, which
	// is the closest persisted gauge of the next request's context size.
	TokenUsage       *Usage      `json:"usage,omitempty"`
	RecentTokenUsage *Usage      `json:"recent_usage,omitempty"`
	Queries          []QueryCost `json:"queries,omitempty"`
}

type QueryCost struct {
	CreatedAt time.Time `json:"created_at"`
	CostUSD   float64   `json:"cost_usd"`
	// MessageTrigger points at the most recent user message at the time
	// of the query cost
	MessageTrigger int    `json:"current_index"`
	Model          string `json:"model,omitempty"`
	Usage          Usage  `json:"usage"`
	// Purpose is empty for the conversation's own model calls and "summary"
	// for rows a summarizer contributed; index model resolution skips the
	// latter (worklog 2026-09-09-conversation-summaries, D19).
	Purpose string `json:"purpose,omitempty"`
}

func (c Chat) TotalTokens() string {
	if c.TokenUsage == nil {
		return "N/A"
	}
	return fmt.Sprintf("%d", c.TokenUsage.TotalTokens)
}

func (c Chat) TotalCostUSD() float64 {
	total := 0.0
	for _, q := range c.Queries {
		total += q.CostUSD
	}
	return total
}

func (c Chat) HasCostEstimates() bool {
	return len(c.Queries) > 0
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	PromptTokensDetails     PromptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details"`
}

type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens"`
}

type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens"`
	AudioTokens              int `json:"audio_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

// ReasoningItem is an opaque reasoning output item from the OpenAI Responses API.
// EncryptedContent is sealed by OpenAI: it cannot be read locally and is only
// meaningful when replayed to the OpenAI Responses API for a reasoning model. It is
// never sent to other vendors. Persisted out-of-band in a per-chat sidecar so the
// conversation JSON stays human-readable.
type ReasoningItem struct {
	ID               string   `json:"id"`
	EncryptedContent string   `json:"encrypted_content"`
	Summary          []string `json:"summary,omitempty"`
}

type Message struct {
	Role             string `json:"role"`
	ToolCalls        []Call `json:"tool_calls,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ReasoningItems carry opaque reasoning continuity for a tool-bearing assistant
	// turn. They are stored out-of-band and keyed by the first persisted tool-call
	// ID, so the conversation JSON stays human-readable and portable.
	ReasoningItems []ReasoningItem `json:"-"`
	Content        string          `json:"-"`
	Parts          []Part          `json:"-"`
}

// Describe returns the message as text: Content when Parts is empty, otherwise
// the Describe of every part joined by "\n".
func (m Message) Describe() string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	out := make([]string, 0, len(m.Parts))
	for _, p := range m.Parts {
		if p == nil {
			continue
		}
		out = append(out, p.Describe())
	}
	return strings.Join(out, "\n")
}

// MarshalJSON
//  1. A message with empty Parts writes "content" as the Content string,
//     byte-identical to the pre-worklog encoding (I10).
//  2. Non-empty Parts write "content" as an array, in order, of
//     {"type":"text","text":<Text>} and
//     {"type":"image","asset":<Asset>,"mime":<MIME>,"origin":<Origin>}.
//  3. A pending ImagePart (Data set, Asset empty) fails with ErrPendingImage, so no
//     image bytes reach disk (C1, I5).
func (m Message) MarshalJSON() ([]byte, error) {
	obj := map[string]any{
		"role": m.Role,
	}
	if len(m.ToolCalls) > 0 {
		patched := make([]Call, len(m.ToolCalls))
		copy(patched, m.ToolCalls)
		for i := range patched {
			patched[i].Patch()
		}
		obj["tool_calls"] = patched
	}
	if m.ToolCallID != "" {
		obj["tool_call_id"] = m.ToolCallID
	}
	if m.ReasoningContent != "" {
		obj["reasoning_content"] = m.ReasoningContent
	}
	if len(m.Parts) == 0 {
		obj["content"] = m.Content
		return json.Marshal(obj)
	}
	parts, err := encodeParts(m.Parts)
	if err != nil {
		return nil, err
	}
	obj["content"] = parts
	return json.Marshal(obj)
}

// UnmarshalJSON
//  1. A string "content" sets Content and leaves Parts nil.
//  2. An array of "text" and "image" elements sets Parts: a "text" element becomes a
//     TextPart, and an "image" element becomes a stored ImagePart.
//  3. A legacy element {"type":"image_url","image_url":{"url":"data:<m>;base64,<b>"}}
//     becomes a pending ImagePart{Origin: user, MIME: <m>, Data: decoded <b>} at its
//     position (C2, I5). A url that is not a base64 data URL, or base64 that does not
//     decode, fails with ErrLegacyImageURL, wrapped with the element index.
//  4. An element of any other type fails with ErrUnknownPartType, naming the type.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["role"]; ok {
		if err := json.Unmarshal(v, &m.Role); err != nil {
			return err
		}
	}
	if v, ok := raw["tool_calls"]; ok {
		if err := json.Unmarshal(v, &m.ToolCalls); err != nil {
			return err
		}
	}
	if v, ok := raw["tool_call_id"]; ok {
		if err := json.Unmarshal(v, &m.ToolCallID); err != nil {
			return err
		}
	}
	if v, ok := raw["reasoning_content"]; ok {
		if err := json.Unmarshal(v, &m.ReasoningContent); err != nil {
			return err
		}
	}
	if v, ok := raw["content"]; ok {
		t := bytes.TrimSpace(v)
		if len(t) > 0 && t[0] == '[' {
			var arr []partJSON
			if err := json.Unmarshal(v, &arr); err != nil {
				return err
			}
			parts, err := decodeParts(arr)
			if err != nil {
				return err
			}
			m.Parts = parts
			m.Content = ""
		} else {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			m.Content = s
			m.Parts = nil
		}
	}
	return nil
}

func (m *Message) String() string {
	if m.Content != "" {
		return m.Content
	}
	for _, p := range m.Parts {
		if tp, ok := p.(TextPart); ok && tp.Text != "" {
			return tp.Text
		}
	}
	return ""
}

// FirstSystemMessage returns the first encountered Message with role 'system'
func (c *Chat) FirstSystemMessage() (Message, error) {
	for _, msg := range c.Messages {
		if msg.Role == "system" {
			return msg, nil
		}
	}
	return Message{}, errors.New("failed to find any system message")
}

func (c *Chat) FirstUserMessage() (Message, error) {
	for _, msg := range c.Messages {
		if msg.Role == "user" {
			return msg, nil
		}
	}
	return Message{}, errors.New("failed to find any user message")
}

// LastOfRole returns the last Message with the given role,
// along with its index position in the Messages slice.
// Returns an error if no message with that role is found.
func (c *Chat) LastOfRole(role string) (Message, int, error) {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		msg := c.Messages[i]
		if msg.Role == role {
			return msg, i, nil
		}
	}
	return Message{}, -1, fmt.Errorf("failed to find any %v message", role)
}
