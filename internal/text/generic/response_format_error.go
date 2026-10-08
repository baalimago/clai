package generic

import "errors"

// A response-format file (-rf) is loaded by the vendor-agnostic text layer, but
// what makes a valid response format usable is vendor-specific. These sentinels
// let that layer classify the load failure without knowing any vendor, and let a
// vendor's ErrorHandler recognise the failure and append its own guidance.
var (
	// ErrResponseFormatNotJSON is returned when the -rf file is not valid JSON.
	ErrResponseFormatNotJSON = errors.New("response format file must be JSON")
	// ErrResponseFormatShape is returned when the -rf file is JSON but not a
	// well-formed response_format object.
	ErrResponseFormatShape = errors.New("response format file must be a response_format JSON object")
)

// ErrorHandler is an optional capability of a stream completer. The text layer
// asks for it when it has produced an error that a vendor may be able to
// explain better: the handler inspects the error (with errors.Is against the
// sentinels above) and returns it enriched, or returns it unchanged when it has
// nothing to add. A stream completer that does not implement it is simply never
// asked.
type ErrorHandler interface {
	HandleError(err error) error
}
