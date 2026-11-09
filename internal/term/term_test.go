package term

import (
	"os"
	"testing"
)

func TestIsTerminal_devNullIsNot(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Fatal("/dev/null counted as a terminal")
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	if IsTerminal(rd) {
		t.Fatal("a pipe counted as a terminal")
	}
	if ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0); err == nil {
		defer ptmx.Close()
		if !IsTerminal(ptmx) {
			t.Fatal("a pseudo-terminal is not a terminal")
		}
	}
}
