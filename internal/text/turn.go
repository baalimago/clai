package text

import (
	"context"
	"errors"
	"io"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/present"
)

type ImageGenSetup struct {
	Requested bool                     // generate_image is in the run's tool selection
	Config    imagegen.GeneratorConfig // Model (see query.2) and ConfigDir
}

// ImageProgress shows that an image is being generated (board.Status).
type ImageProgress interface {
	// Start shows the progress; starting it while shown keeps it running.
	Start()
	// Stop erases the progress and leaves the cursor at the start of its row.
	Stop()
}

// ImageTurn is the turn's image wiring. text.Configurations gains
//
//	ImageTurn *ImageTurn `json:"-"`
type ImageTurn struct {
	Store        asset.AssetStorer
	Normalizer   imagenorm.Normalizer
	Presenter    present.Presenter
	Persister    chat.Persister
	Stderr       io.Writer
	Generation   ImageGenSetup
	NewGenerator imagegen.NewGeneratorFunc
	Progress     ImageProgress // nil shows no progress
}

type TurnRunner interface {
	// Run executes one query turn: model steps and tool steps until stop.
	//  1. A nil ImageTurn, as pkg/agent and pkg/text build, is replaced by the default
	//     ImageTurn for the querier's config dir: Store = asset.NewStore at the README
	//     asset directory under configDir; Normalizer = imagenorm.New(Store); Presenter on
	//     the querier's output, with Terminal = utils.IsTerminalWriter(out), Color =
	//     Terminal unless NO_COLOR is truthy or raw mode is on, and AutoOpen false; Persister = chat.NewPersister(configDir,
	//     Store); Stderr = os.Stderr; Generation not requested; NewGenerator nil;
	//     Progress = board.NewStatus(out, Terminal, "generating image"), one per querier. Images
	//     entering such a run (user parts, MCP results, OpenRouter output) therefore
	//     work like any other. A text-only chat runs exactly as today (I10).
	//  2. When a model step begins, every pending ImagePart in session.Chat is stored
	//     with Store.Put and replaced by its stored form, so every handle the model sees
	//     resolves (I6, I1). A Put failure here ends the turn before the request.
	//  3. Every vendor call that carries the chat (StreamCompletions, and
	//     CountInputTokens for the stoploss) receives
	//     Normalizer.Normalize(session.Chat, completer.ImageCapabilities()) (P12-3). The
	//     un-normalized session.Chat is what is persisted, so labels, notes and synthetic
	//     messages never reach disk. A Normalize error ends the turn before that call.
	//  4. On an ImageEvent the image is stored with Store.Put, then presented with
	//     Presenter.Present, at once (I6, I7), with present.DetailsOf its bytes, the chat
	//     model (an OpenRouter model with its "or:" prefix) and the time since the step's
	//     first ImageProgressEvent, or no time when the step had none. With structured
	//     output on, nothing is presented, here or in Run.5, so stdout holds only the
	//     final response. It is added to the step's assistant output as
	//     ImagePart{Asset, MIME, Origin: generated}. The persisted assistant message holds
	//     its text and images as Parts, in arrival order. A text-only step still persists
	//     Content (I10).
	//  5. A PartsCaller tool's result is persisted as Parts when it holds an image. Any
	//     other tool result is persisted as Content, byte-equal to today's InvokeWith
	//     output, error folding included (I10). A tool ImagePart with Origin generated is
	//     presented when the tool returns, with the call's imagegen/tool.Rebus when it is
	//     valid and, for generate_image, present.DetailsOf the image, its model and how
	//     long Generate took; the rebus comes only from a generate_image call; tool-origin
	//     images are not presented. Unless raw display, debug or structured output is on,
	//     the tool result is recorded, then printed as a tool block that stays on screen:
	//     the tool's unbounded text, its stoploss prefix starting the first row, without
	//     generate_image's SavedText lines, then after it each generated image's
	//     Presenter.Rows, whole, each followed by the sanitized rest of its SavedText part
	//     (a revised prompt); then the images are opened (Presenter.Open). The rolling
	//     window's rows so far stay where they are above the block, and a new window
	//     starts below it. In raw or debug mode each image gets its own line
	//     (Presenter.Present). A generated image that cannot be stored makes the result
	//     an ERROR text and fails the turn; such a failure, or one showing the images,
	//     still lets the batch's other calls run and record their results.
	//  6. Image generation is set up per Gen.1–3 before the first request.
	//  7. When a turn fails after an image was stored, the asset and its presented line
	//     remain, the failed-run persist keeps the stored part, and the error is returned
	//     as today (I7).
	//  8. *present.OpenFailedError is written once as a notice to Stderr and never fails
	//     the turn (U2). Any other Present error ends the turn.
	//  9. A Store.Put failure on an ImageEvent ends the turn with that error, and nothing
	//     is presented for that image.
	// 10. With ImageTurn set, every write the turn makes goes through
	//     ImageTurn.Persister.Promote, where today it calls chat.SaveAsPreviousQuery.
	// 11. Unless raw display, debug or structured output is on, Progress shows while an
	//     image is generated: from an ImageProgressEvent until the step's next event that
	//     writes (not NoopEvent, nil or ""), and while the client tool's generator runs. It is stopped before
	//     anything else is written. When streamed text left its row open, a newline
	//     precedes it, and an image line that follows takes the same row.
	// 12. Behaviour that is an exception to I10:
	//     - When the last step writes no text and no image, prose already shown before a
	//       tool call is not printed again; it is still logged as the final answer.
	//       Prose the display never showed (debug with raw display) prints then.
	//     - A step whose every call is handed back to the user ends the turn as a step
	//       that answers does, with no further request: its prose and reasoning are the
	//       final answer, or, without prose, the answer shown before stays. With no
	//       answer at all the turn fails with *HandedBackError. When the step also runs
	//       a call, its prose is prose before a tool call, as above.
	//     - Replacing streamed text with its formatted reprint clears the rows it took,
	//       counted in terminal cells, and never a tool block printed before it. Debug
	//       mode streams nothing and clears nothing.
	//     - A stream that ends with io.EOF pops the answer from the rolling window as
	//       StopEvent does, so the window never cuts it.
	Run(ctx context.Context, session *QuerySession) error
}

//  Gen.1. When Generation.Requested is false, nothing is registered.
//  Gen.2. Otherwise imagegen/tool.NewGenerateImageTool(Config, NewGenerator, Store) is
//         registered in the run's toolset when the completer is a models.ToolBox; a
//         completer without one runs without it, as it runs without every tool. The
//         vendor's own image output is never enabled.
//  Gen.3. When NewGenerator is nil, the turn fails before any request with
//         ErrNoImageGenerator.

var ErrNoImageGenerator = errors.New("generate_image requested but no image generator is configured")
