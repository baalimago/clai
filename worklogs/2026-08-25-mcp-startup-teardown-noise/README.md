# MCP client startup/teardown noise (`file already closed` / `channel closed`)

Analysis date: 2026-08-25. Repo: clai. Status: analysis complete, fix not yet implemented.

## The incident

Starting clai with a `slivingdoc` MCP server emitted, in one second:

```text
2026-08-25T07:57:21+03:00 error: client: slivingdoc, got error when encoding message: 'map[jsonrpc:2.0 method:notifications/initialized params:map[]]', error: write |1: file already closed
2026-08-25T07:57:21+03:00 error: client: slivingdoc, got error when encoding message: '{2.0 2 tools/list map[]}', error: write |1: file already closed
2026-08-25T07:57:21+03:00 error: channel closed
```

The three lines look like a server crash during the MCP handshake. They are
not. Every line comes from clai's own `internal/tools/mcp` package, and the
server was later proven to start and serve correctly.

## What the three lines actually are

| Line                                                           | Origin                         | Meaning                                                                             |
| -------------------------------------------------------------- | ------------------------------ | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `client: slivingdoc, got error when encoding message ... write | 1: file already closed`        | `client.go:81` writer goroutine                                                     | clai closed its own stdin pipe and then tried to encode two queued handshake messages |
| `channel closed`                                               | `manager.go:144` `sendRequest` | clai's stdout reader saw EOF (the child process ended) and closed the `out` channel |

The `write |1` path is decisive. `os/exec.Cmd.StdinPipe()` returns the write
end of an `os.Pipe()`, which Go names `|1`. Nothing in `os/exec` closes that
file for the caller. clai closes it in exactly one place, the teardown
goroutine:

```go
// client.go:172-175
go func() {
    <-ctx.Done()
    stdin.Close()
}()
```

So the write failures are the direct consequence of clai cancelling the
server's context — not of the server refusing to read. The subsequent
`channel closed` is the same teardown seen from the read side: the child
process ends, its stdout write end closes, the scanner loop exits, and
`defer close(out)` (`client.go:95`) closes the output channel that
`sendRequest` is waiting on.

The full chain for one run:

1. The run context is cancelled (run teardown, or an unrelated failure).
2. `exec.CommandContext` kills the child; the `<-ctx.Done(); stdin.Close()`
   goroutine closes the stdin pipe concurrently.
3. The writer goroutine still holds the queued `notifications/initialized`
   and `tools/list` messages; each `enc.Encode` now fails with
   `write |1: file already closed`.
4. The child's stdout EOF reaches the reader goroutine, which closes `out`.
5. `sendRequest` observes the closed `out` and logs `channel closed`.

The three lines are therefore shutdown noise: the observable tail of a
cancelled run, not a defect in the MCP server.

## Server-side verification

The server in the incident is slivingdoc v0.1.7, launched as
`npx -y slivingdoc serve ...`. It was checked against the reporter's exact
flags and AWS credentials:

```bash
. /home/imago/.sandbox-safe/.slivingdoc && \
  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{},"clientInfo":{"name":"clai","version":"dev"},"protocolVersion":"2025-03-26"}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | ~/.npm/_slivingdoc/0.1.7/linux/amd64/slivingdoc-v0.1.7-linux-amd64 serve \
      --bucket slivingdoc --region eu-north-1 \
      --workspace-root /tmp/slprobe-ws --private-root /tmp/slprobe-priv
```

`initialize` and `tools/list` both returned valid results and the process
exited 0. The S3 store was reachable (the `current` manifest reads at
generation 8), and `npx -y slivingdoc version` returns `slivingdoc 0.1.7`.

The most probable trigger for the 07:57 run is a transient first-run delay:
the slivingdoc binary's cache entry is timestamped 08:00, after the 07:57
error, so at 07:57 the `npx` launcher was still downloading the ~15 MB
native binary (or npm was installing the package). The run context was
cancelled during that window. The binary is now cached, so later starts
are fast.

A further signal that slivingdoc never logged a startup refusal: the paste
contains no `mcp_slivingdoc: ...` line at all. clai buffers server stderr
and flushes it on teardown; a real refusal (for example `INCOMPATIBLE_STORE`)
would have appeared there.

## Client-side defects

The incident exposes three defects in `internal/tools/mcp`. None caused the
teardown; all three turned an expected teardown into misleading output.

### Finding A — expected teardown logged at `error` level

`client.go:81` logs every encode failure at `error`, even when the failure
is the direct, intended result of the teardown that clai itself started one
line away (`stdin.Close()`). Any cancellation that lands between a channel
send and the following encode produces this line. The writer goroutine also
keeps looping after an encode error (`client.go:74-86`), so one cancellation
can emit one `error` line per queued message.

### Finding B — `sendRequest` swallows the connection-closed error

`manager.go:144-145` returns `(Response{}, nil)` when the output channel
closes:

```go
case msg, ok := <-out:
    if !ok {
        ancli.Errf("channel closed")
        return Response{}, nil
    }
```

The `nil` error tells `handleServer` the request succeeded with an empty
response. It then proceeds to send the notification and `tools/list`, and
only fails later on an unrelated unmarshal error (`decode list result:
unexpected end of JSON input`). The real failure — the server ended during
the handshake — is masked. This branch must return an error.

### Finding C — close/encode race on the stdin pipe

`client.go:172-175` closes `stdin` as soon as the context is done, with no
coordination with the writer goroutine. Messages that were accepted into
the unbuffered `in` channel before cancellation are then encoded into a
closed file. The writer goroutine checks `ctx.Done()` only in its select,
not before encoding, so it does not stop cleanly. The fix is either to stop
encoding once the context is done, or to suppress the encode error when
`ctx.Err() != nil`.

## Proposed fix (not yet implemented)

- `manager.go`: return `fmt.Errorf("mcp %s: connection closed", ...)` from
  the `!ok` branch instead of `nil`.
- `client.go`: in the writer goroutine, stop on `ctx.Done()` before encoding,
  and log the encode failure at `debug` (or only when `ctx.Err() == nil`).
- `client.go`: add a regression test that cancels the context while a request
  is queued and asserts the client does not emit an `error`-level encode line
  and does not report a spurious `channel closed` for a succeeded request.

## Open items / follow-ups

- Implement the proposed fix behind tests first, per the clai way of work.
- Re-run the reporter's `slivingdoc` server after the fix and confirm a
  clean teardown (no `file already closed` / `channel closed` noise).
- Consider the same `channel closed` masking in any other `sendRequest`
  caller path beyond `handleServer`.
