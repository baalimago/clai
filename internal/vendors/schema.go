package vendors

import "time"

// schema.go declares what a JSONL-backed source tells the generic discovery
// algorithm: the generic layer owns the walk, the scan and the aggregation, a
// vendor owns only the meaning of one line.

// LineRole classifies one JSONL line for counting and preview extraction.
type LineRole uint8

const (
	// LineRoleNone is not a message, but may still carry identity fields.
	LineRoleNone LineRole = iota
	LineRoleUser
	LineRoleAssistant
	LineRoleTool
	// LineRoleSkip discards the whole line: no fields, no count.
	LineRoleSkip
)

// LineFields is everything one JSONL line may contribute to a SourceRow. The
// set is closed — TestLineFields_closedSet fails on a new field — and when
// Role is LineRoleSkip every other field is ignored.
type LineFields struct {
	SessionID string
	Cwd       string
	Timestamp time.Time
	Model     string
	Role      LineRole
	// UserText is the full user text; the generic layer owns truncation.
	UserText string
}

// JSONLSchema is all a JSONL-backed source implements. It never receives a
// path, handle, reader or line index, so it cannot decide how much of a file
// is read.
type JSONLSchema interface {
	SourceName() string
	// Root is empty when the source is not configured on this host.
	Root() string
	// SkipDirs names directories that never hold sessions of their own.
	SkipDirs() []string
	Fields(line []byte) LineFields
}

// LinePrefilter is an optional upgrade of JSONLSchema that skips decoding
// lines which cannot contribute. Not implementing it is always correct, only
// slower; implementing it wrongly silently undercounts, so a vendor that does
// must run jsonltest.RunSchemaConformance over a corpus with a line per marker.
type LinePrefilter interface {
	// MayContribute must never be false for a line Fields would read from;
	// true for a line that yields nothing only costs a decode.
	MayContribute(line []byte) bool
}
