package diagnostic

// SourceLocation identifies a function and a project-relative file location.
// An empty location means that the origin is not available.
type SourceLocation struct {
	Function string
	File     string
	Line     int
}

// Locator is an error that reports the operation and the source location at
// which clai attached its context to a non-nil cause.
type Locator interface {
	error

	// Unwrap returns the original non-nil cause. (Unwrap.1, F2)
	Unwrap() error

	// Operation returns the context supplied by the caller, for example
	// "connect MCP server". (Operation.1, F8)
	Operation() string

	// Location returns where clai attached this context. Locator is the
	// public form of error context. (Location.1, F8, F10)
	Location() SourceLocation

	// Stack returns an origin-captured, bounded stack when capture was enabled.
	// Otherwise it returns no frames. (Stack.1, F8)
	Stack() []SourceLocation
}
