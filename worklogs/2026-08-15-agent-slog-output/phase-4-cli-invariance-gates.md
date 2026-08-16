# Phase 4 — CLI-invariance + gates

**Status:** Complete (2026-08-15)

[← README](./README.md)

## Goal

Prove the interactive CLI display is byte-identical and run the full quality
gates.

## Specification

Add `internal/text/slog_output_test.go` (or extend the suite):

```go
// TestSlogLogger_DoesNotPerturbDisplay runs the same scripted stream twice,
// once with agentSettings == nil and once with a capturing slog logger, and
// asserts the bytes written to out are byte-identical.
```

The scripted stream uses the existing mock completer and exercises at least one
tool call plus a final answer, so all display paths run.

Do not modify any production write to `out`. Proof of CLI-invariance: (1) the
byte-identity test, and (2) the full pre-existing suite (including CLI e2e)
passing unchanged.

## Integration contract

- **Trigger:** run the same scripted stream twice — `agentSettings == nil` vs a
  capturing `*slog.Logger`.
- **Observable result:** the bytes written to `out` are byte-identical; the
  capturing logger receives the expected completed-message records.
- **Required side effects:** none on the display path.
- **Prohibited side effects:** no production write to `out` is modified,
  reordered, or newly gated; no display-only flag (`structuredOutput`,
  `rawDisplay`, `debug`, `q.Raw`) changes behavior.

## Acceptance criteria

- [x] `out` bytes identical with and without a logger — `TestSlogLogger_DoesNotPerturbDisplay`
- [x] `go test ./... -race -cover -count=3 -timeout=30s` passes with no pre-existing expectation edits
- [x] CLI e2e tests pass unchanged
- [x] `go build ./...` clean
- [x] `go vet ./...` clean
- [x] `go run mvdan.cc/gofumpt@latest -w -l .` clean
- [x] `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` clean
- [x] `go fix ./...` clean
- [x] `go run github.com/mibk/dupl@latest -t 80 .` clean

## Error coverage

| Failure | Expected outcome |
| ------- | ---------------- |
| Logger attached | Display bytes unchanged |
| Logger nil | Display bytes unchanged, no log records |

## Implementation notes

Executing agent: clai (worker session 2026-08-15-04).

- `TestSlogLogger_DoesNotPerturbDisplay` landed in `internal/text/slog_output_test.go`
  next to the `logMessage` unit tests, reusing the existing `captureHandler` +
  `recordAttrs` helpers and the `MockQuerier.streamFn` scripted-stream pattern.
- The scripted stream emits reasoning, assistant prose, one `test` tool call,
  a second reasoning block, and a final answer — so reasoning open/close,
  streamed tokens, the tool-call echo, the tool result, and the final answer
  all run. The tool is registered via `inttools.WithTestRegistry` +
  `inttools.Registry.Set("test", stubTool{...})`, the same pattern as the
  existing tool-flow tests.
- The test runs the script once per display mode: the raw path (`Raw: true`)
  and the default interactive path (rolling activity window pinned via the
  existing `withRollingTheme` helper). For each mode it runs the stream twice
  — `agentSettings == nil` vs `&AgentSettings{Logger: slog.New(captureHandler)}`
  — and asserts the `out` bytes are byte-identical, that the nil run captured
  zero records, and that the logger run captured the five completed-message
  kinds in stream order (`reasoning,assistant,tool_call,tool_result,
  reasoning,final_answer`) with `tool=test` on the tool records.
- No production code changed in this phase. A temporary mutation (the logger
  writing to `out`) made the test fail with a byte diff, proving it detects
  display perturbation; the mutation was reverted.
- The full pre-existing suite — including the CLI e2e tests — passes
  unchanged.

Verification (all run from the repo root):

```bash
go test ./internal/text/ -run 'TestSlogLogger_DoesNotPerturbDisplay' -race -v -count=1 -timeout=60s   # both modes pass
```

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

```bash
go test ./... -race -cover -count=3 -timeout=30s   # all ok, no pre-existing expectation edits
```

```bash
go run mvdan.cc/gofumpt@latest -w -l .   # no output (already formatted)
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # clean
```

```bash
go fix ./...   # no output
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # clone groups all pre-existing, none in this phase's files
```

## Review findings

_(empty)_
