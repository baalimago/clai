package cloudcmd

import (
	"context"
	"os"
	"strings"
	"testing"
)

func firstPullHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, "tok-a")
	h.srv.Put("config", "profiles/x.json", "theirs")
	h.write("profiles/x.json", "mine")
	h.tty = true
	return h
}

func TestFirstPull_unknownAnswerAsksAgain(t *testing.T) {
	h := firstPullHarness(t)
	h.in = "yes\n\nr\n"
	code, out := h.run("pull", "config")
	if code != 0 || h.read("profiles/x.json") != "theirs" {
		t.Fatalf("code %d, file %q:\n%s", code, h.read("profiles/x.json"), out)
	}
	if n := strings.Count(out, "Keep [l/r/p/q]"); n != 3 {
		t.Fatalf("asked %d times, want 3:\n%s", n, out)
	}
}

func TestFirstPull_quitStopsCleanly(t *testing.T) {
	h := firstPullHarness(t)
	h.in = "q\n"
	r := &runner{deps: h.deps().withDefaults()}
	if err := r.pull(context.Background(), nil, false, false, false); err != nil {
		t.Fatalf("quit is an error: %v", err)
	}
	if h.exit != ExitDecisionNeeded || h.read("profiles/x.json") != "mine" {
		t.Fatalf("exit %d, file %q", h.exit, h.read("profiles/x.json"))
	}
	out := h.out.String()
	mustContain(t, out, "stopped: nothing was pulled")
	if strings.Contains(out, "▸ conversations") {
		t.Fatalf("pulled on after quit:\n%s", out)
	}
}

func TestIsTerminal_devNullIsNot(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null counted as a terminal")
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	if isTerminal(rd) {
		t.Fatal("a pipe counted as a terminal")
	}
	if ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0); err == nil {
		defer ptmx.Close()
		if !isTerminal(ptmx) {
			t.Fatal("a pseudo-terminal is not a terminal")
		}
	}
}
