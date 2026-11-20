package menu_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/menu"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

var errBroken = errors.New("broken pipe")

type failing struct{}

func (failing) Read([]byte) (int, error)  { return 0, errBroken }
func (failing) Write([]byte) (int, error) { return 0, errBroken }

func TestScreen_DrawReplacesExactlyWhatItShowed(t *testing.T) {
	var out strings.Builder
	s := &menu.Screen{In: strings.NewReader("1\n"), Out: &out}
	draw := func(content string, cols int) {
		t.Helper()
		if err := s.Draw(content, cols); err != nil {
			t.Fatal(err)
		}
	}
	draw("a\nb\n", 80)
	if answer, err := s.Ask(t.Context(), "pick: "); answer != "1" || err != nil {
		t.Fatalf("Ask = %q, %v", answer, err)
	}
	if _, err := s.Write([]byte("playing\n")); err != nil {
		t.Fatal(err)
	}
	s.Echoed("")
	draw("c\n", 80)
	draw("d", 10)
	draw(strings.Repeat("x", 25)+"\n", 10)
	draw("e", 10)
	want := "\na\nb\npick: playing\n" + // the first draw starts on a row of its own
		"\x1b[5A\r\x1b[Jc\n" + // a, b, the question with its echoed answer, playing, the echoed Enter
		"\x1b[1A\r\x1b[Jd" +
		"\r\x1b[J" + strings.Repeat("x", 25) + "\n" + // nothing to go up over
		"\x1b[3A\r\x1b[Je" // 25 cells take 3 rows of 10
	if out.String() != want {
		t.Fatalf("the screen got\n%q\nwant\n%q", out.String(), want)
	}
}

func TestScreen_Ask(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, row := range []struct {
		name   string
		ctx    context.Context
		in     string
		answer string
		left   bool
		// out is what the screen got, and rest what the input still holds.
		out, rest string
	}{
		{name: "an answer is one line, trimmed", in: " lorentz \nrest\n", answer: "lorentz", out: "ask: ", rest: "rest\n"},
		{name: "an empty line is an answer", in: "\n", out: "ask: "},
		{name: "q leaves", in: "q\nrest", left: true, out: "ask: ", rest: "rest"},
		{name: "quit leaves", in: "quit\n", left: true, out: "ask: "},
		{name: "QUIT leaves", in: "QUIT\n", left: true, out: "ask: "},
		{name: "the end of the input leaves, below the question", left: true, out: "ask: \n"},
		{name: "a cancelled ctx leaves before anything is asked", ctx: cancelled, in: "1\n", left: true, rest: "1\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			var out strings.Builder
			in := strings.NewReader(row.in)
			if row.ctx == nil {
				row.ctx = t.Context()
			}
			answer, err := (&menu.Screen{In: in, Out: &out}).Ask(row.ctx, "ask: ")
			rest, _ := io.ReadAll(in)
			if answer != row.answer || errors.Is(err, menu.ErrLeft) != row.left || (err != nil) != row.left || out.String() != row.out || string(rest) != row.rest {
				t.Fatalf("Ask = %q, %v; the screen got %q and the input holds %q; want %q, left %v, %q and %q", answer, err, out.String(), rest, row.answer, row.left, row.out, row.rest)
			}
		})
	}
}

func TestScreen_Failures(t *testing.T) {
	var asked []string
	prompting := &menu.Screen{In: strings.NewReader("1\n"), Out: failing{}, Prompt: func(q string) error {
		asked = append(asked, q)
		return nil
	}}
	if answer, err := prompting.Ask(t.Context(), "ask: "); answer != "1" || err != nil || len(asked) != 1 || asked[0] != "ask: " {
		t.Fatalf("with a Prompt Ask = %q, %v after the prompts %q; want the question shown by the Prompt alone", answer, err, asked)
	}
	if _, err := prompting.Ask(t.Context(), "ask: "); !errors.Is(err, menu.ErrLeft) {
		t.Fatalf("with a Prompt the end of the input gave %v, want ErrLeft and nothing written", err)
	}
	_, write := (&menu.Screen{Out: failing{}}).Write([]byte("a"))
	_, ask := (&menu.Screen{In: strings.NewReader("1\n"), Out: failing{}}).Ask(t.Context(), "ask: ")
	_, prompt := (&menu.Screen{Prompt: func(string) error { return errBroken }}).Ask(t.Context(), "ask: ")
	for name, err := range map[string]error{"Draw": (&menu.Screen{Out: failing{}}).Draw("a", 80), "Write": write, "Ask": ask, "Prompt": prompt} {
		if _, isOutput := errors.AsType[*menu.OutputError](err); !isOutput || !errors.Is(err, errBroken) || err.Error() != "menu: write to the session: broken pipe" {
			t.Fatalf("%s on a broken screen gave %v, want an *OutputError", name, err)
		}
	}
	_, err := (&menu.Screen{In: failing{}, Out: io.Discard}).Ask(t.Context(), "ask: ")
	if _, isInput := errors.AsType[*menu.InputError](err); !isInput || !errors.Is(err, errBroken) || !strings.HasPrefix(err.Error(), "menu: read an answer: ") {
		t.Fatalf("Ask on a broken input gave %v, want an *InputError", err)
	}
}

// interrupted is an input whose user, at the question, presses the
// interrupt key and then types a line all the same.
type interrupted struct {
	t      *testing.T
	asking *menu.Asking
	cancel context.CancelFunc
}

func (i interrupted) Read(p []byte) (int, error) {
	if !i.asking.Waiting() {
		i.t.Error("no question waits while the answer is read")
	}
	i.cancel()
	return copy(p, "\n"), nil
}

func TestScreen_ACancelAtTheQuestionLeaves(t *testing.T) {
	var asking, unused menu.Asking
	var none *menu.Asking
	ctx, cancel := context.WithCancel(menu.WithAsking(t.Context(), &asking))
	var out strings.Builder
	s := &menu.Screen{In: interrupted{t, &asking, cancel}, Out: &out}
	if asking.Waiting() || none.Waiting() {
		t.Fatal("a question waits before one is asked")
	}
	answer, err := s.Ask(ctx, "Delete? [y/N]: ")
	if answer != "" || !errors.Is(err, menu.ErrLeft) || out.String() != "Delete? [y/N]: " || asking.Waiting() || unused.Waiting() {
		t.Fatalf("Ask = %q, %v; the screen got %q; a question still waits: %v", answer, err, out.String(), asking.Waiting())
	}
}

func TestTable_DropsColumnsUntilItFits(t *testing.T) {
	columns := []menu.Column{{Title: "Name"}, {Title: "Age", Drop: 1}, {Title: "Town", Drop: 2}, {Title: "Shoe", Drop: 2}}
	rows := [][]string{{"Åsa", "41", "Umeå", "38"}, {"日本", "7", "Kyoto", "30"}}
	full := "Index | Name | Age | Town  | Shoe\n" +
		"10    | Åsa  | 41  | Umeå  | 38\n" +
		"11    | 日本 | 7   | Kyoto | 30\n"
	for _, row := range []struct {
		cols int
		want string
	}{
		{0, full},
		{33, full},
		// Of two columns that drop alike the rightmost goes first.
		{32, "Index | Name | Age | Town\n10    | Åsa  | 41  | Umeå\n11    | 日本 | 7   | Kyoto\n"},
		{25, "Index | Name | Age\n10    | Åsa  | 41\n11    | 日本 | 7\n"},
		// A column that never drops stays, whole, however narrow the terminal.
		{17, "Index | Name\n10    | Åsa\n11    | 日本\n"},
		{3, "Index | Name\n10    | Åsa\n11    | 日本\n"},
	} {
		if got := menu.Table(columns, rows, 10, row.cols); got != row.want {
			t.Fatalf("at %d columns the table is\n%s\nwant\n%s", row.cols, got, row.want)
		}
	}
	if got := menu.Table(columns[:1], nil, 0, 80); got != "Index | Name\n" {
		t.Fatalf("a table without rows is %q", got)
	}
	if got := menu.Pad("日本", 6) + "|" + menu.Pad("long", 2) + "|"; got != "日本  |long|" {
		t.Fatalf("Pad gave %q", got)
	}
}
