package text

import "testing"

func TestTailBufferKeepsNewestBytes(t *testing.T) {
	buf := tailBuffer{capacity: 4}
	buf.WriteString("abcd")
	buf.WriteString("ef")

	if got := buf.String(); got != "cdef" {
		t.Fatalf("String() = %q, want cdef", got)
	}
	if buf.Len() != 4 {
		t.Fatalf("Len() = %d, want 4", buf.Len())
	}

	buf.WriteString("12345")
	if got := buf.String(); got != "2345" {
		t.Fatalf("String() after oversized write = %q, want 2345", got)
	}

	buf.Reset()
	if buf.Len() != 0 || buf.String() != "" {
		t.Fatalf("buffer after Reset() = %q (length %d), want empty", buf.String(), buf.Len())
	}
	buf.WriteString("xy")
	if got := buf.String(); got != "xy" {
		t.Fatalf("String() after Reset() and write = %q, want xy", got)
	}
}
