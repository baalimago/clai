# StreamCompleter goroutine leak (sakfraga incident 2026-08-12)

Analysis date: 2026-08-12. Fix date: 2026-08-12 (clai repo, uncommitted).

## Status board

| #   | Phase                      | Status | Summary                                                            |
| --- | -------------------------- | ------ | ------------------------------------------------------------------ |
| 1   | Root-cause confirmation    | Done   | One leaked goroutine per LLM round in `generic.StreamCompleter`    |
| 2   | Tests-first implementation | Done   | 5 regression tests, all red before the fix                         |
| 3   | Fix: generic stream        | Done   | Terminal-event return + ctx-guarded sends                          |
| 4   | Fix: sibling streams       | Done   | Same hardening for claude and openai responses streams             |
| 5   | Quality gates              | Done   | gofumpt, vet, fix, staticcheck, dupl, `go test ./... -race -count=3` pass |

## The incident

The sakfraga container on hemserver leaked one goroutine per LLM round at
16-20 rounds per minute since the 19:45 EEST deploy. `go_goroutines` climbed
from 590 (flat for 10 h) to 1700 at write-up time. The pprof dump showed 1231
of 1394 goroutines in the same stack:

```
goroutine 10229 [chan send, 71 minutes]:
generic.(*StreamCompleter).handleStreamResponse.func1()
    internal/text/generic/stream_completer.go:138
```

Line 138 is the unbuffered send of a streamed event. Five parent goroutines
(the crawler harvest agents) had each created 186-307 leaked stream
goroutines. RSS climbed 41 MB to 446 MB; the container has no memory limit,
so the leak would eventually OOM the host.

## Root cause

The session runner (clai's consumer) returns on `StopEvent` (the `data:
[DONE]` frame) and never reads the channel again. The producer continued its
loop. The `[DONE]` frame is `data: [DONE]\n\n`, so the trailing blank line is
a separate read that yields a `NoopEvent`. Its send to the abandoned
unbuffered channel blocked forever. The ctx check sits at the top of the
loop only, so cancellation never unblocks the in-flight send.

The same class exists in two sibling streams:

- `internal/vendors/openai/responses_stream.go`: terminal guard present,
  ctx guard missing on all sends.
- `internal/vendors/anthropic/claude_stream.go`: ctx guard missing on all
  sends, plus a second bug — a clean EOF emitted an io.EOF terminal event
  and then a second "failed to read line" error into the abandoned channel,
  blocking forever.

## The fix

The producer now (1) stops after delivering the terminal `StopEvent`/io.EOF
event and (2) guards every send with a select on `ctx.Done()`. Guard helpers:
`emitResponses` (openai) and `emitClaude` (anthropic). The claude EOF branch
no longer emits the duplicate error after the terminal io.EOF event.

## Regression tests

All tests use an `io.Pipe` as the response body and a consumer that mirrors
the session runner (return on terminal event, never read again). They assert
that the producer terminates: after the terminal event the channel must close
with no further events; after ctx cancellation a further pipe write must fail
with `io.ErrClosedPipe` (the producer closed the body). The pipe write is the
synchronization point: it completes only once the producer consumed the
bytes, proving the producer is mid-send when the test cancels.

| Test                                                       | Guards                                        |
| ---------------------------------------------------------- | --------------------------------------------- |
| `generic: TestHandleStreamResponse_ClosesAfterStopEvent`   | Reported leak (trailing blank line after DONE) |
| `generic: TestHandleStreamResponse_ClosesOnCtxCancelMidSend` | ctx-cancel mid-send                          |
| `anthropic: TestClaudeStream_ClosesAfterEOFWithoutDoubleSend` | claude duplicate error on clean EOF         |
| `anthropic: TestClaudeStream_ClosesOnCtxCancelMidSend`     | ctx-cancel mid-send                            |
| `openai: TestResponsesStream_ClosesOnCtxCancelMidSend`     | ctx-cancel mid-send                            |

Each test failed against the pre-fix code (event delivered after the terminal
event, or the producer never terminating) and passes with the fix, also under
`-race -count=3`.

## Quality gates

All pass:

- `go run mvdan.cc/gofumpt@latest -w -l .`
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`
- `go vet ./...`
- `go test ./... -race -cover -count=3 -timeout=30s`
- `go fix ./...`
- `go run github.com/mibk/dupl@latest -t 80 .` — flagged clones are all
  pre-existing and outside the changed files

## Follow-ups (outside this fix)

- Restart `sakfraga-sakfraga-1` to reclaim the leaked goroutines and swap
  pressure, then bump sakfraga's clai pin, redeploy, and watch
  `go_goroutines` stay flat.
- Add a container memory cap in sakfraga's `compose.yaml` (host had 230 MB
  free, swap 1.9/2.0 GB).
- The Playwright MCP wedge that exposed the leak (hung chromium navigation)
  is a separate issue.
