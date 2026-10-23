package models

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func pngBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decode fixture png: %v", err)
	}
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestParts_RoundTrip(t *testing.T) {
	asset := sha256Hex(pngBytes(t)) + ".png"
	msg := Message{
		Role: "assistant",
		Parts: []Part{
			TextPart{Text: "here it is"},
			ImagePart{Asset: asset, MIME: "image/png", Origin: OriginGenerated},
		},
	}

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"content":[{"type":"text","text":"here it is"},{"type":"image","asset":"` + asset +
		`","mime":"image/png","origin":"generated"}],"role":"assistant"}`
	if string(b) != want {
		t.Fatalf("encoding mismatch:\nwant: %s\n got: %s", want, b)
	}
	if strings.Contains(string(b), `"data"`) {
		t.Fatalf("encoding carries a data key: %s", b)
	}

	var got Message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("round trip mismatch:\nwant: %#v\n got: %#v", msg, got)
	}
}

func TestParts_TextOnlyUnchanged(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "simple user text",
			msg:  Message{Role: "user", Content: "hello world"},
			want: `{"content":"hello world","role":"user"}`,
		},
		{
			name: "assistant with reasoning and tool call",
			msg: Message{
				Role:             "assistant",
				Content:          "I will call a tool.",
				ReasoningContent: "Need current files.",
				ToolCalls:        []Call{{ID: "call-1", Name: "ls", Type: "function"}},
			},
			want: `{"content":"I will call a tool.","reasoning_content":"Need current files.","role":"assistant","tool_calls":[{"id":"call-1","name":"ls","type":"function","function":{"name":"ls","arguments":"{\"id\":\"call-1\",\"name\":\"ls\",\"type\":\"function\",\"function\":{\"name\":\"ls\"}}"}}]}`,
		},
		{
			name: "multi-line system content",
			msg:  Message{Role: "system", Content: "<shell context>\ncwd: /tmp/project\n\nhost: box\n</shell context>\nSYSTEM"},
			want: `{"content":"\u003cshell context\u003e\ncwd: /tmp/project\n\nhost: box\n\u003c/shell context\u003e\nSYSTEM","role":"system"}`,
		},
		{
			name: "tool result",
			msg:  Message{Role: "tool", ToolCallID: "call-1", Content: `a <b> & "c"`},
			want: `{"content":"a \u003cb\u003e \u0026 \"c\"","role":"tool","tool_call_id":"call-1"}`,
		},
		{
			name: "empty content",
			msg:  Message{Role: "user"},
			want: `{"content":"","role":"user"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("encoding changed:\nwant: %s\n got: %s", tc.want, b)
			}
			var got Message
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Parts != nil {
				t.Fatalf("Parts = %#v, want nil for string content", got.Parts)
			}
			if got.Content != tc.msg.Content {
				t.Fatalf("Content = %q, want %q", got.Content, tc.msg.Content)
			}
		})
	}
}

func TestParts_PendingRefused(t *testing.T) {
	msg := Message{Role: "user", Parts: []Part{
		TextPart{Text: "look"},
		ImagePart{MIME: "image/png", Origin: OriginUser, Data: pngBytes(t)},
	}}

	b, err := msg.MarshalJSON()
	if !errors.Is(err, ErrPendingImage) {
		t.Fatalf("MarshalJSON err = %v, want ErrPendingImage", err)
	}
	if len(b) != 0 {
		t.Fatalf("MarshalJSON wrote partial JSON: %s", b)
	}

	b, err = json.Marshal(Chat{ID: "c", Messages: []Message{msg}})
	if !errors.Is(err, ErrPendingImage) {
		t.Fatalf("json.Marshal(Chat) err = %v, want ErrPendingImage", err)
	}
	if len(b) != 0 {
		t.Fatalf("json.Marshal(Chat) wrote partial JSON: %s", b)
	}
}

func TestParts_LegacyDecodes(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-image-message.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal legacy message: %v", err)
	}

	want := []Part{
		ImagePart{Origin: OriginUser, MIME: "image/png", Data: pngBytes(t)},
		TextPart{Text: "what is in <IMG_0>?"},
	}
	if !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("Parts mismatch:\nwant: %#v\n got: %#v", want, got.Parts)
	}
	if got.Content != "" {
		t.Fatalf("legacy decode must fill Parts only, got Content %q", got.Content)
	}
	if strings.Contains(fmt.Sprintf("%#v", got), onePixelPNG) {
		t.Fatalf("decoded message still holds base64: %#v", got)
	}
}

func TestParts_LegacyBadURL(t *testing.T) {
	fetched := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched <- r.URL.Path
	}))
	defer srv.Close()

	tests := []struct {
		name string
		url  string
	}{
		{"remote url", srv.URL + "/cat.png"},
		{"corrupt base64", "data:image/png;base64,!!not-base64!!"},
		{"data url without base64 marker", "data:image/png," + onePixelPNG},
		{"data url without mime", "data:;base64," + onePixelPNG},
		{"empty image data", "data:image/png;base64,"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"` + tc.url + `"}}]}`
			var got Message
			err := json.Unmarshal([]byte(raw), &got)
			if !errors.Is(err, ErrLegacyImageURL) {
				t.Fatalf("err = %v, want ErrLegacyImageURL", err)
			}
			if !strings.Contains(err.Error(), "content part 1") {
				t.Fatalf("err %q does not name the element index", err)
			}
		})
	}

	t.Run("missing image_url object", func(t *testing.T) {
		var got Message
		err := json.Unmarshal([]byte(`{"content":[{"type":"image_url"}]}`), &got)
		if !errors.Is(err, ErrLegacyImageURL) {
			t.Fatalf("err = %v, want ErrLegacyImageURL", err)
		}
	})

	select {
	case path := <-fetched:
		t.Fatalf("decoder fetched %s", path)
	default:
	}
}

func TestParts_UnknownType(t *testing.T) {
	var got Message
	err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"audio"}]}`), &got)
	if !errors.Is(err, ErrUnknownPartType) {
		t.Fatalf("err = %v, want ErrUnknownPartType", err)
	}
	if !strings.Contains(err.Error(), "audio") {
		t.Fatalf("err %q does not name the type", err)
	}
	if got.Content != "" || got.Parts != nil {
		t.Fatalf("unknown part decoded as content: %#v", got)
	}

	for _, part := range []Part{&TextPart{Text: "x"}, nil} {
		_, err := json.Marshal(Message{Role: "user", Parts: []Part{part}})
		if !errors.Is(err, ErrUnknownPartType) {
			t.Fatalf("marshal %T: err = %v, want ErrUnknownPartType", part, err)
		}
	}
}

func TestParts_HandleStable(t *testing.T) {
	b := pngBytes(t)
	sum := sha256Hex(b)
	want := sum[:6]
	if HandleLength != len(want) {
		t.Fatalf("HandleLength = %d, want the README's %d", HandleLength, len(want))
	}

	pending, err := ImagePart{MIME: "image/png", Data: b}.Handle()
	if err != nil {
		t.Fatalf("pending Handle: %v", err)
	}
	stored, err := ImagePart{Asset: sum + ".png", MIME: "image/png"}.Handle()
	if err != nil {
		t.Fatalf("stored Handle: %v", err)
	}
	if pending != want || stored != want {
		t.Fatalf("handles pending %q stored %q, want both %q", pending, stored, want)
	}
}

func TestParts_HandleEmpty(t *testing.T) {
	empty := ImagePart{MIME: "image/png", Data: []byte{}}
	if _, err := empty.Handle(); !errors.Is(err, ErrImageEmpty) {
		t.Fatalf("Handle err = %v, want ErrImageEmpty", err)
	}
	if got := empty.Describe(); got != "[image ?]" {
		t.Fatalf("Describe = %q, want [image ?]", got)
	}

	short := ImagePart{Asset: "a.png"}
	if _, err := short.Handle(); err == nil {
		t.Fatal("Handle of an asset ref shorter than a handle must fail")
	}
	if got := short.Describe(); got != "[image ?]" {
		t.Fatalf("Describe = %q, want [image ?]", got)
	}

	if _, err := json.Marshal(Message{Parts: []Part{ImagePart{}}}); !errors.Is(err, ErrImageEmpty) {
		t.Fatalf("marshal err = %v, want ErrImageEmpty", err)
	}
	var got Message
	err := json.Unmarshal([]byte(`{"content":[{"type":"image","mime":"image/png"}]}`), &got)
	if !errors.Is(err, ErrImageEmpty) {
		t.Fatalf("unmarshal err = %v, want ErrImageEmpty", err)
	}
}

func TestParts_Describe(t *testing.T) {
	asset := sha256Hex([]byte("img")) + ".jpg"
	var _ Describer = Message{}
	msg := Message{Role: "user", Content: "ignored", Parts: []Part{
		TextPart{Text: "first\nline"},
		ImagePart{Asset: asset, MIME: "image/jpeg", Origin: OriginUser},
		TextPart{Text: " spaced "},
	}}
	want := "first\nline\n[image " + asset[:6] + "]\n spaced "
	if got := msg.Describe(); got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	if got := (TextPart{Text: " a\tb "}).Describe(); got != " a\tb " {
		t.Fatalf("TextPart.Describe = %q", got)
	}

	plain := Message{Role: "user", Content: "just text\n"}
	if got := plain.Describe(); got != "just text\n" {
		t.Fatalf("Content-only Describe = %q", got)
	}
}
