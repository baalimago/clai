# Phase 2 — Config + querier plumbing

**Status:** Complete (2026-08-15)

[← README](./README.md)

## Goal

Carry the agent-only slog settings and recorder hooks from `Configurations`
into the querier via one `AgentSettings` pointer, and add the rune
middle-truncation helper.

## Specification

`internal/text/conf.go` (add `log/slog` import):

```go
// AgentSettings carries agent-only runtime settings into the querier. It is
// never serialized to textConfig.json; a nil Logger or recorder disables that
// channel.
type AgentSettings struct {
    Logger           *slog.Logger
    Level            slog.Level
    RuneLimit        int
    UsageRecorder    pub_models.CallUsageRecorder
    ToolCallRecorder pub_models.ToolCallRecorder
}
```

- Add `AgentSettings *AgentSettings` to `Configurations`, tagged `json:"-"`.
  The tag keeps it out of `textConfig.json` and the presence-based config merge
  (`internal/utils/config.go` `jsonConfigKey`).
- Remove `UsageRecorder` and `ToolCallRecorder` from `Configurations`; they move
  into `AgentSettings`.
- Keep `RequestedToolGlobs` and `Out io.Writer` untouched — the CLI, profiles,
  and `pkg/text` full querier set those, so they are not agent-only.

`internal/text/querier_setup.go` (`NewQuerier`):

- When `userConf.AgentSettings != nil`, copy
  `querier.callUsageRecorder = userConf.AgentSettings.UsageRecorder` and
  `querier.toolCallRecorder = userConf.AgentSettings.ToolCallRecorder`, and set
  `querier.agentSettings = userConf.AgentSettings`.
- The existing `querier.callUsageRecorder` / `querier.toolCallRecorder` fields
  stay; only their source changes.

`internal/text/querier.go` (the `Querier` struct):

- Add `agentSettings *AgentSettings` alongside `out`.

New `internal/text/slog_output.go`:

```go
// truncateMiddleRunes returns s unchanged when utf8.RuneCountInString(s) <= limit,
// or when limit <= 0 (no cap, D5). Otherwise it returns head + "…" + tail
// totalling exactly limit runes and reports truncated=true. The split is
// rune-safe: head is the first headLen runes, tail is the last tailLen runes,
// headLen + tailLen == limit-1, balanced head/tail.
func truncateMiddleRunes(s string, limit int) (string, bool)
```

The marker is the single rune `…` (U+2026). It avoids the sub-marker edge case
a longer `…[truncated]…` marker would create for tiny limits (see D2).

## Integration contract

`unit-test-only`.

## Acceptance criteria

- [x] `Configurations` carries `AgentSettings` with all five fields — `TestConfigurations_AgentSettings`
- [x] `Configurations` no longer exposes `UsageRecorder`/`ToolCallRecorder` — `go test ./internal/... ./pkg/...` compiles
- [x] `Configurations` JSON round-trip omits `AgentSettings` — `TestConfigurations_AgentSettings_jsonOmitted`
- [x] `NewQuerier` copies `AgentSettings` and both recorders — `TestNewQuerier_AgentSettings`
- [x] `truncateMiddleRunes` unchanged within limit — `TestTruncateMiddleRunes/within_limit`
- [x] `truncateMiddleRunes` `(out, false)` at exactly limit — `TestTruncateMiddleRunes/at_limit`
- [x] `truncateMiddleRunes` `(out, true)` over limit, `runes(out) == limit` — `TestTruncateMiddleRunes/over_limit`
- [x] `truncateMiddleRunes` never splits a multi-byte rune — `TestTruncateMiddleRunes/multibyte_rune`
- [x] `truncateMiddleRunes` with `limit <= 0` returns `(s, false)` — `TestTruncateMiddleRunes/nonpositive_limit`
- [x] `truncateMiddleRunes` with `limit == 1` returns `("…", true)` — `TestTruncateMiddleRunes/single_rune_limit`

## Error coverage

| Failure | Expected outcome |
| ------- | ---------------- |
| Empty input | `("", false)` |
| Input exactly at limit | unchanged, not truncated |
| Input over limit | `head + "…" + tail`, truncated |
| `limit <= 0` | unchanged, no cap (D5) |
| `limit == 1` | `("…", true)` |
| Multi-byte rune straddles the cut | cut moves to a rune boundary |
| `AgentSettings` set on `Configurations` | not serialized (`json:"-"`) |
| `AgentSettings == nil` | querier recorders stay nil, logging disabled |

## Implementation notes

Executing agent: clai (worker session 2026-08-15-02).

- The `AgentSettings` struct and the `Configurations.AgentSettings` field
  already existed from Phase 1 (D8); this phase removed the now-redundant
  loose `UsageRecorder`/`ToolCallRecorder` fields from `Configurations` and
  moved `NewQuerier`'s recorder source onto the grouped pointer (D9).
  `pkg/agent/asInternalConfig` dropped its loose assignments — the grouped
  pointer is now the only carrier.
- `NewQuerier` copies the whole `agentSettings` pointer when non-nil and
  leaves the querier recorders nil otherwise, so the CLI and `pkg/text`
  paths (which never set `AgentSettings`) are behavior-identical: the
  session runner's existing nil-recorder noop path absorbs the nil.
- `truncateMiddleRunes` lives in the new `internal/text/slog_output.go`
  next to `slogTruncationMarker` (the single rune `…`, U+2026). The
  head/tail split is range-based (`firstRunes`/`lastRunes`) and never
  allocates a full `[]rune` copy: the reasoning buffer is capped at 1 MiB,
  and a full conversion would transiently cost up to 4× that. The split is
  head-biased for even limits (limit 4 → 2 head + 1 tail), balanced for odd
  limits (limit 5 → 2 + 2). All three functions reach 100% statement
  coverage.
- The pre-written `slog_output_test.go` (untracked before this phase)
  compiled and passed unchanged against the implementation.

Verification (all run from the repo root):

```bash
go vet ./...   # clean
```

```bash
go test ./internal/text/ -run 'TestTruncateMiddleRunes|TestConfigurations_AgentSettings|TestNewQuerier_AgentSettings' -v -count=1 -timeout=60s   # all pass
```

```bash
go test ./... -race -cover -count=3 -timeout=30s   # all ok; internal/text 75.4%, pkg/agent 94.2%
```

```bash
go build ./...   # clean
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
go run github.com/mibk/dupl@latest -t 80 .   # clone groups all pre-existing; none in the files touched by this phase
```

## Review findings

_(empty)_
