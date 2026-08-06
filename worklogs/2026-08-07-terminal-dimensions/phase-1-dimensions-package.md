# Phase 1 — Shared dimensions package

**Status:** Done  
[Back to README](./README.md)

## Goal

Add a reusable Unix terminal-dimensions viewer to `go_away_boilerplate/pkg/dimensions`.

## Specification

Provide width and height as one `Dimensions` value. Provide a stateful `Viewer` bound to an output terminal, an initial/current read, and resize notifications driven by `SIGWINCH`. Query `TIOCGWINSZ` from the selected terminal; do not invoke tmux or external commands. Keep all ioctl and fallback logic in this package. Support injected readers or dimensions for deterministic tests.

The package must not write to the terminal from a signal callback. It must be safe to stop watching through context cancellation and must not leak signal registrations or goroutines. This phase is Unix-only.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| Current dimensions requested | PTY/file descriptor | Width and height are returned together | One live ioctl query | No tmux subprocess |
| `SIGWINCH` received | Viewer watcher | A fresh dimension snapshot is delivered | Query after notification | No terminal write in signal path |
| Context cancelled | Viewer watcher | Notification channel closes or stops | Signal registration is released | No goroutine leak |
| Non-terminal/failing ioctl | Fallback policy | Documented safe dimensions/error | Deterministic fallback | No stale `COLUMNS` override |

## Acceptance criteria

- `pkg/dimensions` exposes a reusable `Dimensions` and `Viewer` API.
- Width and height are obtained through one Unix implementation.
- `SIGWINCH` produces a fresh query.
- Cancellation releases resources.
- Tests cover successful reads, invalid/zero sizes, signal delivery, cancellation, and failure behavior.
- New package code has very high, meaningful coverage, with deterministic tests for injected provider, signal, cancellation, stop, fallback, and error behavior. Do not add production seams solely to increase a percentage.
- `go test ./...` passes in `go_away_boilerplate`.

## Error coverage

| Failure | Expected behavior | Test |
|---|---|---|
| ioctl fails | Return documented fallback or wrapped error | provider failure test |
| ioctl returns zero dimension | Clamp or fallback according to API contract | zero-size test |
| watcher context ends | Stop and release signal notification | cancellation test |
| notification channel is full | Coalesce resize invalidations | burst-signal test |
| injected signal source stops or closes | Viewer exits without blocking or leaking | source-close test |
| stop called more than once | Cleanup remains idempotent | repeated-stop test |
| refresh fails after a valid snapshot | Last valid snapshot and documented error policy are preserved | refresh-after-valid test |

## Implementation notes (2026-08-07, worker session 1 — execution)

Implemented in `go_away_boilerplate/pkg/dimensions` as the sole
terminal-dimension implementation of the module. The API contract below
resolves R1-01, R1-02, and R2-02 and is the policy that phase 2 must consume.

### API contract

- `Dimensions{Width, Height int}` carries the size as one value; both fields
  are greater than zero for a usable size.
- `Fallback` is the documented safe size `80x24` and preserves the historical
  `table.TermWidth` fallback of 80 columns.
- `ErrUnavailable` is the sentinel every read failure wraps. Callers use
  `errors.Is(err, ErrUnavailable)` to detect "this file descriptor has no
  usable terminal size".
- `Reader func() (Dimensions, error)` performs one live query and never
  fabricates a size.
- `DefaultReader(fd)` queries `TIOCGWINSZ` on fd. A failed ioctl (including
  `ENOTTY` on non-terminals) or a reported zero width or height returns zero
  `Dimensions` and a wrapped `ErrUnavailable`. The unix build is the only
  real implementation; the `!unix` stub returns `ErrUnavailable`.
- `New(ctx, fd, opts...) *Viewer` binds the viewer to the fd of the actual
  output writer (R2-02). The viewer borrows the fd and never closes it. `New`
  performs one initial read and starts the watcher goroutine.
- `Snapshot() (Dimensions, error)` performs one live read per call. On
  failure it returns the last valid snapshot, or `Fallback` when no valid
  snapshot exists, together with the wrapped error. It stays usable after
  stop.
- `Events() <-chan Dimensions` is the resize channel (capacity one). A
  successful signal-driven refresh delivers a fresh snapshot; bursts
  coalesce via non-blocking sends. The channel closes when the viewer stops;
  receivers use the two-value receive or `range`.
- `Stop()` is idempotent, releases the owned `signal.Notify` registration,
  closes `Events`, and blocks until the watcher has exited. Context
  cancellation and a closed injected signal source stop the watcher the same
  way; `Stop` remains safe afterwards.
- `WithReader(r)` injects deterministic reads; `WithSignalSource(src)`
  injects signals so unit tests never send process-global signals. A closed
  source stops the viewer. A nil source or nil reader selects the default
  registration or ioctl reader.

### Zero-size and failure policy (R1-02)

One policy applies to every reader, injected or default: a read is usable
only when it succeeds and reports positive width and height. The viewer
normalizes all reads (`normalizeRead`), so an injected reader that reports a
zero size follows the same `ErrUnavailable` policy as the ioctl reader.
`COLUMNS` is never consulted; the ioctl is authoritative. No subprocess is
invoked.

Phase 2 mapping: the `TermWidth` compatibility wrapper will map
`ErrUnavailable` to `(Fallback.Width, nil)` to preserve the legacy silent
80-column fallback; callers that need terminal awareness check `errors.Is`.

### Test coverage

Behavioral tests with injected readers and signal sources cover live reads,
initial-read failure, zero width and height, refresh failure after a valid
snapshot (last valid size preserved, no event on failure), signal delivery,
burst coalescing (1000 signals, one buffered event), repeated stop,
context cancellation, closed signal source, post-stop snapshot, nil reader,
and default signal registration. A subprocess test (`TestViewer_RealSIGWINCH`)
exercises the process-wide signal path end to end without contaminating other
tests. Real-PTY tests on linux verify a live `TIOCGWINSZ` success and a
zero-size failure; a pipe test verifies the `ENOTTY` path deterministically.
Statement coverage on the linux build is 100%.

### Commands and results

Baseline before changes: `go test ./... -race -count=3 -timeout=30s` passed
in `go_away_boilerplate`. After the change the same command passed, plus
`go vet ./...`, `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`,
`go run mvdan.cc/gofumpt@latest -w -l .`, and `go fix ./...` were clean.
Cross-platform compile checks passed: darwin/amd64 and linux/arm64 for the
whole module, windows/amd64 for the dimensions package.

## Review findings (review 1, 2026-08-07)

**R1-01 — Major — resolved.** [x] The `Viewer` API is defined in the
implementation notes: fd binding and borrowing, initial read in `New`,
notification channel behavior, idempotent stop, and injected readers and
signal sources that replace OS facilities in tests.

**R1-02 — Major — resolved.** [x] One zero-size and failure policy is
documented and enforced by `normalizeRead` and `DefaultReader`: a usable size
requires a successful read with positive width and height; failures wrap
`ErrUnavailable`; `Snapshot` returns the last valid snapshot or `Fallback`
(80x24). The phase 2 mapping is stated in the implementation notes.

Verified good: the phase correctly prohibits terminal writes from the signal
callback and requires cancellation cleanup, burst coalescing, and failure tests.

## Review findings (review 2, 2026-08-07)

**R2-02 — Normal — resolved.** [x] The viewer takes the fd of the actual
writer and never closes it; `table.TermWidth` keeps stderr semantics in
phase 2 only as a deliberate compatibility wrapper, while clai binds the
snapshot to `querier.out` in phase 3. The snapshot-fd-equals-writer-fd
assertion is a phase 3 acceptance criterion.