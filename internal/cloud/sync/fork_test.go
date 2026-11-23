package sync

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

var legacyID = regexp.MustCompile(`[0-9a-f]+-[0-9a-f]{20}`)

func TestForkTheirs_unchanged(t *testing.T) {
	const rel = "conversations/abc.json"
	for _, c := range []struct {
		name, raw, device string
		wantBytes         string
		wantTitle         string
		wantErr           string
	}{
		{
			"titled with device", `{"id":"abc","title":"Hello","created":"2026-01-02T03:04:05Z","messages":[{"role":"user","content":"hi"}]}`, "laptop",
			`{"created":"2026-01-02T03:04:05Z","id":"ID","messages":[{"role":"user","content":"hi"}],"source":"clai","source_id":"abc","title":"Hello (laptop)"}`, "Hello (laptop)", "",
		},
		{
			"titled no device", `{"id":"abc","title":"Hello","messages":[]}`, "",
			`{"id":"ID","messages":[],"source":"clai","source_id":"abc","title":"Hello"}`, "Hello", "",
		},
		{
			"untitled with device keeps unknown members", `{"id":"abc","messages":[{"role":"system","content":"s"},{"role":"user","content":"  first   question here "}],"extra":{"a":1}}`, "laptop",
			`{"extra":{"a":1},"id":"ID","messages":[{"role":"system","content":"s"},{"role":"user","content":"  first   question here "}],"source":"clai","source_id":"abc","title":"first question here (laptop)"}`, "first question here (laptop)", "",
		},
		{
			"untitled no device", `{"id":"abc","messages":[{"role":"user","content":"first question"}]}`, "",
			`{"id":"ID","messages":[{"role":"user","content":"first question"}],"source":"clai","source_id":"abc","title":"first question"}`, "first question", "",
		},
		{
			"no title no text", `{"id":"abc","messages":[]}`, "",
			`{"id":"ID","messages":[],"source":"clai","source_id":"abc","title":""}`, "", "",
		},
		{
			"no title only device", `{"id":"abc","messages":[]}`, "dev",
			`{"id":"ID","messages":[],"source":"clai","source_id":"abc","title":"(dev)"}`, "(dev)", "",
		},
		{"array", `[]`, "dev", "", "", "fork conversations/abc.json: json: cannot unmarshal array"},
		{"not json", `not json`, "", "", "", "fork conversations/abc.json: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, b, title, err := forkTheirs(rel, []byte(c.raw), c.device, testNow)
			if c.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), c.wantErr) || p != "" || b != nil || title != "" {
					t.Fatalf("got (%q, %q, %q, %v), want error %q and nothing else", p, b, title, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("forkTheirs: %v", err)
			}
			if got := legacyID.ReplaceAllString(p, "ID"); got != "conversations/ID.json" {
				t.Fatalf("path = %q", p)
			}
			if got := legacyID.ReplaceAllString(string(b), "ID"); got != c.wantBytes {
				t.Fatalf("bytes =\n%s\nwant\n%s", got, c.wantBytes)
			}
			if title != c.wantTitle {
				t.Fatalf("title = %q, want %q", title, c.wantTitle)
			}
		})
	}
}

func TestForkCopy(t *testing.T) {
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		raw   string
		stamp forkStamp
		want  map[string]any
		err   bool
	}{
		{
			"title and created kept", `{"id":"p","title":"T","created":"2026-01-01T00:00:00Z","messages":[],"x":1}`,
			forkStamp{},
			map[string]any{"id": "L", "source": "clai", "source_id": "p", "title": "T", "created": "2026-01-01T00:00:00Z", "x": float64(1)},
			false,
		},
		{
			"no title falls back to first user text", `{"id":"p","messages":[{"role":"user","content":"hello   there"}]}`,
			forkStamp{},
			map[string]any{"id": "L", "source": "clai", "source_id": "p", "title": "hello there"},
			false,
		},
		{
			"stamp replaces title and created", `{"id":"p","title":"T","created":"2026-01-01T00:00:00Z","messages":[]}`,
			forkStamp{Title: "N", Created: created},
			map[string]any{"id": "L", "source": "clai", "source_id": "p", "title": "N", "created": "2026-11-01T10:00:00Z"},
			false,
		},
		{"array is an error", `[]`, forkStamp{}, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, b, _, err := forkCopy("conversations/p.json", []byte(c.raw), "L", c.stamp)
			if c.err {
				if err == nil || p != "" || b != nil {
					t.Fatalf("got (%q, %q, %v), want an error and no bytes", p, b, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p != "conversations/L.json" {
				t.Fatalf("path = %q", p)
			}
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}
