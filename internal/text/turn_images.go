package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	imagegentool "github.com/baalimago/clai/internal/imagegen/tool"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

var errIncompleteImageTurn = errors.New("image turn is incomplete")

const imageProgressTitle = "generating image"

var _ TurnRunner = (*sessionRunner[models.StreamCompleter])(nil)

// imageWiring returns the querier's ImageTurn, or the default for its config dir (Run.1).
func (q *Querier[C]) imageWiring() *ImageTurn {
	if q.imageTurn != nil {
		return q.imageTurn
	}
	out := q.output()
	terminal := utils.IsTerminalWriter(out)
	store := asset.NewStore(chat.AssetsDir(q.configDir))
	return &ImageTurn{
		Store:      store,
		Normalizer: imagenorm.New(store),
		Presenter:  present.New(present.Options{Out: out, Terminal: terminal, Color: terminal && !table.NoColor() && !q.rawDisplay()}),
		Persister:  chat.NewPersister(q.configDir, store),
		Stderr:     os.Stderr,
	}
}

func (t *ImageTurn) check() error {
	var missing []string
	if t.Store == nil {
		missing = append(missing, "Store")
	}
	if t.Normalizer == nil {
		missing = append(missing, "Normalizer")
	}
	if t.Presenter == nil {
		missing = append(missing, "Presenter")
	}
	if t.Persister == nil {
		missing = append(missing, "Persister")
	}
	if t.Stderr == nil {
		missing = append(missing, "Stderr")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s unset", errIncompleteImageTurn, strings.Join(missing, ", "))
	}
	return nil
}

// storeImage returns img stored, as ImagePart{Asset, MIME, Origin}.
func storeImage(store asset.AssetStorer, img pub_models.ImagePart) (pub_models.ImagePart, error) {
	ref, err := store.Put(img.Data, img.MIME)
	if err != nil {
		return pub_models.ImagePart{}, err
	}
	return pub_models.ImagePart{Asset: ref, MIME: img.MIME, Origin: img.Origin}, nil
}

// storePending replaces every pending image of c with its stored form. Messages and
// parts shared with the caller are copied, never written (Run.2).
func storePending(store asset.AssetStorer, c *pub_models.Chat) error {
	var msgs []pub_models.Message
	for i, m := range c.Messages {
		var parts []pub_models.Part
		for j, p := range m.Parts {
			img, ok := p.(pub_models.ImagePart)
			if !ok || img.Asset != "" || len(img.Data) == 0 {
				continue
			}
			stored, err := storeImage(store, img)
			if err != nil {
				return fmt.Errorf("message %d part %d: %w", i, j, err)
			}
			if parts == nil {
				parts = slices.Clone(m.Parts)
			}
			parts[j] = stored
		}
		if parts == nil {
			continue
		}
		if msgs == nil {
			msgs = slices.Clone(c.Messages)
		}
		msgs[i].Parts = parts
	}
	if msgs != nil {
		c.Messages = msgs
	}
	return nil
}

// normalizeForModel returns c as the active route must receive it (Run.3, P12-3).
func (q *Querier[C]) normalizeForModel(c pub_models.Chat) (pub_models.Chat, error) {
	var caps models.ImageCapabilities
	if encoder, ok := any(q.Model).(models.ImageEncoder); ok {
		caps = encoder.ImageCapabilities()
	}
	normalized, err := q.imageWiring().Normalizer.Normalize(c, caps)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("normalize images: %w", q.nameRoute(err))
	}
	return normalized, nil
}

// nameRoute returns err with the vendor and model a refusal in it leaves empty
// filled in from the active route; the refusal itself is not modified.
func (q *Querier[C]) nameRoute(err error) error {
	var refusal *claierr.ImageUnsupportedError
	if !errors.As(err, &refusal) {
		return err
	}
	named := *refusal
	if named.Vendor == "" {
		named.Vendor = q.vendor
	}
	if named.Model == "" {
		named.Model = q.routeModel()
	}
	return &named
}

// chatForModel stores the session's pending images, then normalizes its chat (Run.2, Run.3).
func (q *Querier[C]) chatForModel(session *QuerySession) (pub_models.Chat, error) {
	if err := storePending(q.imageWiring().Store, &session.Chat); err != nil {
		return pub_models.Chat{}, fmt.Errorf("store pending images: %w", err)
	}
	return q.normalizeForModel(session.Chat)
}

// normalizingCounter counts the tokens of the chat the route receives (P12-3).
type normalizingCounter struct {
	models.StreamCompleter
	counter   models.InputTokenCounter
	normalize func(pub_models.Chat) (pub_models.Chat, error)
}

func (n normalizingCounter) CountInputTokens(ctx context.Context, c pub_models.Chat) (int, error) {
	normalized, err := n.normalize(c)
	if err != nil {
		return 0, err
	}
	return n.counter.CountInputTokens(ctx, normalized)
}

// budgetModel is the model the stoploss counts tokens with.
func (q *Querier[C]) budgetModel() models.StreamCompleter {
	counter, ok := any(q.Model).(models.InputTokenCounter)
	if !ok {
		return q.Model
	}
	return normalizingCounter{StreamCompleter: q.Model, counter: counter, normalize: q.normalizeForModel}
}

// receiveImage stores a generated image as it arrives, records it for the step's
// assistant message, then presents it (Run.4, Run.9, I7).
func (q *Querier[C]) receiveImage(session *QuerySession, ev models.ImageEvent) error {
	img := ev.Image
	img.Origin = pub_models.OriginGenerated
	stored, err := storeImage(q.imageWiring().Store, img)
	if err != nil {
		return fmt.Errorf("store generated image: %w", err)
	}
	session.addStepImage(stored)
	var took time.Duration
	if !q.imageProgressSince.IsZero() {
		took = time.Since(q.imageProgressSince)
	}
	details := present.DetailsOf(img.Data, q.imageOutputModel(), took)
	lineOpen := session.lineOpen()
	if err := q.presentImage(stored, "", details, lineOpen); err != nil {
		return err
	}
	if lineOpen {
		session.endRow()
	}
	return nil
}

// imageVendor names the built completer's vendor for route.ImageModelFor.
func (q *Querier[C]) imageVendor() route.ChatVendor {
	switch any(q.Model).(type) {
	case *openai.ChatGPT:
		return route.OpenAI
	case *gemini.Gemini:
		return route.Gemini
	case *openrouter.OpenRouter:
		return route.OpenRouter
	default:
		return route.OtherVendor
	}
}

// imageOutputModel names an image-output chat model as the user selects it.
func (q *Querier[C]) imageOutputModel() string {
	model := q.routeModel()
	if q.imageVendor() == route.OpenRouter && !strings.HasPrefix(model, "or:") {
		return "or:" + model
	}
	return model
}

// output is the querier's writer, stdout when none is set.
func (q *Querier[C]) output() io.Writer {
	if q.out == nil {
		return os.Stdout
	}
	return q.out
}

// imageProgress is the ImageTurn's Progress, or the querier's default (Run.1).
func (q *Querier[C]) imageProgress() ImageProgress {
	if q.imageTurn != nil {
		return q.imageTurn.Progress
	}
	if q.defaultImageProgress == nil {
		out := q.output()
		q.defaultImageProgress = board.NewStatus(out, utils.IsTerminalWriter(out), imageProgressTitle)
	}
	return q.defaultImageProgress
}

func (q *Querier[C]) showsImageProgress() bool {
	return q.imageProgress() != nil && !q.rawDisplay() && !q.debug && !q.structuredOutput
}

// startImageProgress shows the progress on a row of its own (Run.11).
func (q *Querier[C]) startImageProgress(session *QuerySession) error {
	if q.imageProgressSince.IsZero() {
		q.imageProgressSince = time.Now()
	}
	if !q.showsImageProgress() {
		return nil
	}
	if q.activityViewport == nil && session.lineOpen() {
		if _, err := fmt.Fprintln(q.output()); err != nil {
			return fmt.Errorf("end the text row before the image progress: %w", err)
		}
		session.endRow()
	}
	q.imageProgress().Start()
	return nil
}

func (q *Querier[C]) stopImageProgress() {
	if progress := q.imageProgress(); progress != nil {
		progress.Stop()
	}
}

// callRebus is a generate_image call's valid rebus, or "": a missing or invalid one is
// left off the image line and never fails the call (Run.5).
func callRebus(call pub_models.Call) string {
	if call.Name != imagegentool.Name || call.Inputs == nil {
		return ""
	}
	rebus, err := imagegentool.Rebus(*call.Inputs)
	if err != nil {
		return ""
	}
	return rebus
}

// progressGenerator shows the image progress while Generate runs, and records how each
// image was made for its presented line (Run.11, Run.5).
type progressGenerator struct {
	imagegen.ImageGenerator
	model       string
	start, stop func()
	record      func(handle string, details present.Details)
}

func (g progressGenerator) Generate(ctx context.Context, req imagegen.ImageRequest) ([]imagegen.GeneratedImage, error) {
	g.start()
	defer g.stop()
	began := time.Now()
	images, err := g.ImageGenerator.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	took := time.Since(began)
	for _, img := range images {
		if handle, herr := (pub_models.ImagePart{Data: img.Image.Data}).Handle(); herr == nil {
			g.record(handle, present.DetailsOf(img.Image.Data, g.model, took))
		}
	}
	return images, nil
}

// takeImageDetails returns and forgets the details generate_image recorded for img.
func (q *Querier[C]) takeImageDetails(img pub_models.ImagePart) present.Details {
	handle, err := img.Handle()
	if err != nil {
		return present.Details{}
	}
	details := q.imageDetails[handle]
	delete(q.imageDetails, handle)
	return details
}

func (q *Querier[C]) generatorsWithProgress(newGen imagegen.NewGeneratorFunc) imagegen.NewGeneratorFunc {
	start := func() {
		if q.showsImageProgress() {
			q.imageProgress().Start()
		}
	}
	record := func(handle string, details present.Details) {
		if q.imageDetails == nil {
			q.imageDetails = map[string]present.Details{}
		}
		q.imageDetails[handle] = details
	}
	return func(cfg imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
		gen, err := newGen(cfg)
		if err != nil {
			return nil, err
		}
		return progressGenerator{ImageGenerator: gen, model: cfg.Model, start: start, stop: q.stopImageProgress, record: record}, nil
	}
}

// presentImage shows a stored generated image on its own row (Run.4).
func (q *Querier[C]) presentImage(img pub_models.ImagePart, rebus string, details present.Details, lineOpen bool) error {
	if q.structuredOutput {
		return nil
	}
	shown, err := q.shownFor(img, rebus, details)
	if err != nil {
		return err
	}
	return q.presentShown(shown, lineOpen)
}

// presentShown writes shown's line on a row of its own. A failed opener is a notice on
// Stderr; any other failure is returned (Run.8, U2).
func (q *Querier[C]) presentShown(shown present.Shown, lineOpen bool) error {
	if err := q.freeRowForImage(lineOpen); err != nil {
		return fmt.Errorf("present image %s: %w", shown.Handle, err)
	}
	if err := q.openNotice(q.imageWiring().Presenter.Present(shown)); err != nil {
		return fmt.Errorf("present image %s: %w", shown.Handle, err)
	}
	if q.activityViewport != nil {
		if err := q.activityViewport.Render(q.out); err != nil {
			return fmt.Errorf("render activity viewport after image %s: %w", shown.Handle, err)
		}
	}
	return nil
}

// shownFor is how img is presented: its handle, its asset path, rebus and details.
func (q *Querier[C]) shownFor(img pub_models.ImagePart, rebus string, details present.Details) (present.Shown, error) {
	handle, err := img.Handle()
	if err != nil {
		return present.Shown{}, fmt.Errorf("present image: %w", err)
	}
	path, err := q.imageWiring().Store.Path(img.Asset)
	if err != nil {
		return present.Shown{}, fmt.Errorf("present image %s: %w", handle, err)
	}
	return present.Shown{Handle: handle, Path: path, Rebus: rebus, Details: details}, nil
}

// printImageToolBlock prints a tool result that holds generated images as a tool block
// that stays on screen, then opens the images (Run.5). The rolling window's rows stay
// where they are above it, and a new window starts below it.
func (q *Querier[C]) printImageToolBlock(call pub_models.Call, prefix string, parts []pub_models.Part, shown []present.Shown) error {
	presenter := q.imageWiring().Presenter
	content := make([]pub_models.Part, 0, len(parts))
	var rows []string
	next := 0
	for _, p := range parts {
		img, ok := p.(pub_models.ImagePart)
		if !ok || img.Origin != pub_models.OriginGenerated {
			content = append(content, p)
			continue
		}
		if next == len(shown) {
			return fmt.Errorf("generated image %s has no presentation", img.Describe())
		}
		s := shown[next]
		next++
		var after trailingRows
		content, after = takeSavedText(content, imagegentool.SavedText(s.Handle, s.Path))
		rows = append(rows, presenter.Rows(s)...)
		rows = append(rows, after...)
	}
	q.activityViewport = nil
	text := prefix + pub_models.Message{Parts: content}.Describe()
	if err := utils.PrintToolBlock(q.output(), call, text, rows, q.dims.Width, utils.ToolOutputRows()); err != nil {
		return err
	}
	q.line, q.lineCount = "", 0
	if q.usesActivityViewport() {
		q.ensureActivityViewport()
	}
	return q.openImages(shown)
}

// trailingRows follow an image's own rows in its tool block.
type trailingRows []string

// takeSavedText drops the last display part when it is text that starts with saved,
// and returns the lines after saved, sanitized, as trailing rows.
func takeSavedText(display []pub_models.Part, saved string) ([]pub_models.Part, trailingRows) {
	last := len(display) - 1
	if last < 0 {
		return display, nil
	}
	text, ok := display[last].(pub_models.TextPart)
	if !ok {
		return display, nil
	}
	rest, cut := strings.CutPrefix(text.Text, saved)
	if !cut {
		return display, nil
	}
	if rest = strings.TrimPrefix(rest, "\n"); rest == "" {
		return display[:last], nil
	}
	return display[:last], strings.Split(utils.SanitizeTerminalText(rest), "\n")
}

// openImages opens the images whose lines a tool output already shows.
func (q *Querier[C]) openImages(shown []present.Shown) error {
	for _, s := range shown {
		if err := q.openNotice(q.imageWiring().Presenter.Open(s)); err != nil {
			return fmt.Errorf("open image %s: %w", s.Handle, err)
		}
	}
	return nil
}

// openNotice turns a failed opener into one notice on Stderr (Run.8, U2).
func (q *Querier[C]) openNotice(err error) error {
	var openFailed *present.OpenFailedError
	if !errors.As(err, &openFailed) {
		return err
	}
	if _, werr := fmt.Fprintf(q.imageWiring().Stderr, "notice: %v\n", err); werr != nil {
		return fmt.Errorf("write open notice: %w", werr)
	}
	return nil
}

// freeRowForImage clears the rolling window, or ends an open text line, so the
// image line starts a row of its own.
func (q *Querier[C]) freeRowForImage(lineOpen bool) error {
	if q.activityViewport != nil {
		if rows := q.activityViewport.DetachRenderedRegion(); rows > 0 {
			return table.ClearTermTo(q.out, rows)
		}
		return nil
	}
	if !lineOpen || q.debug || q.structuredOutput {
		return nil
	}
	_, err := fmt.Fprintln(q.output())
	return err
}

// setupImageGeneration registers generate_image when the run selects it (Gen.1–3).
func (q *Querier[C]) setupImageGeneration() error {
	if !q.imageWiring().Generation.Requested {
		return nil
	}
	return q.registerGenerateImage()
}

// registerGenerateImage adds the client tool to the model and the run's toolset once.
func (q *Querier[C]) registerGenerateImage() error {
	turn := q.imageWiring()
	if turn.NewGenerator == nil {
		return ErrNoImageGenerator
	}
	tool := imagegentool.NewGenerateImageTool(turn.Generation.Config, q.generatorsWithProgress(turn.NewGenerator), turn.Store)
	name := tool.Specification().Name
	if _, ok := q.tooling.run[name]; ok {
		return nil
	}
	toolBox, ok := any(q.Model).(models.ToolBox)
	if !ok {
		return nil
	}
	if q.tooling.registered == nil {
		q.tooling.registered = map[string]struct{}{}
	}
	if _, ok := q.tooling.registered[name]; !ok {
		toolBox.RegisterTool(tool)
		q.tooling.registered[name] = struct{}{}
	}
	run := maps.Clone(q.tooling.run)
	if run == nil {
		run = map[string]pub_models.LLMTool{}
	}
	run[name] = tool
	q.tooling.run = run
	return nil
}

// partsCapture lets tools.InvokeWith run a PartsCaller and fold its error as for any
// tool, while keeping the parts.
type partsCapture struct {
	pub_models.PartsCaller
	parts []pub_models.Part
}

func (c *partsCapture) CallWithContext(ctx context.Context, in pub_models.Input) (string, error) {
	parts, err := c.CallParts(ctx, in)
	if err != nil {
		return "", err
	}
	c.parts = parts
	return pub_models.Message{Parts: parts}.Describe(), nil
}

func holdsImage(parts []pub_models.Part) bool {
	return slices.ContainsFunc(parts, func(p pub_models.Part) bool {
		_, ok := p.(pub_models.ImagePart)
		return ok
	})
}

func (q *Querier[C]) modelName() string {
	namer, ok := any(q.Model).(ModelNamer)
	if !ok {
		return ""
	}
	return namer.ModelName()
}

// routeModel names the active model as the cost manager does: the completer's
// own name, else the configured model version.
func (q *Querier[C]) routeModel() string {
	if name := strings.TrimSpace(q.modelName()); name != "" {
		return name
	}
	return q.modelVersion
}
