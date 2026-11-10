package diag

import (
	"context"
	"io"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/pkg/diagnostic"
)

type ErrorReporter interface {
	// ReportError displays each independent cause once, with known remedies,
	// on the owner's diagnostic destination. It preserves the supplied error
	// and returns a failed or short write. (ReportError.1, F2, F8)
	//
	// The owner supplies the presenter's Level. At LevelDebug or above each
	// cause shows its operation and location; at LevelTrace it also shows
	// its origin-captured stack; below LevelDebug it shows causes and
	// remedies only. (ReportError.2, F8)
	//
	// A Presenter from NewJSONPresenter writes one F14 "error" record per
	// terminal error through its Encoder, one cause per independent cause,
	// showing what ReportError.1 and ReportError.2 show at its Level, with the
	// provider fields of a claierr API error, and under the Deliver.2
	// redaction. (ReportError.3, F7, F11, F12, F14)
	ReportError(context.Context, error) error
}

// Viewport is the consumed shape of board.Board.Log: an in-place display
// that places a persistent line above itself. This phase declares no clause
// for it; the owner detaches a viewport before it stops accepting lines.
type Viewport interface {
	Log(line string)
}

// Presenter is the Deliverer for the Immediate output mode and the terminal
// error presenter. It is internal to clai.
type Presenter interface {
	// Attach makes v the active viewport until the returned function runs.
	// While one is attached, Deliver and ReportError place their lines
	// through v.Log; attaches nest, and detaching restores the previous one.
	// The owner detaches before the viewport stops accepting lines, since
	// Attach cannot observe that. (Attach.1, F1)
	Attach(v Viewport) (detach func())

	// Deliver writes one diagnostic to the owner's diagnostic destination,
	// never to a result writer, through the attached Viewport when there is
	// one. It escapes terminal controls and applies best-effort redaction to
	// external text. (Deliver.2, F1, F7, F10)
	//
	// It uses the configured theme on a terminal, plain text on a pipe or
	// with truthy NO_COLOR, and timestamps per the Terminal timestamps
	// parameter (README Parameters). (Deliver.3)
	//
	// A Presenter from NewJSONPresenter writes jsonl.EventRecord(e), after
	// the Deliver.2 redaction, through its Encoder, with no theme, viewport
	// placement, or timestamp, and Attach has no effect on it. The format
	// changes the encoding only, never the destination. (Deliver.5, F7, F11,
	// F12)
	//
	// It returns a failed or short write. (Deliver.4, F6)
	diagnostic.Deliverer

	ErrorReporter
}

// Color is the text presenter's color choice, which the owner derives from the
// destination's terminal state and NO_COLOR (Deliver.3).
type Color uint8

const (
	Plain Color = iota
	Colored
)

// NewTextPresenter returns the Presenter that writes text to w, in color, at
// level. (NewTextPresenter.1, F11)
func NewTextPresenter(w io.Writer, color Color, level diagnostic.Level) Presenter {
	return &textPresenter{destination: w, color: color, level: level, redactor: newRedactor()}
}

// NewJSONPresenter returns the Presenter that writes records through enc,
// the one Encoder of its destination, at level. (NewJSONPresenter.1, F11,
// F12)
func NewJSONPresenter(enc jsonl.Encoder, level diagnostic.Level) Presenter {
	return &jsonPresenter{encoder: enc, level: level, redactor: newRedactor()}
}
