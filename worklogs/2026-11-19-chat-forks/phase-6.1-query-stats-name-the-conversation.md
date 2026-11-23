# Phase 6.1 — Query stats name the conversation

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

The `-qs` run summary names the conversation the run saved, so a primed chat can be forked straight from it: `clai chat fork <id> <task>`.

## Interface

Written to `internal/text/run_summary.go`.

What `-qs` prints on stderr after a query, `clai chat continue <id> <prompt>` or `clai chat fork …`:

```text
▸ query done in 12s
  chat    0198f2c4-7a1e-7c3d-9b21-5f0e8a4c2d17
  tokens  5.2k in (5k cached) · 310 out
  cost    ~$0.02
  tools   none
```

```go
type QuerySummary struct {
	Outcome  QueryOutcome
	Duration time.Duration
	ChatID   string // the conversation the run saved; "" when it saved none
	RunTally
}
```

- `summary.1` When the run saves its conversation, the first detail row is `  chat    <id>`, where `<id>` is the saved file's ID (`conversations/<id>.json`), so `clai chat fork <id>` and `clai chat continue <id>` take it as printed.
- `summary.2` A run that saves no conversation prints no `chat` row.
- `summary.3` Under `-json`, the run-summary record gains the fact `chat_id` exactly when the row would print.
- `summary.4` Every other row, and the loop summary, are unchanged.

Consumes: `QuerySummary`, `finishQuery`, `reportSummary` and `reportSummaryRecord` (repository code, `internal/text`); the finalizer's save decision (repository code).

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1   | `clai -qs q <prompt>` prints `chat <id>` first, and `conversations/<id>.json` exists | summary.1 | `Test_e2e_querySummary_namesChat` |
| 2   | Priming with `-qs`, then `clai chat fork <id from the summary> <task>`, forks that chat | summary.1 | `Test_e2e_querySummary_forkFromPrintedID` |
| 3   | A run with saving off prints no `chat` row | summary.2 | `TestQuerySummary_noChatRowWithoutSave` |
| 4   | The `-json` summary record carries `chat_id` when saved, and not otherwise | summary.3 | `TestQuerySummary_jsonChatID` |
| 5   | Existing summary expectations change only by the new row | summary.4 | the existing run-summary tests, updated |

## Test cases

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `Test_e2e_querySummary_namesChat` | integration scenario | summary.1 | a stand-in provider; `clai -qs q hello` | stderr's first detail row is `  chat    <id>`; that file exists | print the row on stdout |
| `Test_e2e_querySummary_forkFromPrintedID` | integration scenario | summary.1 | prime with `-qs`, read the ID from stderr | `clai chat fork <id> task` exits 0 and prints `fork <F> of <id>` | use `chat list` to find the ID |
| `TestQuerySummary_noChatRowWithoutSave` | unit | summary.2 | a summary with an empty `ChatID` | no `chat` row; the other rows as before | print an empty `chat` row |
| `TestQuerySummary_jsonChatID` | unit | summary.3 | the facts with and without `ChatID` | `chat_id` present only with it | add `chat_id: ""` |
| existing run-summary tests | unit / integration | summary.4 | as today | only the new row differs | change other rows |

## Error management

None. The row only reports an ID the run already holds.

## Golden diff

| Golden | Change |
| ------ | ------ |
| every expectation that pins a `-qs` summary of a run that saves its conversation | gains the `chat <id>` row (the executor lists them) |

## Removes

None.

## Local facts

- The maintainer asked for this on 2026-11-23, to make forking a primed conversation easier.
- The ID must be the persisted one: if the save step mints the ID, the summary takes it from the saved chat, not from the pre-run chat.

## Human required

None.

## Implementation notes

| Run | Commit | Command | Result |
| --- | ------ | ------- | ------ |

## Review findings

None.
