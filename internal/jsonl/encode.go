package jsonl

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/errctx"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func init() {
	ErrInvalidRecord = errors.New("jsonl: invalid record")
}

// object is an ordered JSON object that, unlike slog.GroupValue, keeps
// empty groups among its attributes.
type object []slog.Attr

type encoder struct {
	mu          sync.Mutex
	destination io.Writer
}

func (e *encoder) Encode(r Record) error {
	if r.Type == "" {
		return errctx.Errorf("%w: empty type", ErrInvalidRecord)
	}
	line := make([]byte, 0, 256)
	line = append(line, `{"type":`...)
	line = appendString(line, r.Type)
	for _, field := range r.Fields {
		if field.Key == "type" {
			return errctx.Errorf("%w: field named \"type\" in a %q record", ErrInvalidRecord, r.Type)
		}
		var err error
		line = append(line, ',')
		if line, err = appendAttr(line, field); err != nil {
			return errctx.Errorf("jsonl: encode a %q record: %w", r.Type, err)
		}
	}
	line = append(line, "}\n"...)

	e.mu.Lock()
	defer e.mu.Unlock()
	written, err := e.destination.Write(line)
	if err != nil {
		return errctx.Errorf("jsonl: write a %q record: %w", r.Type, err)
	}
	if written != len(line) {
		return errctx.Errorf("jsonl: write a %q record: %d of %d bytes: %w", r.Type, written, len(line), io.ErrShortWrite)
	}
	return nil
}

func appendAttr(buf []byte, attr slog.Attr) ([]byte, error) {
	buf = appendString(buf, attr.Key)
	buf = append(buf, ':')
	return appendValue(buf, attr.Value)
}

func appendObject(buf []byte, attrs []slog.Attr) ([]byte, error) {
	buf = append(buf, '{')
	for i, attr := range attrs {
		if i > 0 {
			buf = append(buf, ',')
		}
		var err error
		if buf, err = appendAttr(buf, attr); err != nil {
			return nil, errctx.Errorf("field %q: %w", attr.Key, err)
		}
	}
	return append(buf, '}'), nil
}

func appendValue(buf []byte, value slog.Value) ([]byte, error) {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return appendString(buf, value.String()), nil
	case slog.KindBool:
		return strconv.AppendBool(buf, value.Bool()), nil
	case slog.KindInt64:
		return strconv.AppendInt(buf, value.Int64(), 10), nil
	case slog.KindUint64:
		return strconv.AppendUint(buf, value.Uint64(), 10), nil
	case slog.KindFloat64:
		return appendFloat(buf, value.Float64()), nil
	case slog.KindDuration:
		return appendFloat(buf, value.Duration().Seconds()), nil
	case slog.KindTime:
		return appendString(buf, value.Time().Format(time.RFC3339Nano)), nil
	case slog.KindGroup:
		return appendObject(buf, value.Group())
	default:
		return appendAny(buf, value.Any())
	}
}

func appendAny(buf []byte, value any) ([]byte, error) {
	switch value := value.(type) {
	case nil:
		return append(buf, "null"...), nil
	case object:
		return appendObject(buf, value)
	case slog.Value:
		return appendValue(buf, value)
	}
	if converted := slog.AnyValue(value); converted.Kind() != slog.KindAny {
		return appendValue(buf, converted)
	}
	reflected := reflect.ValueOf(value)
	if isNil(reflected) {
		return append(buf, "null"...), nil
	}
	if err, ok := value.(error); ok {
		return appendString(buf, err.Error()), nil
	}
	return appendReflected(buf, reflected)
}

func appendReflected(buf []byte, value reflect.Value) ([]byte, error) {
	switch value.Kind() {
	case reflect.Bool:
		return strconv.AppendBool(buf, value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(buf, value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.AppendUint(buf, value.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return appendFloat(buf, value.Float()), nil
	case reflect.String:
		return appendString(buf, value.String()), nil
	case reflect.Pointer, reflect.Interface:
		return appendAny(buf, value.Elem().Interface())
	case reflect.Slice, reflect.Array:
		return appendArray(buf, value)
	case reflect.Map:
		if value.Type().Key().Kind() == reflect.String {
			return appendMap(buf, value)
		}
	}
	return nil, errctx.Errorf("unsupported value of type %s", value.Type())
}

func appendArray(buf []byte, value reflect.Value) ([]byte, error) {
	buf = append(buf, '[')
	for i := range value.Len() {
		if i > 0 {
			buf = append(buf, ',')
		}
		var err error
		if buf, err = appendAny(buf, value.Index(i).Interface()); err != nil {
			return nil, errctx.Errorf("element %d: %w", i, err)
		}
	}
	return append(buf, ']'), nil
}

func appendMap(buf []byte, value reflect.Value) ([]byte, error) {
	keys := value.MapKeys()
	slices.SortFunc(keys, func(a, b reflect.Value) int {
		switch {
		case a.String() < b.String():
			return -1
		case a.String() > b.String():
			return 1
		}
		return 0
	})
	buf = append(buf, '{')
	for i, key := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendString(buf, key.String())
		buf = append(buf, ':')
		var err error
		if buf, err = appendAny(buf, value.MapIndex(key).Interface()); err != nil {
			return nil, errctx.Errorf("key %q: %w", key.String(), err)
		}
	}
	return append(buf, '}'), nil
}

func isNil(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	}
	return false
}

// appendFloat writes a non-finite float, which JSON cannot hold as a
// number, as null.
func appendFloat(buf []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(buf, "null"...)
	}
	return strconv.AppendFloat(buf, f, 'g', -1, 64)
}

const hexDigits = "0123456789abcdef"

func appendString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			buf = append(buf, "\uFFFD"...)
		case r == '"' || r == '\\':
			buf = append(buf, '\\', byte(r))
		case r == '\n':
			buf = append(buf, `\n`...)
		case r == '\r':
			buf = append(buf, `\r`...)
		case r == '\t':
			buf = append(buf, `\t`...)
		case unicode.IsControl(r) || r == '\u2028' || r == '\u2029':
			buf = append(buf, '\\', 'u', hexDigits[r>>12&0xf], hexDigits[r>>8&0xf], hexDigits[r>>4&0xf], hexDigits[r&0xf])
		default:
			buf = utf8.AppendRune(buf, r)
		}
	}
	return append(buf, '"')
}

func levelName(level diagnostic.Level) string {
	switch level {
	case diagnostic.LevelError:
		return "error"
	case diagnostic.LevelWarn:
		return "warn"
	case diagnostic.LevelInfo:
		return "info"
	case diagnostic.LevelDebug:
		return "debug"
	case diagnostic.LevelTrace:
		return "trace"
	}
	return "level(" + strconv.Itoa(int(level)) + ")"
}
