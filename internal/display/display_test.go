package display_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type displayerFactory struct {
	name string
	new  func(jsonl.Encoder) display.Displayer
}

func displayerFactories() []displayerFactory {
	return []displayerFactory{{name: "NewJSONDisplayer", new: display.NewJSONDisplayer}}
}

// recordingEncoder encodes through a real Encoder into buf and counts the
// records it was handed, so a test sees both the calls and the bytes.
type recordingEncoder struct {
	buf     bytes.Buffer
	enc     jsonl.Encoder
	records []jsonl.Record
}

func newRecordingEncoder() *recordingEncoder {
	r := &recordingEncoder{}
	r.enc = jsonl.NewEncoder(&r.buf)
	return r
}

func (r *recordingEncoder) Encode(rec jsonl.Record) error {
	r.records = append(r.records, rec)
	return r.enc.Encode(rec)
}

var errEncode = errors.New("encode failed")

type failingEncoder struct{ calls int }

func (f *failingEncoder) Encode(jsonl.Record) error {
	f.calls++
	return errEncode
}

type call struct {
	name string
	do   func(display.Displayer) error
	want string
}

// calls holds one call of each method, plus the variants the contract
// names, with the record F14 asks for. (Displayer.1, NewJSONDisplayer.1)
func calls() []call {
	return []call{
		{
			name: "ReplyDelta.1",
			do:   func(d display.Displayer) error { return d.ReplyDelta("Hel") },
			want: `{"type":"reply_delta","text":"Hel"}`,
		},
		{
			name: "ReasoningDelta.1",
			do:   func(d display.Displayer) error { return d.ReasoningDelta("thinking\n") },
			want: `{"type":"reasoning_delta","text":"thinking\n"}`,
		},
		{
			name: "Reply.1",
			do:   func(d display.Displayer) error { return d.Reply("Hello\x1b[0m") },
			want: `{"type":"reply","text":"Hello\u001b[0m"}`,
		},
		{
			name: "ReportToolCall.1/nested inputs",
			do: func(d display.Displayer) error {
				return d.ReportToolCall(display.ToolCall{Name: "ls", Inputs: map[string]any{
					"path":  "/tmp",
					"flags": []any{"-l", map[string]any{"depth": float64(2)}, nil},
					"all":   true,
					"opts":  map[string]any{"sort": nil},
				}})
			},
			want: `{"type":"tool_call","name":"ls","inputs":{"path":"/tmp","flags":["-l",{"depth":2},null],"all":true,"opts":{"sort":null}}}`,
		},
		{
			name: "ReportToolCall.1/nil inputs",
			do:   func(d display.Displayer) error { return d.ReportToolCall(display.ToolCall{Name: "date"}) },
			want: `{"type":"tool_call","name":"date","inputs":{}}`,
		},
		{
			name: "ReportToolCall.1/empty inputs",
			do: func(d display.Displayer) error {
				return d.ReportToolCall(display.ToolCall{Name: "date", Inputs: map[string]any{}})
			},
			want: `{"type":"tool_call","name":"date","inputs":{}}`,
		},
		{
			name: "ReportToolResult.1/ok",
			do: func(d display.Displayer) error {
				return d.ReportToolResult(display.ToolResult{Name: "ls", Output: "a\nERROR: b"})
			},
			want: `{"type":"tool_result","name":"ls","ok":true,"output":"a\nERROR: b"}`,
		},
		{
			name: "ReportToolResult.1/failure",
			do: func(d display.Displayer) error {
				return d.ReportToolResult(display.ToolResult{Name: "cat", Output: "ERROR: no such file: x\nusage: cat FILE"})
			},
			want: `{"type":"tool_result","name":"cat","ok":false,"output":"ERROR: no such file: x\nusage: cat FILE","error":"no such file: x"}`,
		},
		{
			name: "ReportToolResult.1/failure with a CRLF first line",
			do: func(d display.Displayer) error {
				return d.ReportToolResult(display.ToolResult{Name: "cat", Output: "ERROR: no such file: x\r\nusage: cat FILE"})
			},
			want: `{"type":"tool_result","name":"cat","ok":false,"output":"ERROR: no such file: x\r\nusage: cat FILE","error":"no such file: x"}`,
		},
		{
			name: "ReportToolResult.1/failure without text",
			do: func(d display.Displayer) error {
				return d.ReportToolResult(display.ToolResult{Name: "cat", Output: "ERROR: "})
			},
			want: `{"type":"tool_result","name":"cat","ok":false,"output":"ERROR: "}`,
		},
		{
			name: "ReportToolResult.1/prefix without space",
			do: func(d display.Displayer) error {
				return d.ReportToolResult(display.ToolResult{Name: "cat", Output: "ERROR:x"})
			},
			want: `{"type":"tool_result","name":"cat","ok":true,"output":"ERROR:x"}`,
		},
		{
			name: "ReportProgress.1/row",
			do: func(d display.Displayer) error {
				return d.ReportProgress(display.Progress{Title: "setup", Row: "models", State: "update", Detail: "3/5"})
			},
			want: `{"type":"progress","title":"setup","row":"models","state":"update","detail":"3/5"}`,
		},
		{
			name: "ReportProgress.1/status line",
			do: func(d display.Displayer) error {
				return d.ReportProgress(display.Progress{Title: "thinking", State: "start"})
			},
			want: `{"type":"progress","title":"thinking","state":"start"}`,
		},
		{
			name: "ShowPrompt.1/choices",
			do: func(d display.Displayer) error {
				return d.ShowPrompt(display.Prompt{Kind: "select", Text: "Pick one", Choices: []string{"a", "b"}})
			},
			want: `{"type":"prompt","kind":"select","text":"Pick one","choices":["a","b"]}`,
		},
		{
			name: "ShowPrompt.1/no choices",
			do: func(d display.Displayer) error {
				return d.ShowPrompt(display.Prompt{Kind: "confirm", Text: "Run ls? [y/N]"})
			},
			want: `{"type":"prompt","kind":"confirm","text":"Run ls? [y/N]"}`,
		},
	}
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("line %q is not a JSON object: %v", line, err)
	}
	return v
}

func TestDisplayerContract(t *testing.T) {
	for _, factory := range displayerFactories() {
		t.Run(factory.name, func(t *testing.T) {
			t.Run("Displayer.1/one record per call in order", func(t *testing.T) {
				rec := newRecordingEncoder()
				d := factory.new(rec)
				cs := calls()
				for _, c := range cs {
					if err := c.do(d); err != nil {
						t.Fatalf("%s: unexpected error: %v", c.name, err)
					}
				}
				if len(rec.records) != len(cs) {
					t.Fatalf("got %d records for %d calls", len(rec.records), len(cs))
				}
				out := rec.buf.String()
				if !strings.HasSuffix(out, "\n") {
					t.Fatalf("output does not end in a newline: %q", out)
				}
				lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
				if len(lines) != len(cs) {
					t.Fatalf("got %d lines for %d calls:\n%s", len(lines), len(cs), out)
				}
				for i, c := range cs {
					if !strings.HasPrefix(lines[i], `{"type":`) {
						t.Errorf("%s: line %q does not start with type", c.name, lines[i])
					}
					got, want := decode(t, lines[i]), decode(t, c.want)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s:\n got  %s\n want %s", c.name, lines[i], c.want)
					}
				}
			})

			for _, c := range calls() {
				t.Run(c.name+"/NewJSONDisplayer.1 exact record", func(t *testing.T) {
					rec := newRecordingEncoder()
					if err := c.do(factory.new(rec)); err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					got := strings.TrimSuffix(rec.buf.String(), "\n")
					if !reflect.DeepEqual(decode(t, got), decode(t, c.want)) {
						t.Errorf("got  %s\nwant %s", got, c.want)
					}
				})
				t.Run(c.name+"/Displayer.1 write failure returns", func(t *testing.T) {
					enc := &failingEncoder{}
					err := c.do(factory.new(enc))
					if !errors.Is(err, errEncode) {
						t.Fatalf("got %v, want the Encoder's error", err)
					}
					if enc.calls != 1 {
						t.Fatalf("got %d Encode calls, want 1", enc.calls)
					}
				})
			}
		})
	}
}
