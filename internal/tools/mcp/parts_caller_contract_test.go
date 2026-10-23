package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/toolstest"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	pub_tools "github.com/baalimago/clai/pkg/tools"
)

func contractPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 3, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func mcpImage(data []byte, mime string) map[string]any {
	return map[string]any{"data": base64.StdEncoding.EncodeToString(data), "mimeType": mime}
}

func echoTool(t *testing.T) *mcpTool {
	t.Helper()
	return &mcpTool{remoteName: "echo", connector: resolvedConnector{startTestServerConn(t)}}
}

type fakePartsCaller struct {
	parts []pub_models.Part
	err   error
}

func (f fakePartsCaller) CallParts(_ context.Context, in pub_models.Input) ([]pub_models.Part, error) {
	if in["fail"] == true {
		return nil, f.err
	}
	return f.parts, nil
}

func (f fakePartsCaller) Call(in pub_models.Input) (string, error) {
	parts, err := f.CallParts(context.Background(), in)
	if err != nil {
		return "", err
	}
	return pub_models.Message{Parts: parts}.Describe(), nil
}

func (fakePartsCaller) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "fake_parts"}
}

func mcpSubject(t *testing.T) toolstest.PartsSubject {
	data := contractPNG(t)
	return toolstest.PartsSubject{
		Caller: echoTool(t),
		ImageIn: pub_models.Input{
			"text":   "look",
			"images": []any{mcpImage(data, "image/png")},
		},
		WantParts: []pub_models.Part{
			pub_models.TextPart{Text: "look"},
			pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: data},
		},
		FailIn: pub_models.Input{"text": "error"},
	}
}

func viewImageSubject(t *testing.T) toolstest.PartsSubject {
	data := contractPNG(t)
	path := filepath.Join(t.TempDir(), "cat.png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	img := pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: data}
	handle, err := img.Handle()
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return toolstest.PartsSubject{
		Caller:    pub_tools.ViewImage,
		ImageIn:   pub_models.Input{"path": path},
		WantParts: []pub_models.Part{pub_models.TextPart{Text: "image " + handle + " from " + path}, img},
		FailIn:    pub_models.Input{"path": filepath.Join(t.TempDir(), "absent.png")},
		FailErr:   os.ErrNotExist,
	}
}

func fakeSubject(*testing.T) toolstest.PartsSubject {
	parts := []pub_models.Part{
		pub_models.TextPart{Text: "before"},
		pub_models.ImagePart{
			Asset:  "4bf5122f344554c53bde2ebb8cd2b7e3d1600ad631c385a5d7cce23c7785459a.png",
			MIME:   "image/png",
			Origin: pub_models.OriginTool,
		},
		pub_models.TextPart{Text: "after"},
	}
	sentinel := errors.New("fake tool failed")
	return toolstest.PartsSubject{
		Caller:    fakePartsCaller{parts: parts, err: sentinel},
		ImageIn:   pub_models.Input{},
		WantParts: parts,
		FailIn:    pub_models.Input{"fail": true},
		FailErr:   sentinel,
	}
}

// AC1: one suite over every PartsCaller, each case naming the clause it proves.
func TestPartsCallerContract(t *testing.T) {
	subjects := []struct {
		name string
		new  func(t *testing.T) toolstest.PartsSubject
	}{
		{"mcp", mcpSubject},
		{"view_image", viewImageSubject},
		{"fake", fakeSubject},
		{"generate_image", generateImageSubject},
	}
	for _, subject := range subjects {
		t.Run(subject.name, func(t *testing.T) {
			toolstest.RunPartsCallerContract(t, subject.new(t))
		})
	}
}

// CallParts.M1, AC2
func TestMcpTool_ImageContent(t *testing.T) {
	data := contractPNG(t)
	parts, err := echoTool(t).CallParts(t.Context(), pub_models.Input{
		"text":   "a screenshot",
		"images": []any{mcpImage(data, "image/png")},
	})
	if err != nil {
		t.Fatalf("CallParts: %v", err)
	}
	want := []pub_models.Part{
		pub_models.TextPart{Text: "a screenshot"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: data},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
}

// CallParts.M2
func TestMcpTool_BadImage(t *testing.T) {
	data := contractPNG(t)
	tool := echoTool(t)
	in := pub_models.Input{
		"text": "three bad images",
		"images": []any{
			map[string]any{"data": "%%% not base64 %%%", "mimeType": "image/png"},
			mcpImage([]byte("just some text bytes"), "image/png"),
			mcpImage(data, "image/jpeg"),
		},
	}
	parts, err := tool.CallParts(t.Context(), in)
	if err != nil {
		t.Fatalf("CallParts: %v, want the call to succeed", err)
	}
	if len(parts) != 4 || parts[0] != (pub_models.TextPart{Text: "three bad images"}) {
		t.Fatalf("parts = %#v, want the text then three notes", parts)
	}
	reasons := []string{"base64", "not a supported image", "image/jpeg is image/png"}
	for i, reason := range reasons {
		note, ok := parts[i+1].(pub_models.TextPart)
		if !ok || !strings.HasPrefix(note.Text, "[image dropped: ") || !strings.HasSuffix(note.Text, "]") ||
			!strings.Contains(note.Text, reason) {
			t.Fatalf("parts[%d] = %#v, want an [image dropped: ...] note naming %q", i+1, parts[i+1], reason)
		}
	}
	if _, err := tool.Call(in); err != nil {
		t.Fatalf("Call: %v, want the call to succeed", err)
	}
}

// CallParts.M3
func TestMcpTool_IsErrorUnchanged(t *testing.T) {
	tool := echoTool(t)
	in := pub_models.Input{"text": "error", "images": []any{mcpImage(contractPNG(t), "image/png")}}
	const want = `mcp tool "echo" returned an error result: error`

	parts, err := tool.CallParts(t.Context(), in)
	if err == nil || err.Error() != want || parts != nil {
		t.Fatalf("CallParts = %#v, %v; want nil, %q", parts, err, want)
	}
	out, err := tool.CallWithContext(t.Context(), in)
	if err == nil || err.Error() != want || out != "" {
		t.Fatalf("CallWithContext = %q, %v; want \"\", %q", out, err, want)
	}
}

// CallParts.M1, I10: a run of text items is one TextPart, so a text-only
// result reads as the pre-worklog concatenation.
func TestMcpTool_TextRunsMerged(t *testing.T) {
	data := contractPNG(t)
	encoded := base64.StdEncoding.EncodeToString(data)
	img := pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: data}
	handle, err := img.Handle()
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for _, tc := range []struct {
		name    string
		content string
		parts   []pub_models.Part
		call    string
	}{
		{"two text items", `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, []pub_models.Part{pub_models.TextPart{Text: "ab"}}, "ab"},
		{"an ignored item between", `[{"type":"text","text":"a"},{"type":"resource","resource":{}},{"type":"text","text":"b"}]`, []pub_models.Part{pub_models.TextPart{Text: "ab"}}, "ab"},
		{
			"runs around an image", `[{"type":"text","text":"a"},{"type":"text","text":"b"},{"type":"image","data":"` + encoded + `","mimeType":"image/png"},{"type":"text","text":"c"},{"type":"text","text":"d"}]`,
			[]pub_models.Part{pub_models.TextPart{Text: "ab"}, img, pub_models.TextPart{Text: "cd"}},
			"ab\n[image " + handle + "]\ncd",
		},
		{"no content", `[]`, []pub_models.Part{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &mcpTool{remoteName: "multi", connector: resolvedConnector{&fakeConn{raw: json.RawMessage(`{"content":` + tc.content + `}`)}}}
			parts, err := tool.CallParts(t.Context(), pub_models.Input{})
			if err != nil {
				t.Fatalf("CallParts: %v", err)
			}
			if !reflect.DeepEqual(parts, tc.parts) {
				t.Fatalf("parts = %#v, want %#v", parts, tc.parts)
			}
			got, err := tool.Call(pub_models.Input{})
			if err != nil || got != tc.call {
				t.Fatalf("Call = %q, %v; want %q", got, err, tc.call)
			}
		})
	}
}
