package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/term"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

const resizeStep = "<resize>"

// scriptedListInput plays lines and width changes, and answers Observe from
// widths, the last one repeating.
type scriptedListInput struct {
	steps    []string
	widths   []int
	observed int
	pending  string
	closes   int
	closeErr error
}

func (s *scriptedListInput) Read(p []byte) (int, error) {
	if s.pending == "" {
		if len(s.steps) == 0 {
			return 0, io.EOF
		}
		step := s.steps[0]
		s.steps = s.steps[1:]
		if step == resizeStep {
			return 0, fmt.Errorf("scripted: %w", term.ErrResized)
		}
		s.pending = step
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *scriptedListInput) Observe() dimensions.Dimensions {
	w := s.widths[min(s.observed, len(s.widths)-1)]
	s.observed++
	return dimensions.Dimensions{Width: w, Height: 40}
}

func (s *scriptedListInput) Close() error {
	s.closes++
	return s.closeErr
}

func resizeHandler(t *testing.T, chats int) *ChatHandler {
	t.Helper()
	cq, confDir := newTestHandler(t)
	t.Cleanup(useTestSourceReaders(nil))
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	for i := range chats {
		saveAll(t, conversationsDir(confDir), pub_models.Chat{
			ID:       fmt.Sprintf("chat-%02d", i),
			Title:    fmt.Sprintf("conversation number %02d about the edge router", i),
			Created:  start.Add(-time.Duration(i) * time.Minute),
			Messages: []pub_models.Message{{Role: "user", Content: fmt.Sprintf("prompt %02d", i)}},
		})
	}
	return cq
}

func runList(t *testing.T, cq *ChatHandler) (string, error) {
	t.Helper()
	paginator, err := NewChatIndexPaginator(context.Background(), cq.convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	err = cq.listChats(context.Background(), paginator, "")
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(out.String(), ""), err
}

// renders splits the output at each table header.
func renders(out string) []string {
	parts := strings.Split(out, "Index ")
	return parts[1:]
}

func TestListChats_widthChangeRedrawsTheTable(t *testing.T) {
	cq := resizeHandler(t, utils.TableTheme().Items+3)
	input := &scriptedListInput{steps: []string{"n\n", resizeStep, "q\n"}, widths: []int{200, 80}}
	cq.openListInput = func(context.Context) (listInput, error) { return input, nil }
	cq.input = strings.NewReader("")

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v\n%s", err, out)
	}
	tables := renders(out)
	if len(tables) != 2 {
		t.Fatalf("want the table drawn at 200 and redrawn at 80, got %d headers:\n%s", len(tables), out)
	}
	if !strings.Contains(tables[0], "Tokens") || strings.Contains(tables[1], "Tokens") || strings.Contains(tables[1], "Model") {
		t.Fatalf("the redraw should drop the columns 80 cannot hold:\n%s", out)
	}
	if !strings.Contains(tables[1], "conversation number 10") || strings.Contains(tables[1], "conversation number 00") {
		t.Fatalf("the redraw should stay on page 1:\n%s", tables[1])
	}
	for line := range strings.SplitSeq(tables[1], "\n") {
		if cells := utils.TerminalStringWidth(line); cells >= 80 && !strings.Contains(line, "(select") {
			t.Fatalf("a redrawn row is %d cells at 80: %q", cells, line)
		}
	}
	// The old render, with the "n" the terminal echoed, rewraps at 80
	// columns; the redraw climbs to its first row and clears from there.
	erased := strings.Index(out, "\r\x1b[")
	if erased < 0 {
		t.Fatalf("the redraw should erase the old render first:\n%q", out)
	}
	old := strings.Replace(out[:erased], "page 0/1): ", "page 0/1): n\n", 1)
	if want := fmt.Sprintf("\r\x1b[%dA\x1b[J", utils.TerminalRows(old, 80)-1); !strings.HasPrefix(out[erased:], want) {
		t.Fatalf("the erase should climb the old render's rows at 80 columns, want %q:\n%q", want, out[erased:])
	}
	if input.closes != 1 {
		t.Fatalf("the list input was closed %d times, want 1", input.closes)
	}
}

func TestListChats_widthChangeKeepsTheFilter(t *testing.T) {
	cq := resizeHandler(t, 13)
	input := &scriptedListInput{steps: []string{"/number 1\n", resizeStep, "q\n"}, widths: []int{200, 80}}
	cq.openListInput = func(context.Context) (listInput, error) { return input, nil }

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v\n%s", err, out)
	}
	tables := renders(out)
	if len(tables) != 2 {
		t.Fatalf("want the table drawn and redrawn, got %d headers:\n%s", len(tables), out)
	}
	redraw := tables[1]
	if !strings.Contains(redraw, `filter: "number 1"`) || !strings.Contains(redraw, "conversation number 12") {
		t.Fatalf("the redraw should keep the filter:\n%s", redraw)
	}
	if strings.Contains(redraw, "conversation number 0") {
		t.Fatalf("the redraw should list only the matches:\n%s", redraw)
	}
}

func TestListChats_pipedOutputShowsEveryColumn(t *testing.T) {
	cq := resizeHandler(t, 2)
	cq.dims = dimensions.Dimensions{Width: 80}
	cq.input = strings.NewReader("q\n")

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v", err)
	}
	if !strings.Contains(out, "| Tokens") || !strings.Contains(out, "| conversation number 00 about the edge router") {
		t.Fatalf("output that is not a terminal should show every column and whole labels:\n%s", out)
	}
}

func TestListChats_noWidthChangeNoRedraw(t *testing.T) {
	cq := resizeHandler(t, 2)
	input := &scriptedListInput{steps: []string{"q\n"}, widths: []int{120}}
	cq.openListInput = func(context.Context) (listInput, error) { return input, nil }

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v", err)
	}
	if n := len(renders(out)); n != 1 {
		t.Fatalf("want one table, got %d:\n%s", n, out)
	}
}

func TestListChats_unsupportedResizeReadsTheRunInput(t *testing.T) {
	cq := resizeHandler(t, 2)
	cq.dims = dimensions.Dimensions{Width: 100}
	cq.openListInput = func(context.Context) (listInput, error) {
		return nil, fmt.Errorf("%w: no controlling terminal", term.ErrResizeUnsupported)
	}
	cq.input = strings.NewReader("q\n")

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v", err)
	}
	if !strings.Contains(out, "conversation number 00") {
		t.Fatalf("the list should render from the run's input:\n%s", out)
	}
}

func TestListChats_listInputFailureIsReturned(t *testing.T) {
	cq := resizeHandler(t, 1)
	broken := errors.New("terminal exploded")
	cq.openListInput = func(context.Context) (listInput, error) { return nil, broken }
	cq.input = strings.NewReader("q\n")

	if _, err := runList(t, cq); !errors.Is(err, broken) {
		t.Fatalf("listChats() = %v, want the open failure", err)
	}
}

func TestListChats_listInputCloseFailureIsReturned(t *testing.T) {
	cq := resizeHandler(t, 1)
	closeErr := errors.New("close refused")
	input := &scriptedListInput{steps: []string{"q\n"}, widths: []int{120}, closeErr: closeErr}
	cq.openListInput = func(context.Context) (listInput, error) { return input, nil }

	if _, err := runList(t, cq); !errors.Is(err, closeErr) {
		t.Fatalf("listChats() = %v, want the close failure", err)
	}
}

func TestListChats_aboutShowsAtAHundredColumns(t *testing.T) {
	cq := resizeHandler(t, 2)
	cq.dims = dimensions.Dimensions{Width: 100}
	cq.outIsTerminal = true
	cq.input = strings.NewReader("q\n")

	out, err := runList(t, cq)
	if err != nil {
		t.Fatalf("listChats: %v", err)
	}
	if strings.Contains(out, "Tokens") || !strings.Contains(out, "| conversation number 00 about the edge router") {
		t.Fatalf("at 100 columns the About column must show the title:\n%s", out)
	}
}

func streamsContext(stdin io.Reader, stdout io.Writer, stdinTerminal, stdoutTerminal bool) context.Context {
	return runenv.With(context.Background(), runenv.New(runenv.Config{Streams: stdio.New(stdio.Config{
		Stdin: stdin, Stdout: stdout, Stderr: io.Discard,
		StdinTerminal: stdinTerminal, StdoutTerminal: stdoutTerminal,
	})}))
}

func newConfDir(t *testing.T) string {
	t.Helper()
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	return confDir
}

func TestNew_watchesTheWidthOnlyOnAnInteractiveTerminal(t *testing.T) {
	confDir := newConfDir(t)
	for _, tc := range []struct {
		name          string
		stdin, stdout bool
	}{
		// The caller declared terminals but supplied its own input.
		{"declared terminals", true, true},
		{"piped stdin", false, true},
		{"piped stdout", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := New(streamsContext(strings.NewReader(""), io.Discard, tc.stdin, tc.stdout), confDir, "list", "", false, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if ch.openListInput != nil {
				t.Fatal("the width watch is wired without a terminal to read")
			}
			if ch.outIsTerminal != tc.stdout {
				t.Fatalf("outIsTerminal = %v, want %v", ch.outIsTerminal, tc.stdout)
			}
		})
	}
}
