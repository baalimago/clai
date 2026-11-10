# Phase 1 — Error context

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Expose source locations on clai-owned error-context wrappers without breaking Go error chains.

## Interface

Written to `pkg/diagnostic/context.go`.

```go
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
```

Consumes: Go's `error`, `errors.Is`, `errors.As`, and error unwrapping rules.

## Acceptance criteria

| #   | Criterion                                                                      | Proves                      | Proof                                             |
| --- | ------------------------------------------------------------------------------ | --------------------------- | ------------------------------------------------- |
| 1   | Existing error causes, text, and typed matches remain available.               | Unwrap.1, F2                | `TestLocatorContract`                             |
| 2   | Each clai-owned context wrapper exposes its operation and attachment location. | Operation.1, Location.1, F8 | `TestLocatorContract`, `TestLocatorInventory`     |
| 3   | Origin stacks exist only when captured and never claim a missing origin.       | Stack.1, F8                 | `TestLocatorContract`                             |
| 4   | Existing agent consumers continue to compile and receive their typed errors.   | Unwrap.1, F2                | `TestAgentErrorCompatibility`                     |
| 5   | `pkg/diagnostic` and `pkg/agent` export nothing outside the F10 allowlist.     | Location.1, F10             | `TestPublicAPISurface`                            |

## Test cases

| Test                          | Level                | Proves                                     | Given                                                                             | Then                                                                        | Must not                                                |
| ----------------------------- | -------------------- | ------------------------------------------ | --------------------------------------------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------- |
| `TestLocatorContract`         | contract suite       | Unwrap.1, Operation.1, Location.1, Stack.1 | A wrapped sentinel, typed cause, and joined causes                                | The operation, location, optional stack, and original causes are accessible | Change existing error text or report absolute paths     |
| `TestLocatorInventory`        | repeatable command   | Criterion 2                                | The production source tree and its explicit exclusions for non-context formatting | Every clai-owned context wrapper uses the contract                          | Count tests or third-party source as migrated clai code |
| `TestAgentErrorCompatibility` | integration scenario | Unwrap.1, F2                               | Existing `pkg/agent` failure cases                                                | Existing callers compile and match the same sentinel and types              | Require a diagnostic Reporter                           |
| `TestPublicAPISurface`        | repeatable command   | Criterion 5, F10                           | The exported identifiers of `pkg/diagnostic`, and those `pkg/agent` gained since `2c9c4ff` | Every identifier is in the F10 allowlist                           | Count unexported identifiers or `internal/diag`         |

## Error management

| Failure                  | Expected outcome                                                  | Test                  |
| ------------------------ | ----------------------------------------------------------------- | --------------------- |
| Original operation fails | Preserve its non-nil cause and existing text through the wrapper. | `TestLocatorContract` |
| Origin stack unavailable | Return no frames and preserve the original error.                 | `TestLocatorContract` |

## Golden diff

None.

## Removes

None.

## Local facts

`diag.Errorf` and `diag.ErrorfWithStack` delegate to the leaf package
`internal/errctx`, which holds their body so that `internal/jsonl` can wrap
errors without importing `internal/diag`; the phase's suite proves it through
the `diag` functions.
An absent origin stack is an expected state, not an error. Library callers can
traverse independent joined causes with Go's standard error conventions.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-11: `ErrorContext` renamed `Locator`. V1-03: criterion 4 cites F2 only; F3 binds phase 8. |
| replan    | 2c9c4ff+wip | —       | F10: Location.1 cites the public surface; `TestPublicAPISurface` bounds the exported identifiers to the allowlist from this phase on. |
| start     | 1bebb27     | full validation policy gate set | pass: all six commands |
| end       | 1bebb27+wip | full validation policy gate set, randomized | pass: all six commands |
| end       | 1bebb27+wip | full validation policy gate set, randomized | pass: all six commands |

Andon: none.

## Review findings

### Review 1, 2026-11-09 (root)

**R1-09 · minor · owner plan.** No interface declares the producer of `Locator`s. Since the V9 fix, `internal/errctx` exports `Errorf`, `ErrorfWithStack`, `ErrorfAt`, `Capture`, `LocationOnly` and `SourceLocation`, and `internal/jsonl` calls `errctx.Errorf` directly. `TestLocatorContract` runs only the `diag.*` factories (`pkg/diagnostic/context_test.go:42`).
- Scenario: a change to `errctx.Errorf` that drops the location still passes the contract suite.
- [ ] Declare the producer in this phase's Interface (or record why `internal/errctx` is body).
- [ ] Add an `errctx` factory to `locatorFactories()`.

Verified good:
- The Interface matches `pkg/diagnostic` token for token.
- `TestPublicAPISurface` holds the F10 allowlist.

Note: `TestLocatorInventory` still excludes `internal/diag/context.go`, which no longer wraps errors (the wrapper is now `internal/errctx/errctx.go`). The test passes only because errctx's format string is not a constant, so the exclusion is stale but harmless.
