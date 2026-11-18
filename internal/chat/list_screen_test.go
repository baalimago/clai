package chat

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingWriter struct {
	accept int
	err    error
}

func (f failingWriter) Write(p []byte) (int, error) {
	return min(len(p), f.accept), f.err
}

func TestListScreen_eraseClimbsEveryRowTheRenderTakes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		draw  func(s *listScreen, in io.Reader)
		want  string
	}{
		{
			name:  "one row",
			width: 80,
			draw:  func(s *listScreen, _ io.Reader) { io.WriteString(s, "(select): ") },
			want:  "\r\x1b[J",
		},
		{
			name:  "pages and the echoed answer",
			width: 80,
			draw: func(s *listScreen, in io.Reader) {
				io.WriteString(s, "\x1b[1mIndex | About\x1b[0m\n-----\n0 | a\n(select): ")
				io.ReadAll(in)
				io.WriteString(s, "1 | b\n(select): ")
			},
			want: "\r\x1b[5A\x1b[J",
		},
		{
			name:  "rows rewrapped at the new width",
			width: 40,
			draw:  func(s *listScreen, _ io.Reader) { io.WriteString(s, strings.Repeat("x", 100)+"\n(select): ") },
			want:  "\r\x1b[3A\x1b[J",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			s := &listScreen{out: &out}
			tc.draw(s, s.echoing(strings.NewReader("n\n")))
			drawn := out.Len()
			if err := s.erase(tc.width); err != nil {
				t.Fatalf("erase: %v", err)
			}
			if got := out.String()[drawn:]; got != tc.want {
				t.Fatalf("erase wrote %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListScreen_eraseForgetsTheRender(t *testing.T) {
	var out strings.Builder
	s := &listScreen{out: &out}
	io.WriteString(s, "a\nb\nc")
	if err := s.erase(80); err != nil {
		t.Fatal(err)
	}
	io.WriteString(s, "d")
	drawn := out.Len()
	if err := s.erase(80); err != nil {
		t.Fatal(err)
	}
	if got := out.String()[drawn:]; got != "\r\x1b[J" {
		t.Fatalf("the second erase wrote %q, want only the row drawn since the first", got)
	}
}

func TestListScreen_resetForgetsTheRender(t *testing.T) {
	var out strings.Builder
	s := &listScreen{out: &out}
	io.WriteString(s, "a\nb\n")
	s.reset()
	io.WriteString(s, "c")
	drawn := out.Len()
	if err := s.erase(80); err != nil {
		t.Fatal(err)
	}
	if got := out.String()[drawn:]; got != "\r\x1b[J" {
		t.Fatalf("erase after reset wrote %q", got)
	}
}

func TestListScreen_failures(t *testing.T) {
	refused := errors.New("terminal gone")
	s := &listScreen{out: failingWriter{accept: 2, err: refused}}
	if n, err := s.Write([]byte("abc\n")); n != 2 || !errors.Is(err, refused) {
		t.Fatalf("Write() = %d, %v; want 2 and the writer's error", n, err)
	}
	if got := s.text.String(); got != "ab" {
		t.Fatalf("recorded %q, want only the written bytes", got)
	}
	if err := s.erase(80); !errors.Is(err, refused) {
		t.Fatalf("erase() = %v, want the writer's error", err)
	}
}
