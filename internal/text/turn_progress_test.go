package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/vttest"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	progressTitle = "generating image"
	eraseRow      = "\r\x1b[2K"
)

// syncOut is the querier's output; the progress animation writes to it from its
// own goroutine.
type syncOut struct {
	mu sync.Mutex
	sb strings.Builder
}

func (s *syncOut) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sb.Write(p)
}

func (s *syncOut) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sb.String()
}

// progressFixture is a non-raw querier whose ImageTurn shows a live board.Status.
func progressFixture(t *testing.T, model models.StreamCompleter, raw bool) (*turnFixture, *syncOut) {
	t.Helper()
	f := newTurnFixture(t, model)
	out := &syncOut{}
	f.q.Raw = raw
	f.q.out = out
	turn := f.diskTurn(present.Options{Out: out})
	turn.Progress = board.NewStatus(out, true, progressTitle)
	return f, out
}

// assertProgressEndsBefore checks the status was drawn, and erased before marker
// with no frame after it.
func assertProgressEndsBefore(t *testing.T, out, marker string) {
	t.Helper()
	at := strings.Index(out, marker)
	if !strings.Contains(out, progressTitle) || at < 0 {
		t.Fatalf("want a %q frame and then %q, got %q", progressTitle, marker, out)
	}
	if last := strings.LastIndex(out, progressTitle); last > at {
		t.Fatalf("a frame was drawn after %q: %q", marker, out)
	}
	if erase := strings.LastIndex(out[:at], eraseRow); erase < strings.LastIndex(out[:at], progressTitle) {
		t.Fatalf("the status was not erased before %q: %q", marker, out)
	}
}

// Run.11: a native image in progress shows the status until the image arrives.
func TestTurn_ProgressUntilNativeImage(t *testing.T) {
	genPNG := pngOf(t, 60)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageProgressEvent{}, models.NoopEvent{}, models.ImageProgressEvent{}, models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f, out := progressFixture(t, model, false)

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	path := f.assetPath(genPNG, "png")
	assertProgressEndsBefore(t, out.String(), path)
	if strings.Contains(out.String(), "\n"+eraseRow) || strings.HasPrefix(out.String(), "\n") {
		t.Fatalf("the status must reuse the empty row, not open one: %q", out.String())
	}
}

// Run.11: the status is erased before streamed text is written.
func TestTurn_ProgressErasedBeforeText(t *testing.T) {
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageProgressEvent{}, "Here you go.", models.StopEvent{}},
	}}
	f, out := progressFixture(t, model, false)

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertProgressEndsBefore(t, out.String(), "Here you go.")
}

// Run.11: after text that left its row open, the status takes the next row, and the
// image line takes that same row.
func TestTurn_ProgressAfterOpenTextLine(t *testing.T) {
	genPNG := pngOf(t, 61)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{"Drawing:", models.ImageProgressEvent{}, models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f, out := progressFixture(t, model, false)

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	path := f.assetPath(genPNG, "png")
	assertProgressEndsBefore(t, got, path)
	if !strings.Contains(got, "Drawing:\n"+eraseRow) {
		t.Fatalf("want the status on the row after the text, got %q", got)
	}
	if strings.Contains(got, "Drawing:\n\n") || !strings.Contains(got, eraseRow+path+"\n") {
		t.Fatalf("want the image line on the status row with no blank row, got %q", got)
	}
}

// Run.11: raw display shows no status.
func TestTurn_NoProgressWhenRaw(t *testing.T) {
	genPNG := pngOf(t, 62)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageProgressEvent{}, models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f, out := progressFixture(t, model, true)

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), progressTitle) || strings.Contains(out.String(), eraseRow) {
		t.Fatalf("raw output carries the status: %q", out.String())
	}
}

// Run.11: an ImageTurn without Progress ignores ImageProgressEvent.
func TestTurn_ProgressEventWithoutProgress(t *testing.T) {
	output := func(events ...models.CompletionEvent) string {
		f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{events}})
		f.diskTurn(present.Options{})
		if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return f.out.String()
	}
	with, without := output(models.ImageProgressEvent{}, "ok", models.StopEvent{}), output("ok", models.StopEvent{})
	if with != without {
		t.Fatalf("output = %q, want %q as without the event", with, without)
	}
}

// Run.11: the client tool shows the status while its generator runs.
func TestTurn_ProgressWhileClientToolGenerates(t *testing.T) {
	genPNG := pngOf(t, 63)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a fox"}), models.StopEvent{}},
	}}
	f, out := progressFixture(t, model, false)
	var during string
	gen := pngGenerator(genPNG)
	gen.Refuse = func(imagegen.ImageRequest) error {
		during = out.String()
		return nil
	}
	turn := f.q.imageTurn
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw a fox"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := strings.LastIndex(during, eraseRow)
	if last < 0 || !strings.Contains(during[last:], progressTitle) {
		t.Fatalf("the status was not showing while Generate ran: %q", during)
	}
	assertProgressEndsBefore(t, out.String(), f.assetPath(genPNG, "png"))
}

// Run.5: the generate_image call's rebus is shown on the image line; an invalid one
// is left out and never fails the call.
func TestTurn_ClientToolRebus(t *testing.T) {
	for _, tc := range []struct {
		rebus any
		want  string
	}{
		{"🐕 🎾 🎨", "▸ [🐕 🎾 🎨] image "},
		{"a dog", "▸ image "},
		{nil, "▸ image "},
	} {
		in := pub_models.Input{"prompt": "a dog"}
		if tc.rebus != nil {
			in["rebus"] = tc.rebus
		}
		model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
			{call("c1", "generate_image", in), models.StopEvent{}},
		}}
		withTheme(t, false)
		f := newTurnFixture(t, model)
		f.q.Raw = false
		f.q.dims.Width, f.q.dims.Height = 200, 20
		turn := f.diskTurn(present.Options{Terminal: true, Color: true})
		turn.Generation = ImageGenSetup{Requested: true}
		turn.NewGenerator = pngGenerator(pngOf(t, 64)).New

		session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw a dog"})
		if err != nil {
			t.Fatalf("rebus %v: Run: %v", tc.rebus, err)
		}
		if msg := lastOfRole(t, session.Chat, "tool"); strings.Contains(msg.Describe(), "ERROR") {
			t.Fatalf("rebus %v: generate_image failed: %s", tc.rebus, msg.Describe())
		}
		plain := regexp.MustCompile(`\x1b\][^\x1b\a]*(?:\x1b\\|\a)|\x1b\[[0-9;]*m`).ReplaceAllString(f.out.String(), "")
		if !strings.Contains(plain, tc.want) {
			t.Fatalf("rebus %v: output %q lacks %q", tc.rebus, plain, tc.want)
		}
	}
}

// chanModel streams what the test sends, one event at a time, and has no tool box.
type chanModel struct{ events chan models.CompletionEvent }

func (m *chanModel) Setup() error { return nil }

func (m *chanModel) StreamCompletions(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
	return m.events, nil
}

// Run.11: an empty content chunk or a nil event writes nothing, so the row stays up
// until the image arrives.
func TestTurn_ProgressSurvivesEmptyChunks(t *testing.T) {
	model := &chanModel{events: make(chan models.CompletionEvent)}
	f, out := progressFixture(t, model, false)
	done := make(chan error, 1)
	go func() {
		_, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
		done <- err
	}()
	model.events <- models.ImageProgressEvent{}
	for _, ev := range []models.CompletionEvent{"", nil} {
		model.events <- ev
		// The runner reads this only after it has handled ev.
		model.events <- models.NoopEvent{}
		if got := out.String(); strings.HasSuffix(got, eraseRow) {
			t.Fatalf("%#v erased the progress row before the image: %q", ev, got)
		}
	}
	genPNG := pngOf(t, 65)
	model.events <- models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}
	model.events <- models.StopEvent{}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertProgressEndsBefore(t, out.String(), f.assetPath(genPNG, "png"))
}

// Run.4: structured output keeps stdout for the final response; the image is stored,
// not presented.
func TestTurn_StructuredOutputPresentsNothing(t *testing.T) {
	genPNG := pngOf(t, 66)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, `{"ok":true}`, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.structuredOutput = true
	f.diskTurn(present.Options{})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	path := f.assetPath(genPNG, "png")
	if path == "" || strings.Contains(f.out.String(), path) {
		t.Fatalf("asset %q, stdout %q: want the image stored and stdout without its line", path, f.out.String())
	}
}

// Run.5: a rebus input on another tool's generated image is not shown.
func TestTurn_RebusOnlyFromGenerateImage(t *testing.T) {
	draw := partsTool{name: "draw", parts: []pub_models.Part{pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: pngOf(t, 67)}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "draw", pub_models.Input{"rebus": "🐕"}), models.StopEvent{}},
	}}
	withTheme(t, false)
	f := newTurnFixture(t, model, draw)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	f.diskTurn(present.Options{Terminal: true, Color: true})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stripEscapes(f.out.String()); !strings.Contains(got, "  ▸ image ") || strings.Contains(got, "[🐕]") {
		t.Fatalf("output %q: want the image line without a rebus", got)
	}
}

// Querier.imageVendor names the completer actually built, not the model string.
func TestQuerier_imageVendor(t *testing.T) {
	for _, tc := range []struct {
		q    interface{ imageVendor() route.ChatVendor }
		want route.ChatVendor
	}{
		{&Querier[*openai.ChatGPT]{Model: &openai.ChatGPT{}}, route.OpenAI},
		{&Querier[*gemini.Gemini]{Model: &gemini.Gemini{}}, route.Gemini},
		{&Querier[*openrouter.OpenRouter]{Model: &openrouter.OpenRouter{}}, route.OpenRouter},
		{&Querier[*ollama.Ollama]{Model: &ollama.Ollama{}}, route.OtherVendor},
		{&Querier[models.StreamCompleter]{Model: &turnModel{}}, route.OtherVendor},
	} {
		if got := tc.q.imageVendor(); got != tc.want {
			t.Errorf("%T: imageVendor = %q, want %q", tc.q, got, tc.want)
		}
	}
	or := &Querier[*openrouter.OpenRouter]{Model: &openrouter.OpenRouter{}, modelVersion: "google/x-image"}
	other := &Querier[models.StreamCompleter]{Model: &turnModel{}, modelVersion: "google/x-image"}
	if or.imageOutputModel() != "or:google/x-image" || other.imageOutputModel() != "google/x-image" {
		t.Fatalf("imageOutputModel = %q, %q; want the or: prefix only on OpenRouter", or.imageOutputModel(), other.imageOutputModel())
	}
}

// Run.5: a generate_image image's line names the image model, its size, file size and
// how long the generation took.
func TestTurn_ClientToolDetails(t *testing.T) {
	genPNG := pngOf(t, 68)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot", "rebus": "⚫"}), models.StopEvent{}},
	}}
	withTheme(t, false)
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true, Color: true})
	turn.Generation = ImageGenSetup{Requested: true, Config: imagegen.GeneratorConfig{Model: "gpt-image-1"}}
	turn.NewGenerator = pngGenerator(genPNG).New

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw a dot"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := regexp.MustCompile(`  ▸ \[⚫\] image ` + handleOf(t, genPNG) + ` · gpt-image-1 · 1×1 \(1:1\) · \d+ B · (<1ms|\d+(\.\d+)?(ms|s))\n  → file://`)
	if plain := stripEscapes(f.out.String()); !want.MatchString(plain) {
		t.Fatalf("output %q does not match %s", plain, want)
	}
}

// Run.4: an image-output model's image names the chat model and the time since its
// progress began.
func TestTurn_NativeImageDetails(t *testing.T) {
	genPNG := pngOf(t, 69)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageProgressEvent{}, models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: genPNG}}, models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.modelVersion = "google/x-image"
	f.diskTurn(present.Options{Terminal: true, Color: true})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := regexp.MustCompile(`▸ image ` + handleOf(t, genPNG) + ` · google/x-image · 1×1 \(1:1\) · \d+ B · (<1ms|\d+(\.\d+)?(ms|s)) → file://`)
	if plain := stripEscapes(f.out.String()); !want.MatchString(plain) {
		t.Fatalf("output %q does not match %s", plain, want)
	}
}

func stripEscapes(s string) string {
	return regexp.MustCompile(`\x1b\][^\x1b\a]*(?:\x1b\\|\a)|\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
}

// withTheme loads a theme with rolling output on or off for the test.
func withTheme(t *testing.T, rolling bool) {
	t.Helper()
	confDir := withEmptyClaiConfigDir(t)
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	theme := fmt.Sprintf(`{"rollingOutput": {"enabled": %t, "windowCellHeight": 30}}`, rolling)
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(theme), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := testenv.Quiet(t)
	err := utils.LoadTheme(confDir)
	restore()
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	t.Cleanup(func() {
		if err := utils.LoadTheme(t.TempDir()); err != nil {
			t.Errorf("reset theme: %v", err)
		}
	})
}

// snapOpener records the output written before each open.
type snapOpener struct {
	out  *strings.Builder
	seen []string
}

func (o *snapOpener) Open(string) error {
	o.seen = append(o.seen, o.out.String())
	return nil
}

// Run.5: on a terminal, a tool's generated image is a permanent tool block whose rows
// replace generate_image's saved-at text and the image label. Later activity, the
// rolling window and a narrow terminal leave its rows on screen; the image opens after
// they are written.
func TestTurn_ToolImageBlock(t *testing.T) {
	var thinking strings.Builder
	for i := range 40 {
		fmt.Fprintf(&thinking, "thought %d\n", i)
	}
	for _, tc := range []struct {
		name    string
		rolling bool
		width   int
		later   string
	}{
		{"rolling", true, 300, thinking.String()},
		{"plain", false, 300, thinking.String()},
		{"rolling narrow", true, 40, thinking.String()},
		{"plain narrow", false, 40, thinking.String()},
		{"rolling short later activity", true, 300, "one\ntwo\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, tc.rolling)
			genPNG := pngOf(t, byte(72+tc.width%7+len(tc.name)))
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
				{models.ReasoningEvent{Content: "planning the dot\n"}, call("c1", "generate_image", pub_models.Input{"prompt": "a dot", "rebus": "⚫"}), models.StopEvent{}},
				{models.ReasoningEvent{Content: tc.later}, "done", models.StopEvent{}},
			}}
			f := newTurnFixture(t, model)
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = tc.width, 20
			opener := &snapOpener{out: f.out}
			turn := f.diskTurn(present.Options{Terminal: true, Color: true, AutoOpen: true, Opener: opener})
			turn.Generation = ImageGenSetup{Requested: true, Config: imagegen.GeneratorConfig{Model: "gpt-image-1"}}
			turn.NewGenerator = pngGenerator(genPNG).New

			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw a dot"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			h := handleOf(t, genPNG)
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			head := regexp.MustCompile(`(?m)^  ▸ \[⚫\] image ` + h + ` · gpt-image-1 · 1×1 \(1:1\) · \d+ B · \S+$`)
			url := "  → file://" + f.assetPath(genPNG, "png")
			if !head.MatchString(screen) || !strings.Contains(screen, url) || !strings.Contains(screen, "generate_image") {
				t.Fatalf("final screen lacks the tool block with both rows:\n%s", screen)
			}
			if strings.Contains(screen, "[image "+h+"]") || strings.Contains(screen, "saved at") {
				t.Fatalf("final screen still shows the label or the saved-at text:\n%s", screen)
			}
			if strings.Count(stripEscapes(f.out.String()), "image "+h+" ·") != 1 {
				t.Fatalf("the image line was written more than once: %q", stripEscapes(f.out.String()))
			}
			if len(opener.seen) != 1 || !strings.Contains(stripEscapes(opener.seen[0]), "→ file://") {
				t.Fatalf("opened %d times; want once, after the rows were written", len(opener.seen))
			}
			before, block := strings.Index(screen, "planning the dot"), strings.Index(screen, "  ▸ [⚫] image")
			if strings.Count(screen, "planning the dot") != 1 || before < 0 || before > block {
				t.Fatalf("the reasoning before the call must stay on screen once, above the block:\n%s", screen)
			}
		})
	}
}

// Run.5: prose the model wrote before an image tool call stays on screen above the
// block, and the answer after it follows below.
func TestTurn_ToolImageBlockKeepsProse(t *testing.T) {
	for _, rolling := range []bool{true, false} {
		t.Run(map[bool]string{true: "rolling", false: "plain"}[rolling], func(t *testing.T) {
			withTheme(t, rolling)
			genPNG := pngOf(t, map[bool]byte{true: 106, false: 107}[rolling])
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
				{"Sure, generating.", call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
				{"Here it is.", models.StopEvent{}},
			}}
			f := newTurnFixture(t, model)
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = 200, 20
			turn := f.diskTurn(present.Options{Terminal: true, Color: true})
			turn.Generation = ImageGenSetup{Requested: true}
			turn.NewGenerator = pngGenerator(genPNG).New
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			prose, block, answer := strings.Index(screen, "Sure, generating."), strings.Index(screen, "  ▸ image "+handleOf(t, genPNG)), strings.Index(screen, "Here it is.")
			if prose < 0 || block < 0 || answer < 0 || prose > block || block > answer || strings.Count(screen, "Sure, generating.") != 1 {
				t.Fatalf("want the prose, the block, then the answer, once each:\n%s", screen)
			}
			if !rolling && !strings.Contains(screen, "assistant: Here it is.") {
				t.Fatalf("plain mode must print the final answer as a message:\n%s", screen)
			}
		})
	}
}

// wordFailingWriter fails every write that holds word.
type wordFailingWriter struct {
	strings.Builder
	word string
}

func (w *wordFailingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.word) {
		return 0, errors.New("terminal gone")
	}
	return w.Builder.Write(p)
}

// Run.7: the tool result is in the chat before its block is written, so a failed write
// keeps the stored image for the failed-run persist.
func TestTurn_ToolImageBlockWriteFailureKeepsPart(t *testing.T) {
	withTheme(t, false)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	out := &wordFailingWriter{word: "generate_image"}
	f.q.Raw, f.q.out = false, out
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Out: out, Terminal: true, Color: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 108)).New
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if err == nil {
		t.Fatal("Run = nil, want the write error")
	}
	if last := session.Chat.Messages[len(session.Chat.Messages)-1]; last.Role != "tool" || !holdsImage(last.Parts) {
		t.Fatalf("last message %s: want the tool result with its stored image", last.Role)
	}
}

// pathFailsAfter is a store whose Path fails after its first ok calls.
type pathFailsAfter struct {
	asset.AssetStorer
	ok    int
	calls *int
}

func (s pathFailsAfter) Path(ref string) (string, error) {
	*s.calls++
	if *s.calls > s.ok {
		return "", errors.New("asset swept")
	}
	return s.AssetStorer.Path(ref)
}

// Run.4 and Run.5: structured output shows no tool block; a presentation failure ends
// the turn only after the tool result is in the chat.
func TestTurn_ToolImageBlockEdges(t *testing.T) {
	withTheme(t, false)
	genPNG := pngOf(t, 109)
	newModel := func() *turnModel {
		return &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
			{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), `{"ok":true}`, models.StopEvent{}},
		}}
	}
	f := newTurnFixture(t, newModel())
	f.q.Raw, f.q.structuredOutput = false, true
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true, Color: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(genPNG).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("structured: Run: %v", err)
	}
	if got := f.out.String(); strings.Contains(got, "image "+handleOf(t, genPNG)) || strings.Contains(got, "generate_image") {
		t.Fatalf("structured output shows the image: %q", got)
	}

	f = newTurnFixture(t, newModel())
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn = f.diskTurn(present.Options{Terminal: true, Color: true})
	calls := 0
	turn.Store = pathFailsAfter{AssetStorer: turn.Store, ok: 1, calls: &calls}
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(genPNG).New
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if err == nil {
		t.Fatal("Run = nil, want the presentation error")
	}
	for i, m := range session.Chat.Messages {
		if len(m.ToolCalls) == 0 {
			continue
		}
		if i+1 == len(session.Chat.Messages) || session.Chat.Messages[i+1].ToolCallID != m.ToolCalls[0].ID {
			t.Fatalf("err %v: tool call %s has no result after it", err, m.ToolCalls[0].ID)
		}
	}
}

// Run.5: each image's revised prompt follows its own rows, whole, even under a rune
// limit far below the tool's text.
func TestTurn_ToolImageBlockGroupsRevisedPrompts(t *testing.T) {
	withTheme(t, false)
	a, b := pngOf(t, 110), pngOf(t, 111)
	gen := &imagegentest.Fake{Images: []imagegen.GeneratedImage{
		{Image: asset.Image{Data: a, MIME: "image/png"}, RevisedPrompt: "a red dot"},
		{Image: asset.Image{Data: b, MIME: "image/png"}, RevisedPrompt: "a blue dot"},
	}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "two dots"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	f.q.tooling.outputRuneLimit = 30
	turn := f.diskTurn(present.Options{Terminal: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := regexp.MustCompile(`  ▸ image ` + handleOf(t, a) + `[^\n]*\n  → file://\S+\n  revised prompt: a red dot\n  ▸ image ` + handleOf(t, b) + `[^\n]*\n  → file://\S+\n  revised prompt: a blue dot\n`)
	if screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n"); !rows.MatchString(screen) || strings.Contains(screen, "too large") {
		t.Fatalf("final screen:\n%s\nwant each image's rows followed by its revised prompt", screen)
	}
}

// Run.5: every generated image of one call keeps both rows, with a revised prompt and a
// rune limit far below the rows' length.
func TestTurn_ToolImageBlockManyImages(t *testing.T) {
	withTheme(t, false)
	var parts []pub_models.Part
	var data [][]byte
	for i := range 4 {
		png := pngOf(t, byte(90+i))
		data = append(data, png)
		parts = append(parts, pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: png})
	}
	parts = append([]pub_models.Part{pub_models.TextPart{Text: "revised prompt: four dots"}}, parts...)
	draw := partsTool{name: "draw", parts: parts}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "draw", pub_models.Input{}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, draw)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 120, 20
	f.q.tooling.outputRuneLimit = 40
	f.diskTurn(present.Options{Terminal: true, Color: true})

	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
	for _, png := range data {
		if !strings.Contains(screen, "  ▸ image "+handleOf(t, png)) || !strings.Contains(screen, "  → file://"+f.assetPath(png, "png")) {
			t.Fatalf("image %s lost a row:\n%s", handleOf(t, png), screen)
		}
	}
	if !strings.Contains(screen, "revised prompt: four dots") {
		t.Fatalf("the tool's other text is gone:\n%s", screen)
	}
}

// Run.5: with debug on, the activity block is not shown, so the image gets its own line.
func TestTurn_ToolImageDebugOwnLine(t *testing.T) {
	genPNG := pngOf(t, 98)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot", "rebus": "⚫"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw, f.q.debug = false, true
	turn := f.diskTurn(present.Options{Terminal: true, Color: true})
	turn.Generation = ImageGenSetup{Requested: true, Config: imagegen.GeneratorConfig{Model: "gpt-image-1"}}
	turn.NewGenerator = pngGenerator(genPNG).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if own := regexp.MustCompile(`▸ \[⚫\] image ` + handleOf(t, genPNG) + ` · gpt-image-1[^\n]* → file://`); !own.MatchString(stripEscapes(f.out.String())) {
		t.Fatalf("debug output %q lacks the image's own line", f.out.String())
	}
}

// Run.4: every image of a step counts from the step's first ImageProgressEvent; a new
// step starts without one.
func TestTurn_NativeImageTimes(t *testing.T) {
	first, second, third := pngOf(t, 101), pngOf(t, 102), pngOf(t, 103)
	image := func(data []byte) models.ImageEvent {
		return models.ImageEvent{Image: pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: data}}
	}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{models.ImageProgressEvent{}, image(first), image(second), call("c1", "ls", nil), models.StopEvent{}},
		{image(third), models.StopEvent{}},
	}}
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	f := newTurnFixture(t, model, ls)
	f.diskTurn(present.Options{Terminal: true, Color: true})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	plain := stripEscapes(f.out.String())
	timed := regexp.MustCompile(` · (<1ms|\d+(\.\d+)?(ms|s)) → `)
	for i, data := range [][]byte{first, second, third} {
		line := regexp.MustCompile(`▸ image ` + handleOf(t, data) + `[^\n]*`).FindString(plain)
		if want := i < 2; timed.MatchString(line) != want {
			t.Errorf("image %d line %q: timed = %v, want %v", i+1, line, !want, want)
		}
	}
}

// Run.5: details recorded for an image that is never shown do not outlive the call.
func TestTurn_ImageDetailsCleared(t *testing.T) {
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	turn := f.diskTurn(present.Options{})
	turn.Store = failingPutStore{err: errors.New("disk full")}
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 104)).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.q.imageDetails) != 0 {
		t.Fatalf("imageDetails = %v after the call, want none", f.q.imageDetails)
	}
}

// Run.5: generate_image's revised prompt follows the image's rows when its saved-at
// line goes, with no blank row between.
func TestTurn_ToolImageBlockKeepsRevisedPrompt(t *testing.T) {
	withTheme(t, false)
	genPNG := pngOf(t, 105)
	gen := pngGenerator(genPNG)
	gen.Images[0].RevisedPrompt = "a calm black dot"
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true, Color: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
	rows := regexp.MustCompile(`\n  ▸ image ` + handleOf(t, genPNG) + `[^\n]*\n  → file://\S+\n  revised prompt: a calm black dot\n`)
	if !rows.MatchString(screen) || strings.Contains(screen, "saved at") {
		t.Fatalf("final screen:\n%s\nwant the image rows, then the revised prompt, without the saved-at line", screen)
	}
}

// Run.5: the stoploss prefix starts the block's first text row, so the error mark sits
// on the row that holds the error.
func TestTurn_ToolImageBlockPrefixRow(t *testing.T) {
	withTheme(t, false)
	f := newTurnFixture(t, &turnModel{caps: nativeCaps()})
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true})
	data := pngOf(t, 112)
	ref, err := turn.Store.Put(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	img := pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginGenerated}
	c := call("c1", "draw", pub_models.Input{})
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "assistant", ToolCalls: []pub_models.Call{c}}}}}
	parts := []pub_models.Part{pub_models.TextPart{Text: "ERROR: half drawn"}, img}
	exec := toolExecutor[models.StreamCompleter]{querier: f.q}
	if err := exec.emitToolResultParts(t.Context(), session, c, "[ Tool calls remaining: 4 ] ", pub_models.Message{Parts: parts}.Describe(), parts); err != nil {
		t.Fatalf("emitToolResultParts: %v", err)
	}
	if got := stripEscapes(f.out.String()); !strings.Contains(got, "\n  ✗ [ Tool calls remaining: 4 ] ERROR: half drawn\n  ▸ image "+handleOf(t, data)) {
		t.Fatalf("output %q: want the prefix and the error on the first, marked row", got)
	}
}

// Run.5: a vendor's revised prompt reaches the terminal without escape sequences.
func TestTurn_ToolImageBlockSanitizesRevisedPrompt(t *testing.T) {
	withTheme(t, false)
	genPNG := pngOf(t, 113)
	gen := pngGenerator(genPNG)
	gen.Images[0].RevisedPrompt = "calm\x1b[2J\x1b]52;c;aGk=\x07dot"
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = gen.New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.out.String(); strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b]52") || !strings.Contains(got, "revised prompt: calm") {
		t.Fatalf("output %q: want the revised prompt without its escape sequences", got)
	}
}

// Run.12: a last step with no text leaves what earlier steps showed alone, so the
// image block's rows and the prose before the call each stay on screen once, and the
// final answer is still logged.
func TestTurn_EmptyLastStepKeepsScreen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rolling bool
		tool    string
	}{{"rolling image", true, "generate_image"}, {"plain image", false, "generate_image"}, {"plain ls", false, "ls"}} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, tc.rolling)
			genPNG := pngOf(t, byte(114+len(tc.name)))
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
				{"Sure, line one.\nLine two.\nLine three.", call("c1", tc.tool, pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
				{models.StopEvent{}},
			}}
			ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt\nb.txt"}}}
			f := newTurnFixture(t, model, ls)
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = 200, 20
			logs := &captureHandler{}
			f.q.agentSettings = &AgentSettings{Logger: slog.New(logs)}
			turn := f.diskTurn(present.Options{Terminal: true})
			turn.Generation = ImageGenSetup{Requested: true}
			turn.NewGenerator = pngGenerator(genPNG).New
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			logged := false
			for _, r := range logs.records {
				logged = logged || recordAttrs(r)["kind"] == "final_answer"
			}
			if !logged {
				t.Fatal("the final answer was not logged")
			}
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			if strings.Count(screen, "Sure, line one.") != 1 || strings.Count(screen, "Line three.") != 1 {
				t.Fatalf("the prose does not show exactly once:\n%s", screen)
			}
			last := "  b.txt"
			if tc.tool == "generate_image" {
				last = "  → file://" + f.assetPath(genPNG, "png")
			}
			if !strings.Contains(screen, last) {
				t.Fatalf("the tool block lost its last row %q:\n%s", last, screen)
			}
		})
	}
}

// Run.8: on the block path a failed opener is one notice on stderr and never fails the
// turn.
func TestTurn_ToolImageBlockOpenFails(t *testing.T) {
	withTheme(t, false)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true, AutoOpen: true, Opener: &turnOpener{err: errors.New("no display")}})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 120)).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"}); err != nil {
		t.Fatalf("Run: %v; a failed opener must not fail the turn", err)
	}
	if n := strings.Count(f.stderr.String(), "notice: "); n != 1 {
		t.Fatalf("stderr %q: want one notice", f.stderr.String())
	}
}

// assertEveryCallAnswered fails when a tool call in the chat has no result.
func assertEveryCallAnswered(t *testing.T, msgs []pub_models.Message) {
	t.Helper()
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" {
			answered[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				t.Fatalf("tool call %s has no result", c.ID)
			}
		}
	}
}

// Run.5: a generated image the turn cannot store makes that call's result an ERROR
// text, so the call keeps its result, and the turn fails with the store error.
func TestTurn_ToolImageStoreFailureIsToolError(t *testing.T) {
	draw := partsTool{name: "draw", parts: []pub_models.Part{pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: pngOf(t, 121)}}}
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "draw", pub_models.Input{}), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, draw, ls)
	f.q.tooling.maxCalls = new(5)
	turn := f.diskTurn(present.Options{})
	turn.Store = failingPutStore{err: errors.New("disk full")}
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run = %v, want the store error", err)
	}
	assertEveryCallAnswered(t, session.Chat.Messages)
	var drawResult string
	for _, m := range session.Chat.Messages {
		if m.ToolCallID == "c1" {
			drawResult = m.Describe()
		}
	}
	if !strings.HasPrefix(drawResult, "[ Tool calls remaining") || !strings.Contains(drawResult, "ERROR:") || !strings.Contains(drawResult, "disk full") {
		t.Fatalf("draw result = %q, want the budget prefix, then an ERROR naming the store failure", drawResult)
	}
}

// Run.5: when showing one call's images fails, the other calls of the batch still get
// their results, and the turn returns the display error.
func TestTurn_ToolImageBlockFailureFinishesBatch(t *testing.T) {
	withTheme(t, false)
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, ls)
	out := &wordFailingWriter{word: "generate_image"}
	f.q.Raw, f.q.out = false, out
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Out: out, Terminal: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 122)).New
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw and list"})
	if err == nil {
		t.Fatal("Run = nil, want the display error")
	}
	assertEveryCallAnswered(t, session.Chat.Messages)
}

// Run.5: after the block a new rolling window replaces the one whose rows stay above it.
func TestPrintImageToolBlockStartsNewWindow(t *testing.T) {
	withTheme(t, true)
	f := newTurnFixture(t, &turnModel{caps: nativeCaps()})
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Terminal: true})
	ref, err := turn.Store.Put(pngOf(t, 123), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	img := pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginGenerated}
	shown, err := f.q.shownFor(img, "", present.Details{})
	if err != nil {
		t.Fatal(err)
	}
	old := f.q.ensureActivityViewport()
	f.q.line, f.q.lineCount = "stale row", 3
	if err := f.q.printImageToolBlock(call("c1", "draw", pub_models.Input{}), "", []pub_models.Part{img}, []present.Shown{shown}); err != nil {
		t.Fatalf("printImageToolBlock: %v", err)
	}
	if f.q.activityViewport == nil || f.q.activityViewport == old {
		t.Fatal("want a new rolling window after the block")
	}
	if f.q.line != "" || f.q.lineCount != 0 {
		t.Fatalf("line counters = %q, %d; want them reset after the block", f.q.line, f.q.lineCount)
	}
}

// Run.5: a raw display's failure to present an image still lets the batch's other calls
// record their results.
func TestTurn_RawImageFailureFinishesBatch(t *testing.T) {
	draw := partsTool{name: "draw", parts: []pub_models.Part{pub_models.ImagePart{Origin: pub_models.OriginGenerated, MIME: "image/png", Data: pngOf(t, 124)}}}
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "draw", pub_models.Input{}), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, draw, ls)
	turn := f.diskTurn(present.Options{})
	calls := 0
	turn.Store = pathFailsAfter{AssetStorer: turn.Store, ok: 0, calls: &calls}
	session, err := f.run(t, pub_models.Message{Role: "user", Content: "draw and list"})
	if err == nil {
		t.Fatal("Run = nil, want the presentation error")
	}
	assertEveryCallAnswered(t, session.Chat.Messages)
}

// Run.12: in debug with a raw display nothing streams, so the prose before a tool call
// is printed at the end when the last step wrote no text.
func TestTurn_DebugRawPrintsProse(t *testing.T) {
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{"Listing the files.", call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		{models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, ls)
	f.q.debug = true
	f.diskTurn(present.Options{})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(f.out.String(), "Listing the files.") {
		t.Fatalf("output %q: want the prose printed", f.out.String())
	}
}

// Run.12: with rolling output, an answer that ends its stream with io.EOF (as Anthropic
// does) prints in full, after a tool block and in a text-only run with reasoning.
func TestTurn_EOFAnswerPrintsInFull(t *testing.T) {
	var answer strings.Builder
	for i := range 40 {
		fmt.Fprintf(&answer, "answer line %d\n", i)
	}
	for _, tc := range []struct {
		name  string
		steps [][]models.CompletionEvent
	}{
		{"after image block", [][]models.CompletionEvent{
			{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
			{answer.String(), io.EOF},
		}},
		{"text only", [][]models.CompletionEvent{
			{models.ReasoningEvent{Content: "thinking\n"}, answer.String(), io.EOF},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, true)
			f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: tc.steps})
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = 200, 20
			turn := f.diskTurn(present.Options{Terminal: true})
			turn.Generation = ImageGenSetup{Requested: true}
			turn.NewGenerator = pngGenerator(pngOf(t, 125)).New
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "explain"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			if !strings.Contains(screen, "answer line 0") || !strings.Contains(screen, "answer line 39") {
				t.Fatalf("the answer was cut:\n%s", screen)
			}
		})
	}
}

// Run.12: in a plain display, neither the next prose's nor the final answer's
// clear-and-reprint reaches into a tool block at a narrow width; each block keeps its
// closing blank row.
func TestTurn_PlainToolThenAnswerKeepsBlock(t *testing.T) {
	for _, tool := range []string{"ls", "generate_image"} {
		t.Run(tool, func(t *testing.T) {
			withTheme(t, false)
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
				{strings.Repeat("x", 35), call("c1", tool, pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
				{strings.Repeat("y", 30) + "\n" + strings.Repeat("z", 30), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
				{strings.Repeat("w", 15), models.StopEvent{}},
			}}
			ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt\nlast.txt"}}}
			f := newTurnFixture(t, model, ls)
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = 40, 20
			turn := f.diskTurn(present.Options{Terminal: true})
			turn.Generation = ImageGenSetup{Requested: true}
			turn.NewGenerator = pngGenerator(pngOf(t, 126+byte(len(tool)))).New
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			screen := finalScreen(t, f.out.String(), f.q.dims.Width)
			blocks := 0
			for i, row := range screen {
				if row != "  last.txt" && !strings.HasPrefix(row, "  → file://") {
					continue
				}
				blocks++
				if i+1 >= len(screen) || screen[i+1] != "" {
					t.Fatalf("the block ending at row %d lost its blank row:\n%s", i, strings.Join(screen, "\n"))
				}
			}
			if blocks != 2 || !slices.Contains(screen, strings.Repeat("z", 30)) {
				t.Fatalf("want both blocks and the prose on screen:\n%s", strings.Join(screen, "\n"))
			}
		})
	}
}

// Run.12: a step whose every call is handed back ends the turn without another request,
// as a step that answers does: its prose and reasoning are the final answer, printed
// once in debug, plain and rolling display, and recorded as a reply. A step that hands
// back no prose keeps the answer shown before.
func TestTurn_ReturnToUserEndsTurn(t *testing.T) {
	var long strings.Builder
	for i := range 40 {
		fmt.Fprintf(&long, "answer line %d\n", i)
	}
	for _, tc := range []struct {
		name           string
		debug, rolling bool
		handedBack     string
		want           []string
	}{
		{"debug", true, false, "Here is the answer.", []string{"Here is the answer."}},
		{"plain", false, false, "Here is the answer.", []string{"Here is the answer."}},
		{"rolling", false, true, long.String(), []string{"answer line 0", "answer line 39"}},
		{"no prose", false, false, "", []string{"Listing the files."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, tc.rolling)
			ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
			second := []models.CompletionEvent{models.ReasoningEvent{Content: "why"}}
			if tc.handedBack != "" {
				second = append(second, tc.handedBack)
			}
			second = append(second, call("c2", "ls", pub_models.Input{}), models.StopEvent{})
			model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
				{"Listing the files.", geminiSignedCall("c1"), models.StopEvent{}},
				second,
			}}
			f := newTurnFixture(t, model, ls)
			f.q.Raw, f.q.debug = false, tc.debug
			f.q.dims.Width, f.q.dims.Height = 100, 20
			logs := &captureHandler{}
			f.q.agentSettings = &AgentSettings{Logger: slog.New(logs)}
			f.diskTurn(present.Options{})
			session, err := f.run(t, pub_models.Message{Role: "user", Content: "list"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if n := len(model.received()); n != 2 {
				t.Fatalf("%d requests, want 2: a handed-back step ends the turn", n)
			}
			final := tc.handedBack
			if final == "" {
				final = "Listing the files."
			}
			if !strings.HasSuffix(session.FinalAssistantText, final) {
				t.Fatalf("final answer %q, want %q", session.FinalAssistantText, final)
			}
			if tc.handedBack != "" && !tc.debug && session.FinalReasoningText != "why" {
				t.Fatalf("final reasoning %q, want the handed-back step's", session.FinalReasoningText)
			}
			last := session.CompletedCalls[len(session.CompletedCalls)-1]
			if !last.EndedWithReply || last.EndedWithTool {
				t.Fatalf("last call recorded as reply=%v tool=%v, want a reply", last.EndedWithReply, last.EndedWithTool)
			}
			logged := false
			for _, r := range logs.records {
				logged = logged || recordAttrs(r)["kind"] == "final_answer"
			}
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			for _, w := range tc.want {
				if !logged || strings.Count(screen, w) != 1 {
					t.Fatalf("logged %v; want %q on screen once:\n%s", logged, w, screen)
				}
			}
			answers := 0
			for _, m := range f.promoted(t).Messages {
				for _, c := range m.ToolCalls {
					if c.ID == "c2" {
						t.Fatalf("the handed-back call was persisted: %+v", m)
					}
				}
				if m.Role == "assistant" && strings.HasSuffix(m.Content, final) {
					answers++
				}
			}
			if answers != 1 {
				t.Fatalf("the final answer is persisted %d times, want once", answers)
			}
		})
	}
}

// Run.12: a turn whose step hands every call back with no answer at all fails with
// HandedBackError naming the calls, instead of ending silently.
func TestTurn_HandedBackWithoutAnswerFails(t *testing.T) {
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	for _, tc := range []struct {
		name  string
		steps [][]models.CompletionEvent
	}{
		{"first step", [][]models.CompletionEvent{
			{call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		}},
		{"after a tool", [][]models.CompletionEvent{
			{geminiSignedCall("c0"), models.StopEvent{}},
			{call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: tc.steps}, ls)
			f.q.isLikelyGemini3Preview = true
			f.diskTurn(present.Options{})
			_, err := f.run(t, pub_models.Message{Role: "user", Content: "list"})
			var handedBack *HandedBackError
			if !errors.As(err, &handedBack) || !slices.Equal(handedBack.Calls, []string{"ls"}) {
				t.Fatalf("Run = %v, want HandedBackError naming ls", err)
			}
		})
	}
}

// Run.12: debug mode streams nothing, so neither the prose before a call nor the final
// answer clears rows when printed.
func TestTurn_DebugTerminalClearsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps [][]models.CompletionEvent
	}{
		{"final answer", [][]models.CompletionEvent{
			{"First prose.", call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
			{"A1\nA2\nA3", models.StopEvent{}},
		}},
		{"prose before call", [][]models.CompletionEvent{
			{"First prose.", call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
			{"L1\nL2\nL3", call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
			{models.StopEvent{}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, false)
			ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
			f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: tc.steps}, ls)
			f.q.Raw, f.q.debug = false, true
			f.q.dims.Width, f.q.dims.Height = 40, 20
			f.diskTurn(present.Options{})
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "list"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
			if !strings.Contains(screen, "First prose.") {
				t.Fatalf("the earlier prose was cleared:\n%s", screen)
			}
		})
	}
}

// Run.5: a later call's hard failure keeps the earlier image failure, in call order.
func TestTurn_BatchJoinsImageThenHardError(t *testing.T) {
	withTheme(t, false)
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model, ls)
	f.q.tooling.maxCalls = new(5)
	out := &wordFailingWriter{word: "▸"}
	f.q.Raw, f.q.out = false, out
	f.q.dims.Width, f.q.dims.Height = 200, 20
	turn := f.diskTurn(present.Options{Out: out, Terminal: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 127)).New
	_, err := f.run(t, pub_models.Message{Role: "user", Content: "draw and list"})
	var imageErr *toolImageError
	if !errors.As(err, &imageErr) {
		t.Fatalf("Run = %v, want the image failure kept", err)
	}
	msg := fmt.Sprint(err)
	img, hard := strings.Index(msg, "images generated by generate_image"), strings.Index(msg, "print tool activity")
	if img < 0 || hard < img {
		t.Fatalf("Run = %q, want the image failure, then the ls failure", msg)
	}
}

func geminiSignedCall(id string) pub_models.Call {
	c := call(id, "ls", pub_models.Input{})
	c.ExtraContent = map[string]any{"google": map[string]any{"thought_signature": "s"}}
	return c
}

// Run.12: when a batch hands its prose back but also runs a call, the prose is shown
// once, before the call's block, and the block stays whole.
func TestTurn_MixedReturnToUserBatchKeepsBlock(t *testing.T) {
	prose := "L1 handed back\nL2 handed back\nL3 handed back"
	for _, tc := range []struct {
		name  string
		steps [][]models.CompletionEvent
	}{
		{"first step", [][]models.CompletionEvent{
			{prose, geminiSignedCall("c1"), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
			{models.StopEvent{}},
		}},
		{"after shown prose", [][]models.CompletionEvent{
			{"Intro prose.", geminiSignedCall("c0"), models.StopEvent{}},
			{prose, geminiSignedCall("c1"), call("c2", "ls", pub_models.Input{}), models.StopEvent{}},
			{models.StopEvent{}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, false)
			ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt\nlast.txt"}}}
			f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: tc.steps}, ls)
			f.q.Raw = false
			f.q.dims.Width, f.q.dims.Height = 60, 30
			f.diskTurn(present.Options{Terminal: true})
			if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			screen := finalScreen(t, f.out.String(), f.q.dims.Width)
			blocks := 0
			for i, row := range screen {
				if row != "  last.txt" {
					continue
				}
				blocks++
				if i+1 < len(screen) && screen[i+1] != "" {
					t.Fatalf("the block ending at row %d lost its blank row:\n%s", i, strings.Join(screen, "\n"))
				}
			}
			joined := strings.Join(screen, "\n")
			if strings.Index(joined, "L3 handed back") > strings.LastIndex(joined, "▸ ls") {
				t.Fatalf("the prose follows the block:\n%s", joined)
			}
			if blocks != len(tc.steps)-1 || strings.Count(joined, "L1 handed back") != 1 || strings.Count(joined, "L3 handed back") != 1 {
				t.Fatalf("want %d whole blocks and the prose once:\n%s", len(tc.steps)-1, joined)
			}
		})
	}
}

// Run.12: in debug with a terminal display the prose before a call is printed once,
// not again when the last step writes no text.
func TestTurn_DebugTerminalPrintsProseOnce(t *testing.T) {
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{"Listing the files.", call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		{models.StopEvent{}},
	}}, ls)
	f.q.Raw, f.q.debug = false, true
	f.q.dims.Width, f.q.dims.Height = 200, 20
	f.diskTurn(present.Options{})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := strings.Count(f.out.String(), "Listing the files."); n != 1 {
		t.Fatalf("prose printed %d times, want 1: %q", n, f.out.String())
	}
}

// Run.12: the final answer's clear-and-reprint never reaches into an image block at a
// narrow width.
func TestTurn_ImageBlockThenAnswerNarrow(t *testing.T) {
	withTheme(t, false)
	model := &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{strings.Repeat("x", 35), call("c1", "generate_image", pub_models.Input{"prompt": "a dot"}), models.StopEvent{}},
		{strings.Repeat("w", 15), models.StopEvent{}},
	}}
	f := newTurnFixture(t, model)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 40, 20
	turn := f.diskTurn(present.Options{Terminal: true})
	turn.Generation = ImageGenSetup{Requested: true}
	turn.NewGenerator = pngGenerator(pngOf(t, 170)).New
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	screen := finalScreen(t, f.out.String(), f.q.dims.Width)
	blocks := 0
	for i, row := range screen {
		if strings.HasPrefix(row, "  → file://") {
			blocks++
			if i+1 >= len(screen) || screen[i+1] != "" {
				t.Fatalf("the block lost its blank row:\n%s", strings.Join(screen, "\n"))
			}
		}
	}
	if blocks != 1 {
		t.Fatalf("%d image blocks on screen, want 1:\n%s", blocks, strings.Join(screen, "\n"))
	}
}

// Run.12: streamed prose of wide runes is cleared by the rows it took, so its reprint
// before a tool block leaves no stale row.
func TestTurn_WideProseClearsWhatItTook(t *testing.T) {
	withTheme(t, false)
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{strings.Repeat("漢", 30) + "END", call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		{models.StopEvent{}},
	}}, ls)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 40, 30
	f.diskTurn(present.Options{Terminal: true})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	screen := strings.Join(finalScreen(t, f.out.String(), f.q.dims.Width), "\n")
	if n := strings.Count(screen, "漢"); n != 30 {
		t.Fatalf("%d wide runes on screen, want the prose's 30 once:\n%s", n, screen)
	}
}

// Streamed prose holding emoji is cleared by the rows it took on a terminal that
// draws emoji one cell wide, so the clear before a tool block never reaches the
// command line above it.
func TestTurn_EmojiProseClearKeepsTheCommandLine(t *testing.T) {
	withTheme(t, false)
	const command = "~/work $ clai -t generate_image q 'A minimal orange triangle'"
	// 72 cells drawn narrow, 75 counted wide: three rows or four.
	prose := strings.Repeat("a", 69) + "🔶🌌📐" + strings.Repeat("b", 144)
	ls := partsTool{name: "ls", parts: []pub_models.Part{pub_models.TextPart{Text: "a.txt"}}}
	f := newTurnFixture(t, &turnModel{caps: nativeCaps(), steps: [][]models.CompletionEvent{
		{prose, call("c1", "ls", pub_models.Input{}), models.StopEvent{}},
		{models.StopEvent{}},
	}}, ls)
	f.q.Raw = false
	f.q.dims.Width, f.q.dims.Height = 72, 30
	f.diskTurn(present.Options{Terminal: true})
	if _, err := f.run(t, pub_models.Message{Role: "user", Content: "go"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, narrow := range []bool{false, true} {
		screen := replayScreen(t, &vttest.Terminal{Width: 72, NarrowEmoji: narrow}, command+"\n"+f.out.String())
		if len(screen) == 0 || screen[0] != command {
			t.Fatalf("narrow emoji %v: the command line is gone:\n%s", narrow, strings.Join(screen, "\n"))
		}
	}
}
