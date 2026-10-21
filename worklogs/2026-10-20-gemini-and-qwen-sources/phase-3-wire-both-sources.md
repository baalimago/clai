# Phase 3 — Wire both sources

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

List and clone Gemini CLI and Qwen Code sessions beside every source clai
already reads.

## Interface

The root interface is the existing `clai chat list` surface, now including the
two new sources.

```text
list.1  clai c list
        lists one row per gemini and qwen session beside native and existing
        foreign chats, from the stores F1 names (F1, F7, F10, L1)

list.2  clai c list
        a row shows the exact message count a clone returns and the preview
        F9 defines (F5, F9); a cache hit costs one stat and no open (F6)

clone.1 clai c list -> select a gemini or qwen row -> c
        clones the session into a native chat and prints
        "cloned <source> session <id> → chat <newID>"; exit 0 (F5, F10)

clone.2 clai c list -> select a gemini or qwen row -> c (store file removed or
        unreadable)
        prints the Read error and leaves the native chat list unchanged;
        non-zero exit (F4, F7)
```

Consumes: `internal/vendors/gemini:SourceReader (phase 1)`,
`internal/vendors/qwen:SourceReader (phase 2)`,
`internal/chat:allSourceReaders` (import), `e2e:runOne` (import),
`e2e:setupMainTestConfigDir` (import).

## Golden diff

None. New scenarios in `e2e/chat_list_e2e_test.go` assert the two clone
notices; no existing golden changes.

## Removes

None.

## Local facts

**L1 — Order.** `allSourceReaders` adds `gemini.SourceReader{}` and
`qwen.SourceReader{}` after `opencode.SourceReader{}`.

**L2 — Isolation.** Each e2e case sets `HOME` to a temporary directory and
clears the root overrides that F1 names for its reader. Each writes one store
file below `HOME` and drives the real list and clone path with
`-n -r c l 0 c`. (F1)

**L3 — Documents.** `architecture/continue-from-claudex.md` gains one Gemini
section, one Qwen section, and updates the Future sources table and the
`allSourceReaders` sketch. The JSONL-source rule there gains the replay
exception both readers need.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                        | Result           |
| ----- | ----------- | ------------------------------ | ---------------- |
| start | 4036e11+wip | make qa && dupl -t 80         | all gates passed |
| end   | 4036e11+wip | make qa && dupl -t 80         | all gates passed |

Andon: none.
Surprises:
- `internal/chat` cannot import `vendors/gemini` while `gemini/image.go` imports `chat`: the photo querier moved to `internal/vendors/gemini/image`, which leaves the vendor package a leaf.

| Run   | Commit      | Command                        | Result           |
| ----- | ----------- | ------------------------------ | ---------------- |
| start | 4036e11+wip | make qa && dupl -t 80         | all gates passed |
| end   | 4036e11+wip | make qa && dupl -t 80         | all gates passed |

## Review findings

| ID    | Severity | Node | Owner | Status     | Summary                                                                                                                                                                                                                       |
| ----- | -------- | ---- | ----- | ---------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R1-04 | major    | 3    | fix   | Closed wip | Two files that claim one source identity produced two list rows; a reader could emit the same `SourceID` twice. `foreignChatRows` now keeps the walk-first row, the same tie-break `ForeignIndex.Locate` uses. Test: `TestForeignChatRows_DuplicateIdentityKeptOnce` (F5, list.1) |
