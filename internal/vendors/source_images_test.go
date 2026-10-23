package vendors_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
	"github.com/baalimago/clai/internal/vendors/pi"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type jsonLine = map[string]any

// imageDialect writes one source's session lines, so the contract suite runs
// unchanged over every reader that imports images.
type imageDialect struct {
	name       string
	shape      jsonltest.Shape
	userMIME   string
	reader     func(root string) vendors.SourceReader
	header     func(sid string) []jsonLine
	user       func(sid string, blocks ...any) jsonLine
	toolCall   func(sid, id string) jsonLine
	toolResult func(sid, id string, blocks ...any) jsonLine
	image      func(mime, b64 string) any
	urlImage   func(url string) any
}

func imageDialects() []imageDialect {
	return []imageDialect{claudeDialect(), piDialect()}
}

func claudeDialect() imageDialect {
	line := func(typ, sid string, content any) jsonLine {
		return jsonLine{
			"type": typ, "sessionId": sid, "cwd": "/work", "timestamp": "2026-01-01T00:00:00Z",
			"message": jsonLine{"role": typ, "content": content},
		}
	}
	return imageDialect{
		name:     "claude-code",
		shape:    jsonltest.ShapeClaude,
		userMIME: "image/png",
		reader:   func(root string) vendors.SourceReader { return anthropic.SourceReader{Root: root} },
		header:   func(string) []jsonLine { return nil },
		user:     func(sid string, blocks ...any) jsonLine { return line("user", sid, blocks) },
		toolCall: func(sid, id string) jsonLine {
			return line("assistant", sid, []any{jsonLine{"type": "tool_use", "id": id, "name": "screenshot", "input": jsonLine{}}})
		},
		toolResult: func(sid, id string, blocks ...any) jsonLine {
			return line("user", sid, []any{jsonLine{"type": "tool_result", "tool_use_id": id, "content": blocks}})
		},
		image: func(mime, b64 string) any {
			return jsonLine{"type": "image", "source": jsonLine{"type": "base64", "media_type": mime, "data": b64}}
		},
		urlImage: func(url string) any {
			return jsonLine{"type": "image", "source": jsonLine{"type": "url", "url": url}}
		},
	}
}

func piDialect() imageDialect {
	message := func(role string, content any) jsonLine {
		return jsonLine{"type": "message", "message": jsonLine{"role": role, "content": content}}
	}
	return imageDialect{
		name:     "pi",
		shape:    jsonltest.ShapePi,
		userMIME: "image/jpeg",
		reader:   func(root string) vendors.SourceReader { return pi.SourceReader{Root: root} },
		header: func(sid string) []jsonLine {
			return []jsonLine{{"type": "session", "id": sid, "cwd": "/work", "timestamp": "2026-01-01T00:00:00Z"}}
		},
		user: func(_ string, blocks ...any) jsonLine { return message("user", blocks) },
		toolCall: func(_, id string) jsonLine {
			return message("assistant", []any{jsonLine{"type": "toolCall", "id": id, "name": "screenshot", "arguments": jsonLine{}}})
		},
		toolResult: func(_, id string, blocks ...any) jsonLine {
			line := message("toolResult", blocks)
			line["message"].(jsonLine)["toolCallId"] = id
			return line
		},
		image: func(mime, b64 string) any { return jsonLine{"type": "image", "data": b64, "mimeType": mime} },
		urlImage: func(url string) any {
			return jsonLine{"type": "image", "url": url, "mimeType": "image/png"}
		},
	}
}

func textBlock(text string) any { return jsonLine{"type": "text", "text": text} }

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func encodedImage(t *testing.T, mime string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	var err error
	switch mime {
	case "image/png":
		err = png.Encode(&buf, img)
	case "image/jpeg":
		err = jpeg.Encode(&buf, img, nil)
	default:
		t.Fatalf("no encoder for %s", mime)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", mime, err)
	}
	return buf.Bytes()
}

func imageLabel(data []byte) string {
	sum := sha256.Sum256(data)
	return "[image " + hex.EncodeToString(sum[:])[:6] + "]"
}

func writeImageSession(t *testing.T, d imageDialect, root, sid string, lines ...jsonLine) string {
	t.Helper()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf bytes.Buffer
	for _, line := range append(d.header(sid), lines...) {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal fixture line: %v", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

func readImageChat(t *testing.T, d imageDialect, root, sid string) pub_models.Chat {
	t.Helper()
	chat, err := d.reader(root).Read(context.Background(), nil, sid)
	if err != nil {
		t.Fatalf("Read(%s): %v", sid, err)
	}
	return chat
}

func withRole(chat pub_models.Chat, role string) []pub_models.Message {
	out := []pub_models.Message{}
	for _, m := range chat.Messages {
		if m.Role == role {
			out = append(out, m)
		}
	}
	return out
}

func assertParts(t *testing.T, m pub_models.Message, want []pub_models.Part) {
	t.Helper()
	if m.Content != "" {
		t.Fatalf("Content = %q, want empty once the message carries Parts", m.Content)
	}
	if !reflect.DeepEqual(m.Parts, want) {
		t.Fatalf("Parts = %#v\nwant %#v", m.Parts, want)
	}
}

// TestReadImages is the contract suite for the image clauses of
// vendors.SourceReader.Read, run over every reader that imports images.
func TestReadImages(t *testing.T) {
	fixtures := map[string][]byte{
		"image/png":  encodedImage(t, "image/png"),
		"image/jpeg": encodedImage(t, "image/jpeg"),
	}
	pngData := fixtures["image/png"]
	for _, d := range imageDialects() {
		t.Run(d.name, func(t *testing.T) {
			// Read.1
			t.Run("UserImage", func(t *testing.T) {
				root := t.TempDir()
				data := fixtures[d.userMIME]
				writeImageSession(t, d, root, "s1", d.user("s1", textBlock("what is this?"), d.image(d.userMIME, b64(data))))
				users := withRole(readImageChat(t, d, root, "s1"), "user")
				if len(users) != 1 {
					t.Fatalf("got %d user messages, want 1", len(users))
				}
				assertParts(t, users[0], []pub_models.Part{
					pub_models.TextPart{Text: "what is this?"},
					pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: d.userMIME, Data: data},
				})
			})

			// Read.2
			t.Run("ToolResultImage", func(t *testing.T) {
				root := t.TempDir()
				writeImageSession(t, d, root, "s1",
					d.user("s1", textBlock("take a screenshot")),
					d.toolCall("s1", "call-1"),
					d.toolResult("s1", "call-1", textBlock("screenshot taken"), d.image("image/png", b64(pngData))),
				)
				tools := withRole(readImageChat(t, d, root, "s1"), "tool")
				if len(tools) != 1 {
					t.Fatalf("got %d tool messages, want 1", len(tools))
				}
				if tools[0].ToolCallID != "call-1" {
					t.Fatalf("ToolCallID = %q, want call-1", tools[0].ToolCallID)
				}
				assertParts(t, tools[0], []pub_models.Part{
					pub_models.TextPart{Text: "screenshot taken"},
					pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: pngData},
				})
			})

			// Read.3
			t.Run("ImageOnlyKept", func(t *testing.T) {
				root := t.TempDir()
				writeImageSession(t, d, root, "s1",
					d.user("s1", d.image("image/png", b64(pngData))),
					d.user("s1", textBlock("and this one?")),
				)
				users := withRole(readImageChat(t, d, root, "s1"), "user")
				if len(users) != 2 {
					t.Fatalf("got %d user messages, want the image-only one kept before the text one", len(users))
				}
				assertParts(t, users[0], []pub_models.Part{
					pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: pngData},
				})
				if users[1].Content != "and this one?" || users[1].Parts != nil {
					t.Fatalf("second user message = %+v, want the text-only mapping", users[1])
				}
			})

			// Read.4
			t.Run("NotImported", func(t *testing.T) {
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					hits.Add(1)
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(pngData)
				}))
				defer srv.Close()
				root := t.TempDir()
				writeImageSession(t, d, root, "s1", d.user("s1",
					d.urlImage(srv.URL+"/cat.png"),
					textBlock("between"),
					d.image("image/png", "%%% not base64 %%%"),
					d.image("image/png", b64([]byte("plain text, not an image"))),
					d.image("image/jpeg", b64(pngData)),
				))
				users := withRole(readImageChat(t, d, root, "s1"), "user")
				if len(users) != 1 {
					t.Fatalf("got %d user messages, want 1", len(users))
				}
				assertParts(t, users[0], []pub_models.Part{
					pub_models.TextPart{Text: "[image not imported: url source]"},
					pub_models.TextPart{Text: "between"},
					pub_models.TextPart{Text: "[image not imported: bad base64]"},
					pub_models.TextPart{Text: "[image not imported: not an image]"},
					pub_models.TextPart{Text: "[image not imported: mime mismatch]"},
				})
				if n := hits.Load(); n != 0 {
					t.Fatalf("Read fetched the url source %d times, want never", n)
				}
			})

			// Read.5, AC4
			t.Run("TextUnchanged", func(t *testing.T) {
				root := t.TempDir()
				corpus := jsonltest.WriteCorpus(t, root, corpusOptions(d.shape))
				reader := d.reader(root)
				got := []textReadResult{}
				for _, fact := range corpus.Sessions {
					if fact.SessionID == "" {
						continue
					}
					res := textReadResult{SourceID: fact.SessionID}
					chat, err := reader.Read(context.Background(), nil, fact.SessionID)
					if err != nil {
						res.Error = err.Error()
					} else {
						res.Chat = &chat
					}
					got = append(got, res)
				}
				b, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				goldenPath := filepath.Join("testdata", "text_read_"+d.name+".golden.json")
				want, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("read golden: %v", err)
				}
				if out := append(b, '\n'); !bytes.Equal(out, want) {
					t.Fatalf("text-only Read output differs from %s, captured before image import:\n%s", goldenPath, firstDiff(string(out), string(want)))
				}
			})
		})
	}
}

// textReadResult is one session of the TextUnchanged golden, which was
// captured from the readers as they stood before image import.
type textReadResult struct {
	SourceID string           `json:"source_id"`
	Chat     *pub_models.Chat `json:"chat,omitempty"`
	Error    string           `json:"error,omitempty"`
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range min(len(g), len(w)) {
		if g[i] != w[i] {
			return "line " + strconv.Itoa(i+1) + ":\n got  " + g[i] + "\n want " + w[i]
		}
	}
	return "line counts differ: got " + strconv.Itoa(len(g)) + ", want " + strconv.Itoa(len(w))
}

// TestReadImages_NoStoreWrites proves Read.6: the readers hold no asset
// store, so discovering and reading sessions full of images writes nothing.
func TestReadImages_NoStoreWrites(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	pngData := encodedImage(t, "image/png")
	for _, d := range imageDialects() {
		root := t.TempDir()
		writeImageSession(t, d, root, "s1",
			d.user("s1", textBlock("look"), d.image("image/png", b64(pngData))),
			d.toolCall("s1", "call-1"),
			d.toolResult("s1", "call-1", d.image("image/png", b64(pngData))),
		)
		reader := d.reader(root)
		if _, err := reader.Discover(context.Background(), nil); err != nil {
			t.Fatalf("%s Discover: %v", d.name, err)
		}
		chat, err := reader.Read(context.Background(), nil, "s1")
		if err != nil {
			t.Fatalf("%s Read: %v", d.name, err)
		}
		pending := 0
		for _, m := range chat.Messages {
			for _, p := range m.Parts {
				if img, ok := p.(pub_models.ImagePart); ok && img.Asset == "" && len(img.Data) > 0 {
					pending++
				}
			}
		}
		if pending != 2 {
			t.Fatalf("%s: %d pending images, want 2; the fixture did not reach the reader", d.name, pending)
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, "conversations", "assets")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("conversations/assets exists after reading (stat err %v), want it never created", err)
	}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("config dir holds %d entries after reading, want none", len(entries))
	}
}

func rowsByID(rows []vendors.SourceRow) map[string]vendors.SourceRow {
	out := map[string]vendors.SourceRow{}
	for _, r := range rows {
		out[r.SourceID] = r
	}
	return out
}

// TestDiscover_ImageOnlyFirstMessage proves Discover.1, and pins that only an
// image-only message previews as its placeholder: text still wins, and a tool
// result's image never previews.
func TestDiscover_ImageOnlyFirstMessage(t *testing.T) {
	pngData := encodedImage(t, "image/png")
	for _, d := range imageDialects() {
		t.Run(d.name, func(t *testing.T) {
			root := t.TempDir()
			writeImageSession(t, d, root, "image-only",
				d.user("image-only", d.image("image/png", b64(pngData))),
				d.user("image-only", textBlock("second message")),
			)
			writeImageSession(t, d, root, "mixed", d.user("mixed", textBlock("look"), d.image("image/png", b64(pngData))))
			writeImageSession(t, d, root, "tool-image",
				d.toolCall("tool-image", "call-1"),
				d.toolResult("tool-image", "call-1", d.image("image/png", b64(pngData))),
				d.user("tool-image", textBlock("typed later")),
			)
			rows, err := d.reader(root).Discover(context.Background(), nil)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			byID := rowsByID(rows)
			want := map[string]string{"image-only": imageLabel(pngData), "mixed": "look", "tool-image": "typed later"}
			for id, preview := range want {
				row, ok := byID[id]
				if !ok {
					t.Fatalf("no row for %s in %+v", id, rows)
				}
				if row.FirstUserMessage != preview || row.FullFirstUserMessage != preview {
					t.Fatalf("%s: preview %q, full %q; want both %q", id, row.FirstUserMessage, row.FullFirstUserMessage, preview)
				}
			}
		})
	}
}

func (c *memCache) storedRow(absPath string) vendors.SourceRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rows[absPath].row
}

func (c *memCache) storeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stores
}

// TestDiscover_EmptyPreviewReread proves Discover.2: a cached row with an
// empty preview and messages is re-read and stored again, after which the
// healed row is served from the cache.
func TestDiscover_EmptyPreviewReread(t *testing.T) {
	pngData := encodedImage(t, "image/png")
	for _, d := range imageDialects() {
		t.Run(d.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeImageSession(t, d, root, "s1",
				d.user("s1", d.image("image/png", b64(pngData))),
				d.user("s1", textBlock("second message")),
			)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			cache := newMemCache()
			cache.Store(path, info, vendors.SourceRow{Source: d.name, SourceID: "s1", MessageCount: 3, RawPath: path})
			before := cache.storeCount()

			rows, err := d.reader(root).Discover(context.Background(), cache)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := imageLabel(pngData)
			if row := onlyRow(t, rows); row.FirstUserMessage != want {
				t.Fatalf("FirstUserMessage = %q, want %q; the stale row was served", row.FirstUserMessage, want)
			}
			if n := cache.storeCount() - before; n != 1 {
				t.Fatalf("Store called %d times, want once with the fresh row", n)
			}
			if got := cache.storedRow(path).FirstUserMessage; got != want {
				t.Fatalf("cached preview = %q, want %q", got, want)
			}

			if _, err := d.reader(root).Discover(context.Background(), cache); err != nil {
				t.Fatalf("second Discover: %v", err)
			}
			if n := cache.storeCount() - before; n != 1 {
				t.Fatalf("the healed row was re-read: Store called %d times in all, want 1", n)
			}
		})
	}
}

// A user message that carries only Parts is content: a tool-call-only
// assistant after it must not merge into a batch before it.
func TestNormalizeToolCallSequence_partsUserMessageIsContent(t *testing.T) {
	img := pub_models.ImagePart{Origin: pub_models.OriginUser, MIME: "image/png", Data: encodedImage(t, "image/png")}
	msgs := []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "a"}}},
		{Role: "user", Parts: []pub_models.Part{img}},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "b"}}},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 3 || got[1].Role != "user" || len(got[0].ToolCalls) != 1 {
		t.Fatalf("got %+v, want the three messages in order", got)
	}
}
