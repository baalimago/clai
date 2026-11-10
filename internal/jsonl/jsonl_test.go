package jsonl_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type encoderFactory struct {
	name string
	new  func(io.Writer) jsonl.Encoder
}

func encoderFactories() []encoderFactory {
	return []encoderFactory{{name: "NewEncoder", new: jsonl.NewEncoder}}
}

// unlockedBuffer appends without a lock of its own, so the race detector
// sees any write the Encoder does not serialise (Encode.3).
type unlockedBuffer struct {
	data []byte
}

func (b *unlockedBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

type countingWriter struct {
	writes int
	buf    bytes.Buffer
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.buf.Write(p)
}

var errDestination = errors.New("destination failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errDestination }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

type orderedKey struct {
	key   string
	value any
}

// decodeObject decodes one JSON object line, keeping key order.
func decodeObject(t *testing.T, line string) []orderedKey {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatalf("line %q does not open an object: %v %v", line, token, err)
	}
	var keys []orderedKey
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			t.Fatalf("line %q: key: %v", line, err)
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("line %q: value of %v: %v", line, keyToken, err)
		}
		keys = append(keys, orderedKey{key: keyToken.(string), value: value})
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("line %q does not close its object: %v", line, err)
	}
	if decoder.More() {
		t.Fatalf("line %q holds more than one value", line)
	}
	return keys
}

func encodeOne(t *testing.T, factory encoderFactory, record jsonl.Record) string {
	t.Helper()
	writer := &countingWriter{}
	if err := factory.new(writer).Encode(record); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if writer.writes != 1 {
		t.Fatalf("Encode.3: want one Write per record, got %d", writer.writes)
	}
	out := writer.buf.String()
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("Encode.1: want one line ending in \\n, got %q", out)
	}
	return out
}

func TestEncoderContract(t *testing.T) {
	t.Parallel()
	for _, factory := range encoderFactories() {
		t.Run(factory.name, func(t *testing.T) {
			t.Parallel()
			t.Run("Encode.1 type first and fields in order", func(t *testing.T) {
				t.Parallel()
				line := encodeOne(t, factory, jsonl.Record{Type: "output", Fields: []slog.Attr{
					slog.String("stream", "stdout"), slog.String("text", "hello"),
				}})
				if line != `{"type":"output","stream":"stdout","text":"hello"}`+"\n" {
					t.Fatalf("got %q", line)
				}
			})
			t.Run("Encode.1 every value kind", func(t *testing.T) {
				t.Parallel()
				when := time.Date(2026, 11, 9, 17, 30, 0, 500, time.UTC)
				line := encodeOne(t, factory, jsonl.Record{Type: "kinds", Fields: []slog.Attr{
					slog.String("s", "text"),
					slog.Bool("b", true),
					slog.Int("i", -3),
					slog.Uint64("u", 7),
					slog.Float64("f", 1.5),
					slog.Duration("d", 1500*time.Millisecond),
					slog.Time("t", when),
					slog.Any("err", errors.New("boom")),
					slog.Group("g", slog.String("k", "v"), slog.Group("inner", slog.Int("n", 1))),
					{Key: "empty_group", Value: slog.GroupValue()},
					slog.Any("map", map[string]any{"b": []any{1, "x", nil, map[string]any{}}, "a": nil, "c": true}),
					slog.Any("empty_map", map[string]any{}),
					slog.Any("objects", []map[string]any{{"message": "m", "status": 404}, {"message": "n"}}),
					slog.Any("strings", []string{"x", "y"}),
					slog.Any("empty_slice", []string{}),
					slog.Any("nil", nil),
					slog.Any("nil_error", error(nil)),
					slog.Any("any_int", 4),
					slog.Any("any_bool", false),
					slog.Any("any_duration", 2*time.Second),
					slog.Any("any_time", when),
					slog.Any("values", []slog.Value{slog.GroupValue(slog.String("message", "v")), slog.IntValue(2)}),
				}})
				var decoded map[string]any
				if err := json.Unmarshal([]byte(line), &decoded); err != nil {
					t.Fatalf("Encode.1: %q is not JSON: %v", line, err)
				}
				want := map[string]any{
					"type": "kinds", "s": "text", "b": true, "i": float64(-3), "u": float64(7), "f": 1.5,
					"d": 1.5, "t": when.Format(time.RFC3339Nano), "err": "boom",
					"g":           map[string]any{"k": "v", "inner": map[string]any{"n": float64(1)}},
					"empty_group": map[string]any{},
					"map": map[string]any{
						"a": nil, "b": []any{float64(1), "x", nil, map[string]any{}}, "c": true,
					},
					"empty_map":    map[string]any{},
					"objects":      []any{map[string]any{"message": "m", "status": float64(404)}, map[string]any{"message": "n"}},
					"strings":      []any{"x", "y"},
					"empty_slice":  []any{},
					"nil":          nil,
					"nil_error":    nil,
					"any_int":      float64(4),
					"any_bool":     false,
					"any_duration": float64(2),
					"any_time":     when.Format(time.RFC3339Nano),
					"values":       []any{map[string]any{"message": "v"}, float64(2)},
				}
				if got, wantText := fmt.Sprint(decoded), fmt.Sprint(want); got != wantText {
					t.Fatalf("Encode.1:\n got %s\nwant %s", got, wantText)
				}
				if !strings.Contains(line, `"empty_group":{}`) || !strings.Contains(line, `"empty_map":{}`) {
					t.Fatalf("Encode.1: empty objects must be {}: %q", line)
				}
				keys := decodeObject(t, line)
				if keys[0].key != "type" || keys[1].key != "s" || keys[len(keys)-1].key != "values" {
					t.Fatalf("Encode.1: order lost: %v", keys)
				}
			})
			t.Run("Encode.1 a non-finite float is still JSON", func(t *testing.T) {
				t.Parallel()
				line := encodeOne(t, factory, jsonl.Record{Type: "x", Fields: []slog.Attr{
					slog.Float64("nan", math.NaN()), slog.Float64("inf", math.Inf(1)),
				}})
				decodeObject(t, line)
			})
			t.Run("Encode.1 an unsupported value returns an error", func(t *testing.T) {
				t.Parallel()
				var out bytes.Buffer
				err := factory.new(&out).Encode(jsonl.Record{Type: "x", Fields: []slog.Attr{slog.Any("ch", make(chan int))}})
				if err == nil || out.Len() != 0 {
					t.Fatalf("want an error and nothing written, got %v and %q", err, out.String())
				}
			})
			t.Run("Encode.2 control characters and invalid UTF-8", func(t *testing.T) {
				t.Parallel()
				hostile := "\x1b[31mred\x1b[0m\rover\x00\t\"q\"\\ \xff\xfe end \u2028"
				line := encodeOne(t, factory, jsonl.Record{Type: "output", Fields: []slog.Attr{
					slog.String("text", hostile),
					slog.Any("map", map[string]any{"\x1bkey": "\rv"}),
				}})
				body := strings.TrimSuffix(line, "\n")
				for i := 0; i < len(body); i++ {
					if body[i] < 0x20 {
						t.Fatalf("Encode.2: raw control byte %#x in %q", body[i], line)
					}
				}
				if !utf8.ValidString(line) {
					t.Fatalf("Encode.2: invalid UTF-8 in %q", line)
				}
				keys := decodeObject(t, line)
				want := "\x1b[31mred\x1b[0m\rover\x00\t\"q\"\\ \uFFFD\uFFFD end \u2028"
				if keys[1].value != want {
					t.Fatalf("Encode.2: round trip\n got %q\nwant %q", keys[1].value, want)
				}
				if got := keys[2].value.(map[string]any)["\x1bkey"]; got != "\rv" {
					t.Fatalf("Encode.2: map key and value round trip: %v", keys[2].value)
				}
			})
			t.Run("Encode.3 concurrent records stay whole lines", func(t *testing.T) {
				t.Parallel()
				destination := &unlockedBuffer{}
				encoder := factory.new(destination)
				const writers, perWriter = 8, 50
				var wg sync.WaitGroup
				errs := make(chan error, writers*perWriter)
				for w := range writers {
					wg.Go(func() {
						for i := range perWriter {
							errs <- encoder.Encode(jsonl.Record{Type: "output", Fields: []slog.Attr{
								slog.Int("writer", w), slog.Int("i", i), slog.String("text", strings.Repeat("x", 200)),
							}})
						}
					})
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatalf("Encode.3: %v", err)
					}
				}
				lines := strings.Split(strings.TrimSuffix(string(destination.data), "\n"), "\n")
				if len(lines) != writers*perWriter {
					t.Fatalf("Encode.3: want %d lines, got %d", writers*perWriter, len(lines))
				}
				for _, line := range lines {
					if keys := decodeObject(t, line); keys[0].key != "type" || len(keys) != 4 {
						t.Fatalf("Encode.3: broken line %q", line)
					}
				}
			})
			t.Run("Encode.4 invalid records write nothing", func(t *testing.T) {
				t.Parallel()
				for name, record := range map[string]jsonl.Record{
					"empty type": {Fields: []slog.Attr{slog.String("text", "x")}},
					"type field": {Type: "output", Fields: []slog.Attr{slog.String("type", "spoof")}},
				} {
					var out bytes.Buffer
					err := factory.new(&out).Encode(record)
					if !errors.Is(err, jsonl.ErrInvalidRecord) {
						t.Fatalf("Encode.4 %s: want ErrInvalidRecord, got %v", name, err)
					}
					if out.Len() != 0 {
						t.Fatalf("Encode.4 %s: wrote %q", name, out.String())
					}
				}
			})
			t.Run("Encode.4 failed and short writes", func(t *testing.T) {
				t.Parallel()
				record := jsonl.Record{Type: "output", Fields: []slog.Attr{slog.String("text", "x")}}
				if err := factory.new(failingWriter{}).Encode(record); !errors.Is(err, errDestination) {
					t.Fatalf("Encode.4: want the writer's error, got %v", err)
				}
				if err := factory.new(shortWriter{}).Encode(record); !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("Encode.4: want io.ErrShortWrite, got %v", err)
				}
			})
		})
	}
}

func TestEventRecord(t *testing.T) {
	t.Parallel()
	names := map[diagnostic.Level]string{
		diagnostic.LevelError: "error", diagnostic.LevelWarn: "warn", diagnostic.LevelInfo: "info",
		diagnostic.LevelDebug: "debug", diagnostic.LevelTrace: "trace",
	}
	for level, name := range names {
		t.Run("EventRecord.1 "+name, func(t *testing.T) {
			t.Parallel()
			record := jsonl.EventRecord(diagnostic.Event{
				Type: "mcp.start", Subsystem: "mcp", Level: level, Message: "server failed",
				Facts: []slog.Attr{
					slog.String("server", "fs"),
					slog.Group("exit", slog.Int("code", 2), slog.String("signal", "")),
					{Key: "empty", Value: slog.GroupValue()},
				},
			})
			line := encodeOne(t, encoderFactories()[0], record)
			keys := decodeObject(t, line)
			wantKeys := []string{"type", "name", "subsystem", "level", "message", "facts"}
			if len(keys) != len(wantKeys) {
				t.Fatalf("EventRecord.1: want fields %v, got %v", wantKeys, keys)
			}
			for i, key := range wantKeys {
				if keys[i].key != key {
					t.Fatalf("EventRecord.1: field %d: want %s, got %s", i, key, keys[i].key)
				}
			}
			if keys[0].value != "diagnostic" || keys[1].value != "mcp.start" || keys[2].value != "mcp" ||
				keys[3].value != name || keys[4].value != "server failed" {
				t.Fatalf("EventRecord.1: %v", keys)
			}
			if !strings.HasSuffix(line, `"facts":{"server":"fs","exit":{"code":2,"signal":""},"empty":{}}}`+"\n") {
				t.Fatalf("EventRecord.1: facts lost: %q", line)
			}
		})
	}
	t.Run("EventRecord.1 no facts", func(t *testing.T) {
		t.Parallel()
		line := encodeOne(t, encoderFactories()[0], jsonl.EventRecord(diagnostic.Event{Type: "t", Subsystem: "s", Message: "m"}))
		if line != `{"type":"diagnostic","name":"t","subsystem":"s","level":"error","message":"m","facts":{}}`+"\n" {
			t.Fatalf("EventRecord.1: got %q", line)
		}
	})
}
