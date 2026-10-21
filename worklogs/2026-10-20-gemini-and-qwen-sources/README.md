# Gemini CLI and Qwen Code transcript sources

## Goal

clai lists and clones conversations from Gemini CLI and Qwen Code beside the
sources it already reads. Discovery stays read-only and exact: the count a row
shows is the number of messages a clone returns, and a source never misreads a
store it does not understand. Each writer stores one file per session, so the
existing foreign cache applies, and no new dependency is added.

What must stay true: the existing readers and the shared list/clone path keep
their behaviour; neither new reader writes to, renames, or creates a file in a
tool's store.

## Glossary

| Term          | Meaning                                                                                                 |
| ------------- | ------------------------------------------------------------------------------------------------------- |
| Session file  | One file holding one conversation; the unit of discovery for both sources.                              |
| Record        | One JSON value in a session file. Gemini and Qwen both append records while the tool runs.              |
| Patch log     | Gemini's append-only updates to an already written message: `$patch`, `$set` and `$rewindTo`.           |
| Record chain  | Qwen's `uuid` / `parentUuid` tree. The active transcript is one chain from the last record to the root. |
| Fragment      | A Qwen record whose `uuid` repeats an earlier one; its parts are appended to that message.              |
| Sidechain     | A Qwen record produced by a subagent (`isSidechain`), not by the main conversation.                     |
| Subagent file | A Gemini session whose `kind` is `subagent`; never a user conversation.                                 |
| Project slug  | Gemini's `<tmp>/<slug>` directory. `<slug>/.project_root` names the absolute project root.              |
| Runtime base  | Qwen's root: `QWEN_RUNTIME_DIR`, else `QWEN_HOME`, else `~/.qwen`.                                      |

## Flows

### List

```text
user -> clai chat list
clai chat list -> gemini:SourceReader.Discover
clai chat list -> qwen:SourceReader.Discover
gemini:SourceReader.Discover -> clai chat list: one SourceRow per session
qwen:SourceReader.Discover -> clai chat list: one SourceRow per session
clai chat list -> user: rows beside native and existing foreign chats
```

### Clone

```text
user -> clai chat list: select a gemini or qwen row, continue
clai chat list -> gemini:SourceReader.Read
clai chat list -> qwen:SourceReader.Read
gemini:SourceReader.Read -> clai chat list: Chat with a system preamble
qwen:SourceReader.Read -> clai chat list: Chat with a system preamble
clai chat list -> user: cloned <source> session <id> → chat <newID>
```

## Status board

**Line:** running

| #   | Node              | Parent | Class       | Interface                                                                          | Status   |
| --- | ----------------- | ------ | ----------- | ---------------------------------------------------------------------------------- | -------- |
| 1   | Gemini CLI source | root   | contract    | `internal/vendors/gemini:SourceReader` implements `vendors.SourceReader`           | Complete |
| 2   | Qwen Code source  | root   | contract    | `internal/vendors/qwen:SourceReader` implements `vendors.SourceReader`             | Complete |
| 3   | Wire both sources | root   | composition | the root interface: `clai chat list` lists and clones `gemini` and `qwen` sessions | Complete |

### Imports

| Member                                                               | Kind                                          | Used by phases |
| -------------------------------------------------------------------- | --------------------------------------------- | -------------- |
| `internal/vendors:SourceReader`                                      | repository interface                          | 1, 2           |
| `internal/vendors:SourceRow`, `internal/vendors:SourceCache`         | repository types                              | 1, 2           |
| `internal/vendors:OpenAbs`, `internal/vendors:StatAbs`               | repository functions                          | 1, 2           |
| `internal/vendors:ScanJSONLRecords`, `internal/vendors:ReadMaxToken` | repository function and constant              | 1, 2           |
| `internal/vendors:TruncateOneLine`                                   | repository function                           | 1, 2           |
| `internal/vendors:WalkJSONLFiles`                                    | repository function                           | 2              |
| `internal/vendors:HomeRelativeRoot`                                  | repository function                           | 1, 2           |
| `pkg/text/models:Chat`, `Message`, `Call`, `Specification`           | repository types                              | 1, 2           |
| `internal/chat:allSourceReaders`                                     | repository variable in `handler_list_chat.go` | 3              |
| `e2e:runOne`, `e2e:setupMainTestConfigDir`                           | repository test helpers                       | 3              |
| `notes:2026-10-20T20-55-00Z_gemini-and-qwen-store-formats`           | measurement note                              | root facts     |

## Scope

Out of scope, and no phase builds it:

- Qwen Code releases before 0.25 (`tmp/<hash>/chats/*.json`, a Gemini-shaped
  store). The current release writes the record log this worklog reads.
- Qwen managed-session logs (`managed_session_*` wrapper records) beyond
  ignoring them as non-conversation; no phase writes one.
- Gemini checkpoint directories, extension directories, shell history, debug
  logs, and agent bootstrap records.
- ChatGPT or Claude web exports, and any store that needs a network API.
- Any write, migration, rename, lock, or sidecar in either store.
- New third-party libraries. Both formats decode with `encoding/json`.

## Root facts

**F1 — Stores (measured 2026-10-20, cited note).** Gemini CLI 0.64.0 writes
`<gemini-home>/tmp/<project-slug>/chats/session-*.jsonl`, where `<gemini-home>`
is `~/.gemini`, or `~/.cache/.gemini` under `SANDBOX=sandbox-exec`. It also
still reads `.json` session documents in the same directory. Qwen Code 0.25.3
writes `<runtime-base>/projects/<sanitized-cwd>/chats/<sessionId>.jsonl`, where
`<runtime-base>` is `QWEN_RUNTIME_DIR`, else `QWEN_HOME`, else `~/.qwen`.

**F2 — Gemini replay is part of the contract.** A Gemini session file is an
append-only log; the conversation is the state after applying every record in
file order: metadata records, message records keyed by `id`, `$set` (including
its deprecated full-history `messages` replacement), `$patch` (including tool
results), and `$rewindTo`, which drops the rewound record and every message
after it. A reader that scans only message records in file order returns
rewound and superseded content.

**F3 — Qwen's active transcript is a chain.** The conversation is the
`parentUuid` chain from the last conversation record to the root, with records
that share a `uuid` aggregated in file order. A conversation record is any
record except an artifact record (`session_artifact_event`,
`session_artifact_snapshot`) and a non-conversation system subtype
(`session_sources_snapshot`, `managed_session_*`); a sidechain record
(`isSidechain`) is excluded, so a trailing subagent record cannot steal the
leaf. `system` records that remain in the chain (compression, title) map to no
message. A file-order scan returns abandoned branches.

**F4 — Absence and refusal.** A missing store root is absence: no rows, no
error. A file that cannot be decoded contributes no row and does not fail the
walk. A store that cannot be walked and produced no row is an error. `Read`
distinguishes: empty id, unknown id, subagent or sidechain conversation, and no
conversation messages are each an error.

**F5 — Count invariant.** `MessageCount` is the number of messages `Read`
returns minus the system preamble. Discovery and `Read` share one replay and
one mapper per source, so the number in the list and the clone cannot drift.

**F6 — Cache.** Each source stores one file per session, so `SourceCache`
applies: `Lookup` with the `fs.FileInfo` taken before the read, `Store` after
it, and a zero row stored for a file that yields nothing. A nil cache scans
everything.

**F7 — Read-only.** Neither reader opens a store file for writing, renames or
deletes a file, or creates a sidecar. A partially written last record is
tolerated and ignored.

**F8 — Tool calls.** A tool call that has a result (Gemini a result field or a
result patch; Qwen a `tool_result` record) becomes a structured `ToolCall` plus
its `{role: "tool"}` result. A call without a result, a call without an id or
name, or a call whose result cannot be paired is rendered as one
`[tool call: name(args)]` text line, because an unpaired structured call makes
a later provider request invalid. This mirrors the opencode reader.

**F9 — Previews.** `FirstUserMessage` is the one-line display preview, at most
100 runes: Gemini's `summary` when present, else the first prompt; Qwen's
`custom_title` when present, else the first prompt. `FullFirstUserMessage` is
the first user prompt, and falls back to the preview when no prompt survives
the ignored-content rule. A prompt is a user record whose text is not ignored:
Gemini ignores empty text and text starting with `/`, `?`, `<session_context>`
or `<hook_context>`; Qwen ignores `agent_mention` and `agent_message` records
as prompts.

**F10 — Identity and context.** Gemini `SourceID` is the record `sessionId`;
`Cwd` is the project root in the session's `<slug>/.project_root`, else empty;
`Created` is `startTime`; `Model` is the model recorded on the conversation.
Qwen `SourceID` is the record `sessionId`; `Cwd` is the `cwd` field of the
first record; `Created` is the first record timestamp; `Model` is the last
assistant model. Source names are `gemini` and `qwen`.

## Parameters

| Parameter                   | Default                                    | Decided by                             |
| --------------------------- | ------------------------------------------ | -------------------------------------- |
| Source names                | `gemini`, `qwen`                           | maintainer, README sign-off            |
| Preview width               | 100 runes                                  | `vendors.TruncateOneLine` (repository) |
| Gemini legacy `.json` files | read                                       | Gemini CLI v0.64.0 loader (cited note) |
| Qwen compression records    | ignored; the original history is read      | maintainer, README sign-off            |
| Qwen sidechain records      | walked for the chain, mapped to no message | maintainer, README sign-off            |
| Pre-0.25 Qwen stores        | not read                                   | maintainer, README sign-off            |

## Validation policy

### Gates

```sh
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go test ./... -race -cover -count=3 -timeout=30s
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

### Human checks

Both need a real store, not credentials or money. The maintainer runs each
once every phase is `Complete` and records the result in the journal.

| Node | Check                                                                                | Command                                       | Certifies                                                                                                                         |
| ---- | ------------------------------------------------------------------------------------ | --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| 1    | A Gemini CLI 0.64+ store with at least one main and one subagent session             | `clai chat list`, then clone the main session | The listed count equals the clone, the subagent is absent, and the first and last cloned messages match the tool's own transcript |
| 2    | A Qwen Code 0.25+ store, ideally with a sidechain, a compression, and a custom title | `clai chat list`, then clone a session        | The same count equality, the title preview, and that a sidechain record does not appear as a prompt                               |

## Findings

| ID    | Severity | Node       | Owner | Status             | Summary                                                                                                                                                                        |
| ----- | -------- | ---------- | ----- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| V1-01 | major    | 1, 2       | fix   | Closed 4036e11+wip | check 4 · phase 1 and phase 2 interfaces cite no invariant id (F1–F10, L1–L5), so no invariant has a clause that proves it; cite the id from each clause that states one       |
| V3-01 | major    | 3          | fix   | Closed 4036e11+wip | check 5 · phase 3 L2 restates the `SANDBOX`, `QWEN_RUNTIME_DIR` and `QWEN_HOME` literals that F1 defines; cite F1 and name no literal                                          |
| V1-02 | major    | 3          | fix   | Closed 4036e11+wip | check 4 · phase 3's interface states three unnumbered behaviours, so no clause id exists to prove them or to name the composition's scenarios; number them and cite F4, F5, F9 |
| V1-03 | major    | root, 1, 2 | fix   | Closed 4036e11+wip | check 5 · the store roots are defined in F1 and restated in phase 1 L1, phase 2 L1 and both interface headers; keep the literals in F1 and let the phases cite it              |

## Session journal

- 2026-10-20 · plan · README drafted
- 2026-10-20 · plan · README amended: Imports, F2, F3, F8
- 2026-10-20 · maintainer · README signed
- 2026-10-20 · work · phase 3 Complete
- 2026-10-20 · validate · V1 Not ready · go vet on the assembled interface unit: ok, tree 3 rows / 3 files, dependencies and classes clean · notes: 5
- 2026-10-20 · fix · V1-01, V1-02, V1-03 closed · go vet ./..., go test ./internal/vendors/gemini/... ./internal/vendors/qwen/... ./e2e/: ok · also removed the stale settled-status parameter and the unused `e2e:moduleRoot` import
- 2026-10-20 · validate · V2 Not ready · go vet ./tmp_validate_a (checks 1–6), inspection (checks 7–10): V1-01 open · notes: 7
- 2026-10-20 · fix · V1-01 closed · cited L5 from qwen Read.2, F7 from both Read.1 clauses and phase 3 list.1; qualified the flow participants, removed the self-arrow, corrected the clone notice to →, named each case's cleared env vars in phase 3 L2, replaced the restated 100-rune literal with a citation to F9, and removed the duplicate `end` row · go test ./... -race -cover -count=3 -timeout=30s: ok (an earlier run at load 7 timed out internal/audio and e2e at 30s; both pass alone)
- 2026-10-20 · validate · V3 Not ready · go vet ./tmp_validate_c/...: ok, checks 1–6 and 7–10 run · notes: 4 · stopped per the round-3 rule; open findings handed to the maintainer
- 2026-10-21 · fix · V1-01, V3-01 closed · added L4/L5 to both Discover.2 clauses and F2/F3 to both type headers; phase 3 L2 cites F1 with no literal, list.1 cites L1, list.2 claims the F9 preview · go test ./... -race -cover -count=3 -timeout=30s: ok
- 2026-10-21 · validate · V4 Ready · go vet ./tmp_validate_e: ok, checks 1–6 and 7–10 run · notes: 8
