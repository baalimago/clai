# Chat forks, prompt caching and cloud continue

## Goal

`clai chat fork <chat> <prompt>` sends a copy of an existing chat plus a new prompt as a new chat, and
leaves the parent as it was. Many forks of one primed chat can run in parallel, and each one reads the
parent's conversation from the provider's prompt cache instead of paying for it again. Caching is
automatic on every vendor, with no flag or setting. OpenAI, DeepSeek, xAI and Kimi already cache by
prefix with no request change. Claude caches only when a request asks for it, so clai adds Anthropic's
one top-level `cache_control` field to every request it sends to Anthropic directly.
Claude models reached through OpenRouter stay uncached.

Two additions ride on the same branch. `clai chat continue cc:<chatID>` (and `clai chat fork
cc:<chatID>`) takes the conversation from the account's conversation store on clai.chat: the cloud copy
keeps the ID, and a local copy holding messages the cloud copy lacks is kept as a fork under a new ID.
`clai chat list` names the conversation a plain `-re` continues on a line above the table.

What must stay true: a fork's request repeats its parent's last request byte for byte up to the new
prompt (I1), the parent is never written (I2), no local message is lost to a cloud copy (I8), and every
other command's output is as before.

Repository: `baalimago/clai`, branch `feat-clai-forks-and-conversations-cleanup`. This effort imports
[`previous-query-pointer`](../2026-11-17-previous-query-pointer/README.md) (cited as
`previous-query-pointer:<member>`): its record is how `-re`, a fork's finalize and the list line find
the previous query, so that worklog reaches its phase 8 before phase 2 here starts.

## Glossary

| Term | Meaning |
| ---- | ------- |
| Parent | The chat named by `chat fork`. |
| Fork | The new chat `chat fork` writes: the parent's messages plus the new prompt and its answer. Also a local copy that adoption moves aside (I8). |
| Primer | A parent made on purpose to be forked: one chat that loads the shared context once. |
| Prefix | Everything a request sends before its newest message: system prompt, tools in order, earlier messages. |
| Cache field | Anthropic's top-level `cache_control: {"type": "ephemeral"}` ("automatic caching"): the provider puts one breakpoint on the request's last cacheable block and reads the longest entry it finds within 20 positions before it. |
| Lineage | A chat and the forks made from it, all sharing the root's `GroupKey`. |
| Previous query | The conversation `previous-query-pointer:LoadPreviousQuery` names: the one a plain `-re` continues. |
| Cloud store | The account's `conversations` store on clai.chat, the one `clai cloud commit` uploads to (`architecture/cloud.md`). |
| Cloud reference | `cc:<chatID>`: a chat argument that names the cloud store's copy of `<chatID>`. A bare ID or index is local. |
| Adoption | Writing the cloud store's copy of a conversation, its reasoning sidecar and its images to this machine under the same ID. |

## Flows

### Fork a chat: `clai chat fork <chat> <prompt>`

```text
clai chat fork P "task"
  │
  ├─ no prompt? ──────────── stop with an error, nothing written
  ├─ find the parent P ───── chat ID │ chat list index │ cc:<id> (cloud, flow 3)
  │
  ▼
Forker (phase 2)
  │  writes the fork F: new ID, P's messages, profile, model, title and group,
  │  and a link back to P
  │  stderr: "fork <F> of <P>"
  ▼
send "task" to F, the way chat continue does
  │  request = P's last request, unchanged, + "task"
  ▼
provider
  │  P's part comes from the cache
  ▼
save the answer in F; F becomes the previous query
stdout: the answer, and "(4,812 cached)" in the usage line
```

P is never written. Holds: I1–I4, I10 · P4, P6, P7, P13.

### Any request to Claude through the Anthropic API

```text
Claude request (query, reply, chat continue, chat fork)
  │
  ▼
anthropic vendor (phase 1)
  │  adds one top-level field: "cache_control": {"type": "ephemeral"}
  ▼
Anthropic
  │  caches the request up to its last message
  │  reads what the chat's previous request cached
  ▼
usage: cached tokens ──▶ "(N cached)"
```

OpenAI, DeepSeek, xAI and Kimi need no change: they cache on their own. Claude through OpenRouter
stays uncached (A01). Holds: I1, I5, I6 · P1–P3.

### Continue from the cloud: `clai chat continue cc:X [prompt]`

```text
clai chat continue cc:X
  │
  ▼
sync engine: pull one conversation (phase 3)
  │  downloads X from clai.chat: chat file, reasoning files, images
  ├─ X not in the cloud ──── stop with an error naming `clai chat continue X`
  │
  │  compares the local X with the cloud copy (the merge `clai cloud pull` uses)
  ├─ no local X ───────────────── write the cloud copy
  ├─ same messages ────────────── write the cloud copy
  ├─ local is behind ──────────── move local X to cloud/trash/, write the cloud copy
  ├─ local has extra messages ─── save local X as fork L (new ID), write the cloud copy
  │
  │  records X as in sync, updates the chat index
  ▼
continue X the way `clai chat continue X [prompt]` does today (phase 4)
```

`clai chat fork cc:X "task"` stops after the pull and forks X. Holds: I7, I8 · P7–P10, P12.

### Show what `-re` continues: `clai chat list`

```text
clai chat list
  │
  ▼
read the previous-query record (read only)
  │
  ├─ names a chat ──────── line: "-re continues: 0 · fork verb plan"
  ├─ no record, or gone ── no line
  │
  ▼
draw the line, then the header and the rows
```

Holds: I9, I10 · P11.

## Status board

**Line:** waiting on human: root

| #   | Node | Parent | Class | Interface | Status |
| --- | ---- | ------ | ----- | --------- | ------ |
| 1   | Claude cache field | root | contract | The Messages API request body `internal/vendors/anthropic` sends: the top-level cache field (P1) on every request, everything else unchanged | Complete |
| 2   | Fork verb | root | contract | CLI `clai chat fork [query flags] <chatID\|index> <prompt>`; `internal/chat` `Forker`, the chat resolver `findChatByRef` and its `CloudResolver` seam for `cc:`, and `chatOwnSpend` | Complete |
| 3   | One-conversation pull | root | contract | `internal/cloud/sync`: pull one conversation, its reasoning files and its images from the cloud store and merge it with the local copy so the cloud copy keeps the ID, reusing `clai cloud pull`'s merge, trash, fork, index hook and base recording | Complete |
| 4   | Cloud references | root | contract | CLI `clai chat continue\|fork cc:<chatID>`: phase 2's `CloudResolver` filled by phase 3's pull, with the P8 error and the P10 notices | Complete |
| 5   | Chat list: previous-query line and group totals | root | contract | CLI `clai chat list`: the `-re continues:` line above the table, and group rows that total each member's own spend (I10) | Complete |
| 6   | Chat forks, caching and cloud continue | root | composition | root: the four Flows through `cli.Run`, with stand-in providers that record request bodies and `internal/cloud/cloudtest` as clai.chat | Reopened (review 3) |
| 6.1 | Query stats name the conversation | root | contract | `-qs` run summary: a `chat <id>` row naming the saved conversation (`internal/text/run_summary.go`) | Not Started |

Phases 1, 2, 3 and 5 touch disjoint files and can run as one wave once `previous-query-pointer` is
done; phase 4 consumes 2 and 3; phase 6 consumes all five.

## Scope

- Forking is a `chat` verb, `clai chat fork`; there is no `-fork` flag (maintainer, 2026-11-23,
  replacing the 2026-11-19 answer). Phase 6 rewrites `architecture/continue-from-claudex.md`'s "Forking
  (future)" and `architecture/chat-groups.md`'s forking section to describe the verb.
- No cache setting, flag, TTL choice or cache report beyond the existing "(N cached)" (I6).
- No OpenAI `prompt_cache_key` and no OpenAI explicit breakpoints: OpenAI caches by prefix on its own,
  and the key only steers routing (P3). No change for any vendor that already caches by prefix
  (DeepSeek 6,046 of 6,482 recorded turns cached, OpenAI 9,105 of 14,349, xAI 8 of 8, Kimi 8 of 9).
- No per-block Anthropic breakpoints: the one top-level field covers queries, replies and forks (P2).
- No caching for Claude through OpenRouter: its requests are built by the generic layer, which has no
  hook for a top-level body field, and the maintainer chose not to add one (A01).
- No Gemini explicit context caches, and no Gemini or Qwen through OpenRouter, which need explicit
  breakpoints there.
- No caching work in `photo`, `video` or `audio`.
- The worklog-orchestrate skill's primer and fork roles are a follow-up outside clai.
- `chat fork` takes a chat the way `chat continue` does (an ID, or a `chat list` index; an external row
  is cloned first, then forked), plus `cc:` (P7); it adds no other selection rules.
- `cc:` reads only the account's own cloud store. Conversations on the public Hub, a publish flow for
  them and any clai-cloud server change are out (maintainer Q3). A per-path lookup route that would
  replace P12's full change list is a clai-cloud follow-up.
- `cc:` never writes to clai.chat. It records `X`'s base in `cloud/state/` the way `clai cloud pull` does,
  so the next pull has nothing to do for `X`; a local fork it leaves is uploaded by the next
  `clai cloud commit` like any new chat (I7).
- `cc:` reuses `clai cloud pull`'s conversation merge with one difference in policy: the cloud copy keeps
  the ID and the local copy becomes the fork. `clai cloud pull` itself keeps its current policy.
- `chat list -json` output is unchanged; the line is part of the table's text output only.
- `-re`, `-dre`, `chat delete` and the previous-query record are `previous-query-pointer`'s; this effort
  only reads the record.

## Root facts

Invariants:

| ID | Statement | Binds |
| -- | --------- | ----- |
| I1 | Prefix stability: for one model and profile, a request that continues or forks a chat repeats the chat's previous request byte for byte (system prompt, tools in the same order, earlier messages, the cache field); only the new messages differ. | phases 1, 2 |
| I2 | `chat fork` never writes the parent's conversation file or its index row. | phases 2, 4 |
| I3 | A fork's ID is new (`internal/chatid.New`), its `Created` is the fork time, `Source` is `"clai"` and `SourceID` the parent's ID; every other field is the parent's: messages, `GroupKey`, `Title`, `Summary`, `Profile`, `OriginDir`, `Queries` (each with its model and cost), `TokenUsage` and `RecentTokenUsage`. The fork runs on the profile's model, as `chat continue` does (P13). The fork's file exists before any request is sent. | phases 2, 3 |
| I4 | N concurrent forks of one parent leave N complete chat files and N index rows; the previous-query record names one of them. | phases 2, 6 |
| I5 | The cache field lives in the `anthropic` vendor package; the generic layer (`internal/text/generic/`) and the `openrouter` vendor do not change (AGENTS.md, A01). | phase 1 |
| I6 | Caching has no flag or setting; the cache field is on every request the `anthropic` vendor sends. | phase 1 |
| I7 | After adoption of `X`, `conversations/X.json` holds the cloud store's bytes verbatim, `conversations/reasoning/X/` holds exactly the cloud store's sidecar files, every image `X.json` references exists under `conversations/assets/`, `X`'s index row describes the cloud copy, and `cloud/state/` records `X`'s base the way `clai cloud pull` does. | phases 3, 4 |
| I8 | Adoption loses no local message: a local `X` whose messages are not a prefix of the cloud copy's is kept whole as a fork (I3's stamping, parent `X`, with its reasoning sidecar copied) before `X.json` is replaced. That fork's `Created` is the `CreatedAt` of its first query the cloud copy lacks (the adoption time when it has none), so its local-only spend counts once under I10; a local `X` whose messages are a prefix goes to `cloud/trash/`. | phase 3 |
| I10 | A spend counts once per lineage: a chat's own spend is the cost and tokens of its queries created at or after its `Created`. A chat's own `chat list` row shows its whole history; a group row totals its members' own spend. | phases 2, 5 |
| I9 | `chat list` writes nothing: the line reads the previous-query record and never migrates, repairs or clears it (`architecture/config.md` read-only promise). | phase 5 |

Parameters:

| Parameter | Default | Decided by |
| --------- | ------- | ---------- |
| P1 Claude cache field | top-level `"cache_control": {"type": "ephemeral"}`, the 5-minute TTL | plan: forks start within minutes; the 1-hour TTL writes at 2× instead of 1.25× |
| P2 Where the field goes | every request of the `anthropic` vendor; none on `openrouter` requests; no block-level `cache_control` anywhere | plan, 2026-11-23 investigation (one field is Anthropic's automatic caching); maintainer, A01 (no OpenRouter) |
| P3 OpenAI cache key | none: no `prompt_cache_key` is sent | plan, 2026-11-23 investigation: OpenAI caches by default; the key is optional and only steers routing |
| P4 Fork notice | one stderr line `fork <new-id> of <parent-id>` at the default log level (a record under `-json`) | plan, to be confirmed at README sign-off |
| P5 Claude cost of always-on | a request over the model's minimum cacheable length (512 tokens on the Claude 5 generation, 1,024 to 4,096 on older models) writes its uncached tail at 1.25× once; later turns, replies and forks read it at 0.1× (0.05× on Opus 5.5 and Sonnet 5.5). A one-shot query that is never continued pays the 1.25× and reads nothing. | maintainer Q2 (automatic) |
| P6 `chat fork` without a prompt | refused before any write or request: exit 1, `chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'` | plan: a fork with no new turn is an exact copy; `chat continue` already previews |
| P7 `cc:` on `chat fork` | accepted: the parent is adopted first (flow 3), then forked | plan: one chat resolver for both verbs |
| P8 `cc:X` not in the cloud store | exit 1, `clai.chat has no conversation X; 'clai chat continue X' continues the local one`; nothing written | maintainer Q4 |
| P9 When local `X` becomes a fork | only when its messages are not a prefix of (or equal to) the cloud copy's messages | maintainer Q5 |
| P10 Adoption notices | stderr, default log level: Adopted `cc:X: taken from clai.chat`; Replaced `cc:X: replaced a shorter local copy (kept in cloud/trash/<file>)`; LocalForked `cc:X: taken from clai.chat; the local copy is now <L>`; Unchanged prints nothing | plan |
| P11 List line | above the header on every render, plain text, also under `-r`: `-re continues: <index> · <label>` for its own row; `-re continues: <index> [group] · <label>` when a collapsed group row holds it; `-re continues: <chatID> · <label> (not in this view)` when a filter hides it; no line when there is no record or its conversation is gone; `-re continues: unreadable record (<reason>)` when the record cannot be read, and the list still draws | maintainer Q6 (a line above the table), plan (wording) |
| P13 Fork's model | the profile's model, as for `chat continue`; no model is carried over from the parent, and `-cm` chooses one | maintainer, F6-01 (2026-11-23): a chat records only the bare model name, not its vendor route, so a carried-over model can reach the wrong vendor; a fork of a chat run with `-cm` reads the cache only when `-cm` is repeated |
| P12 Cloud lookup | page the cloud store's full change list (`allChanges(conversations, 0)`) and fetch only `X`'s blobs | plan: no server change (Scope) |

## Validation policy

### Gates

```sh
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa
go vet ./...
go run github.com/mibk/dupl@latest -t 80 .   # no clone group beyond the count at the effort's start
```

Golden regeneration: the e2e goldens are inline expectations in `e2e/*_test.go` and are edited by hand;
`internal/chat/testdata/persister/*.golden` and `e2e/testdata/diagnostics/*.golden` regenerate as their
tests document (`go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens`).
Phase 6's Golden diff names each one that changes (at least `chat -h`, `chat help`, and every `chat list`
expectation, which gains the P11 line when a record exists).

### Human checks

- **root, caching:** with funded keys, prime a chat of at least 5k tokens and run two forks
  (`clai -r -qs chat fork -cm <model> <id> <task>`, `-cm` matching the parent's model) on each of: a Claude model direct, an OpenAI model and a DeepSeek model. Pass: each fork's "(N cached)" covers at least 90 % of its parent's
  prompt. Record the numbers in a dated note in the notebook's `codebases/imago/clai/`. On 2026-11-19
  the shell's `ANTHROPIC_API_KEY` had no credit; the direct Claude check needs a funded key.
- **root, cloud:** logged in to clai.chat with a conversation store, from a second config dir
  (`CLAI_CONFIG_DIR`) commit a chat `X`, then on this machine: `clai chat continue cc:X` with no local
  `X` (Adopted), again after continuing `X` on the second dir and committing (Replaced), and again after
  adding a different turn locally (LocalForked: `chat list` shows `X` and the fork `L`). Pass: the notices
  match P10, and `clai cloud pull conversations` afterwards reports nothing to do for `X`.

## Findings

| ID  | Severity | Node | Owner | Status | Summary |
| --- | -------- | ---- | ----- | ------ | ------- |
| A01 | design andon | 2 | maintainer | Closed (phase dropped) | Phase 2 cannot send the top-level `cache_control` without changing `internal/text/generic/` (I5): `createRequest` marshals the private `req` struct (no `cache_control` field), the HTTP client is private (`s.client`, set in `Setup`), and `OpenRouter` only reaches the body through `OutputModalities`, `ReasoningEffort` and `ExtraHeaders`. Needs a decision: (a) amend I5 to allow one vendor-agnostic generic hook, e.g. `StreamCompleter.ExtraBody map[string]any` merged into the request (the `OutputModalities` pattern), or (b) openrouter gets its own request builder and stream path (large duplicate of generic). Recommended: (a). Phases 1, 3, 4, 6 are unaffected; 5 and 7 consume 3/4 and 2 respectively. |
| V1-01 | major | 4, 5 | plan | Closed +wip | check 2 · phase 4 `AdoptionError.Step AdoptionStep` and its constants (`StepForkLocal`, `StepTrash`, `StepWriteChat`, `StepWriteSidecar`, `StepWriteAsset`, `StepIndex`) are used but never declared; phase 5 `previousQueryState.Kind previousQueryKind` and `prevNone`/`prevNamed`/`prevUnreadable` are used but never declared; phase 5 `const chatIndexVersion = /* current + 1 */` is not a value (the tree holds 3, so 4). `go build` of the assembled blocks fails on both types. Declare them and write `4` |
| V1-02 | blocker | 4 | plan | Closed +wip | check 3 · phase 4 test row `TestAdopter_forkCreated` (acceptance row 4) calls `chatOwnSpend`, declared by phase 5 and absent from phase 4 `Consumes`; phase 4 cannot compile that test before phase 5. Assert `L.Created` and the lacking queries instead, and leave the `chatOwnSpend` assertions to phase 5 or phase 6 (`Test_e2e_cloud_continue_localForkTotals`), or renumber so phase 5 precedes phase 4 |
| V1-03 | major | 4 | plan | Closed +wip | check 3 · phase 4 changes the body and help of `(*ChatHandler).fork` (resolver `findChatByID` becomes `findChatByRef`, `fork` synopsis gains `cc:`), a member phase 2 declares with clause `fork.2` ("never falls back"); phase 4 is not an injected phase. Either phase 2 declares the resolver seam (`findChatByRef` with only the local branch) so phase 4 supplies the cloud branch, or phase 4 becomes an injected phase 2.1 |
| V1-04 | major | 4, 6 | fix | Closed +wip | check 4 · malformed tables. Phase 4 acceptance row 4 runs into a test-case row: `TestAdopter_forkCreated` (proves Adopter.5, I8, I10) has no `Test cases` row, and the stray line `\| TestAdopter_forksDivergedLocal \|` follows it. Phase 6 acceptance row 11 runs into `Test_e2e_chat_fork_providerError`, which `Error management` names but `Test cases` lacks, and the stray `Test_e2e_chat_fork_thenReContinuesFork` line follows. Split each into a clean criterion row and a real `Test cases` row (criterion 11 proves flow 1 through `Test_e2e_chat_fork_thenReContinuesFork`) |
| V1-05 | major | 1, 2, 4, 6 | fix | Closed +wip | check 4 · invariants not cited by a clause on each member they bind: I5 (phase 1: no clause, only acceptance row 8), I10 (phase 2: no `Forker` or `fork` clause), I2 (phase 4: no `ref` or `Adopter` clause, though `Test_forkCloud_adoptsThenForks` proves it), and phase 6's `root.N` scenario clauses cite none of I1, I4, I7, I8, I9, I10 they prove. Add the citations to the clauses |
| V1-06 | major | 1, 3, 4, 5, 6 | fix | Closed +wip | check 4 · (a) members with no clause id and `Proves` entries naming no declared clause, invariant or criterion: `asset.RefsIn` and `error pass-through` (phase 3), `MoveToTrash` and `ErrCloudCopyUnreadable` (phase 4), `chatIndexVersion` (phase 5), `existing TokenUsage contract` (phase 1 row 7); (b) phase 6 declares `root.1` to `root.10` but `Test_e2e_cloud_fork` proves `root.12`, and `root.9` is used for both the list line and the refusals (declared: refusals are `root.8`); (c) acceptance criteria with no `Test cases` row: phase 1 rows 7 and 8, phase 3 row 9, phase 5 row 13, phase 6 rows 13 and 15. Add clauses or rows, or re-point `Proves` |
| V1-07 | major | 1 | fix | Closed +wip | check 4 · `constructRequest.1` and row `Test_constructRequest_cacheControl` require a request "with extended thinking", but `claudeReq` (`internal/vendors/anthropic/claude.go`) has no thinking member and nothing in `constructRequest` sets one, so that case cannot be triggered. Drop the thinking variant from the clause and the table |
| V1-08 | major | 3 | plan | Closed +wip | check 4 · `TestConversationReader_fetchesOnlyItsBlobs` asserts the hashes the server saw on `/api/v1/blobs/fetch`, but `cloudtest.Server` only counts calls per path (`Requests map[string]int`) and records no hashes; the fixture is not declared in phase 3. Declare the addition to `internal/cloud/cloudtest/server.go` (for example `FetchedHashes []string` under the server lock) in the Interface and `Consumes`, or assert through a recording `http.RoundTripper` |
| V1-09 | minor | 5 | plan | Closed +wip | check 7 · phase 5 bundles two interfaces: the read-only `-re continues:` line (`previousQueryLine`, `readPreviousQueryState`) and the own-spend accounting with its index schema bump (`chatOwnSpend`, `OwnSpend`, `chatIndexRow` fields, `chatIndexVersion`, group totals). Consider splitting at the next re-plan; the verdict is unaffected |
| V2-01 | major | 1 | fix | Closed +wip | check 4 · phase 1 clause `constructRequest.5` states where code lives (which packages change), not caller-observable behaviour, and its row `Test_noDiffOutsideVendor` runs a version-control diff against `main` inside a test, which needs the VCS tool and a `main` ref and breaks the hermetic-test rule (`CLAUDE.md`, `testenv`). Drop the clause and the test and keep I5 as a Scope fact checked by review, or restate I5 as behaviour: the OpenRouter request body is byte-identical (the existing openrouter request tests) |
| V2-02 | major | 6 | fix | Closed +wip | check 4 · phase 6 refers to clause texts that moved. `Test_e2e_cloud_refusals` Then names "the ref.5 text, the ref.4 text" and Error management says "the phase 4 texts" for `cc:` on `chat delete` and `cc:` with no ID; those refusals are phase 2's `chatref.6` and `chatref.4` (phase 4's `ref.4` is now the notices and `ref.5` the untouched record). Re-point them, and add `chatref.4` to `Proves` |
| V2-03 | minor | 2 | plan | Open | check 7 · phase 2 now carries four interfaces: the `chat fork` verb with `Forker`, the chat resolver with the `CloudResolver` seam and `ChatRef` (`chatref.1` to `chatref.7`, including the `chat delete cc:` refusal), and the own-spend function `chatOwnSpend`. Closing V1-09 moved the bundle here. Consider moving the resolver seam and `chatOwnSpend` to their own leaf phases at the next re-plan; the verdict is unaffected |
| V5-01 | major | 3 | fix | Closed +wip | check 4 · `PullConversation.3` and `.5` disagree with the code they reuse. `mergeChats` (`internal/cloud/sync/merge.go`) returns `keepLocal` also for two copies with the same messages when the local one has a title and the cloud one has none (`a.Title != "" && b.Title == ""`). `.5` sends every `keepLocal` to `ConversationLocalForked`, so such a pair forks a duplicate chat although the local copy holds no message the cloud lacks, against P9, Q5 and I8. State the rule on message counts (local has more messages, or the messages diverge, forks; the same messages are Unchanged whatever `mergeChats` says) and add a row for the title-only pair to `TestPullConversation_unchanged` |
| V5-02 | major | 3 | plan | Closed +wip | check 5 · I3 says a fork's ID is new through `internal/chatid.New` (a UUIDv7) and I8 says the adoption fork follows I3's stamping, but `forkCopy.1` mints the ID with `newChatID`, which has the legacy `<unix hex>-<20 hex>` shape of `chat.NewChatID`. Either amend I3 and I8 to allow that shape for the fork `PullConversation` keeps, or make `forkCopy` take the ID from `chatid.New` (`internal/chatid` imports only `diag`, so `sync` may import it) |
| V5-03 | major | 3, 4 | plan | Closed +wip | check 4 · Q9 moved the adoption fork and the merge into phase 3 but the README Binds were not moved: I8 still binds phase 4 only and I3 binds phases 2 and 4. No clause of phase 4 cites I8 or I3, and phase 3's clauses cite I8 (`PullConversation.4`, `.5`) though it is not bound. Re-bind I3 and I8 to phases 2 and 3 and 3 respectively (and keep I7 on 3 and 4) |
| V6-01 | major | 3 | fix | Closed +wip | check 4 · phase 3 acceptance row 6 runs into a test-case row again: `TestPullConversation_forkID` (proves PullConversation.5, I3; cited by acceptance row 4) has no row in `Test cases`, row 6's own proof is shown as `TestPullConversation_forkID` instead of `TestPullConversation_writesExactly`, and a stray `\| TestPullConversation_writesExactly \|` line follows. The newID-failure case of `PullConversation.5` ("wrapped error, nothing changed") therefore has no row, and `Error management` names no test for it. Split the row into a clean criterion 6, add the `TestPullConversation_forkID` row to `Test cases`, and add an `Error management` row for a failing `Engine.newID` |
| V8-01 | major | 3 | fix | Closed +wip | check 4 and 9 · no step owns `*LocalConversationError`: `settle(local, cloud []byte) ConversationOutcome` has no error return and `settle.1` says "No I/O", yet the Errors table and Error management cite `settle.1` for a local chat file that cannot be read or decoded, and `mergeChats` answers `keepBoth` for an undecodable local file, which would fork it instead of failing. Likewise Error management cites `fetch.1` for an invalid ID with "no request", but `fetch.1` says nothing of ID validation (the old `PullConversation.2` did). Assign the local read and decode check and the `Allowed` ID check to a step with a clause (an error return on `settle`, or a check at the start of `fetchConversation`) and re-point the citations |
| V8-02 | major | 3 | fix | Closed +wip | check 4 · `TestForkCopy` expects "the title kept or falling back to `firstUserText`", but `forkCopy.1` says every member except what the stamp sets is unchanged and the `forkStamp.Title` comment now says only "replaces `title` when set"; the fallback rule (old text: empty stamp title keeps the title, falling back to `firstUserText`) was lost in the restyle, so a title-less local chat forked by `PullConversation` is untitled by the clause and titled by the test. State where the fallback lives (in `forkCopy`, or in `forkTheirs`, which then passes the title) in a clause |
| V8-03 | major | 3 | fix | Closed +wip | check 4 · clauses `PullConversation.1` (steps in order, nothing local changes before every blob is held) and `PullConversation.3` (no local message is lost when the cloud files are written) appear in no `Test cases` row's `Proves` and in no acceptance row; only Error management cites them. Add them to `TestPullConversation_failures` and `TestPullConversation_forksLocal` or `TestPullConversation_replacesShorterLocal` (and to a criterion) |
| A02 | design andon | 1 | maintainer | Closed (maintainer: snapshots approved) | Phase 1 changes the anthropic request body, which three goldens outside the phase's code paths encode byte for byte: `internal/text/testdata/turn_text_only/request-1.json` and `request-2.json` (`TestTurn_TextOnlyUnchanged`, `internal/text`) and `internal/vendors/testdata/image_encoder/anthropic.text.golden` (`TestImageEncoderContract/anthropic/TextUnchanged`, `internal/vendors`). Both now fail at `make qa` because the body ends with `,"cache_control":{"type":"ephemeral"}`. The phase says Golden diff: None and names only `internal/vendors/anthropic/`. Needs a decision: amend phase 1 (Golden diff and code paths) to append the member to those three goldens (the only change; recommended), or exclude anthropic from them. Implementation in `internal/vendors/anthropic/` is done and its own tests pass. |
| R1-01 | minor | 3 | fix | Closed +wip | review 1 · no test fails `trash`, the fork write or the `X.json` write, though Error management names the no-loss outcome for them; `writeCloud` 71.4%, `forkLocal` 80.0% (`internal/cloud/sync/conversation.go:289-331,385-406`) |
| R1-02 | minor | 3 | fix | Closed +wip | review 1 · `collectAssetRefs` error text changed (`scan <path>: scan for asset refs: ...` instead of `scan <path> for asset refs: ...`) though the test row says it must not; nothing pins it (`internal/chat/persister.go:275-278`) |
| R1-20 | minor | 2 | fix | Closed +wip | review 1 · `internal/chat/handler_fork_test.go:292` `Test_fork_noticeLine` has no `-json` variant, which acceptance row 7 requires (chat-subsystem record, stdout only records) |
| R1-40 | minor | 4 | fix | Closed +wip | review 1 · a pull that wrote files and then errored (pruneTrash, hookPulled) drops the notice, so a kept local copy L or the trash file is not named anywhere; wrap the error with the P10 notice (internal/chat/cloud_resolver.go:36-42) |
| R1-60 | minor | 5 | fix | Closed +wip | review 1 · the interactive clear of the previous-query line has no test (internal/chat/list_continues.go:86-92); arithmetic read as right |
| R1-61 | minor | 5 | fix | Closed +wip | review 1 · phase 5 Golden diff says None but the three persister goldens changed (version 4, own_total_* only: legitimate); amend the row, and index_test.go:584 uses literal 4 instead of chatIndexVersion |
| R1-62 | minor | 5 | fix | Closed +wip | review 1 · TestChatList_writesNothing does not chmod the config dir read-only (internal/chat/list_continues_test.go:195); no wrapped ErrPreviousQueryGone or invalid-ID record row |
| R1-63 | minor | 5 | fix | Closed +wip | review 1 · I10 for a cc: LocalForked copy is proved only with hand-built rows; add a test through forkCopy, chatIndexRowFromChat and collapseGroupRows (internal/chat/list_continues_test.go:363) |
| F6-01 | major | 2 | fix | Closed +wip (resolution: maintainer dropped P13's model carry-over) | phase 6 · a fork of an OpenRouter chat runs on the wrong vendor: `LastQueryModel` returns the recorded bare model (`anthropic/claude-x`), which `-cm` routes to the anthropic vendor instead of `or:anthropic/claude-x` (P13 passes the recorded model as `-cm`); the same for any route-prefixed model (`berget:`, `novita:`, `hf:`). `Test_e2e_openrouter_claude_isNotCached` names `-cm or:...` on its fork to stay green; a plain fork fails with a missing `ANTHROPIC_API_KEY` |
| R2-20 | minor | 2 | fix | Closed +wip | review 2 · `phase-2-fork-verb.md:7` Goal says the prompt is sent on the parent's model and `fork.6` (line 42) says same profile and model, against P13 and `fork.5` (profile's model) |
| R2-80 | minor | 6 | fix | Closed +wip | review 2 · phase 6 text disagrees with the shipped scenarios: acceptance row 1 and two test rows say stdout and prompt plus reply; acceptance row 3 and the last Error management row run the `Test_e2e_chat_fork_model` row into themselves (phase-6 file lines 97, 102, 119, 130, 141-142) |
| R2-81 | minor | 6 | fix | Closed +wip | review 2 · `indexRows`, the tree walk, `forkNoticeLine` and the text-config setup are duplicated across `e2e` and `e2e/remote`; move to `e2etest` (e2e/remote/cloud_continue_e2e_test.go:383, e2e/chat_fork_e2e_test.go:108) |
| R2-82 | minor | 6 | fix | Closed +wip | review 2 · multi-line rationale comments against the one-line rule (internal/cli/cli.go:310,329; e2e/chat_fork_e2e_test.go:353-367; e2e/remote/cloud_continue_e2e_test.go:183,367,371) |
| R2-83 | minor | 6 | fix | Closed +wip | review 2 · `Test_e2e_chat_fork_concurrent` does not assert the four processes overlapped (e2e/chat_fork_e2e_test.go:380) |
| R2-84 | minor | 6 | fix | Closed +wip | review 2 · `Test_e2e_chat_fork_parentUntouched` does not assert the inherited `TokenUsage`, `RecentTokenUsage` and `Summary` (e2e/chat_fork_e2e_test.go:239) |
| R3-01 | major | 6 | plan | Open | review 3 (root) · with every tool (`-t '*'`, or a profile with `use_tools` and no list) or a tool glob, the offered tools are registered by ranging over a map (`internal/text/querier_setup_tools.go:638,707`, `internal/tools/registry.go:55`), so the request's `tools` order changes in every process and a fork or `-re` of such a chat reads 0 cached tokens on Claude, OpenAI and DeepSeek (I1, fork.6, root.1, Goal). Probe: three `clai -r -t '*' q hi` runs sent the same 36 specs in three orders. Offer tools in name order and add a fork scenario with `-t '*'` and with a glob; the files belong to no phase |
| R3-02 | minor | 6 | fix | Open | review 3 · the help and notes say only `-cm` must be repeated (`internal/chat/cmd.go:113-114`, `architecture/chat.md:306`, `continue-from-claudex.md:948`, `streaming.md:178-179`); `-t` and `-lb` also set the request's tools, so a fork without `-t` of a chat primed with `-t` reads no cache |
| R3-03 | minor | 4 | fix | Open | review 3 · `chat continue -h` and `chat fork -h` say only that `cc:<chatID>` names the clai.chat copy (`internal/chat/cmd.go:103-104,116`); nothing says the cloud copy takes the ID, a local copy with extra messages becomes a new chat, a shorter one goes to `cloud/trash/`, or that `clai cloud pull` keeps the local ID |
| R3-04 | minor | 6 | fix | Open | review 3 · `architecture/cloud.md:117-118` labels the same-messages case Adopted; it is Unchanged with no notice (`conversation.go:282`, `cloud_resolver.go:189`), and a same-message local copy that differs elsewhere goes to `cloud/trash/` unmentioned (`conversation.go:289-296`) |
| R3-05 | minor | 2 | fix | Open | review 3 · `architecture/chat.md:300` puts `### chat fork` between `chat continue` and its preview format and replyable notice (`:309`, `:321`), which now read as part of fork; `:347` cites the worklog invariant ID I9 |
| R3-06 | minor | 5 | fix | Open | review 3 · `internal/chat/list_continues.go:83,91` drops the line's write error and the `ClearTermTo` error, which `listChats` returns everywhere else (`handler_list_chat.go:490,561,1088`) |
| R3-07 | minor | 6 | fix | Open | review 3 · multi-line doc comments in `e2e/e2etest/providers.go:35-38,54-55,140-141` (one-line rule) |
| R3-08 | minor | 3 | fix | Open | review 3 · `internal/cloud/sync/conversation.go:114` cites "(P11)", the list line; the pull's parameters are P9 and P12 |
| R3-09 | minor | root | plan | Open | review 3 · Flow 1 (`README.md:65`) says stdout carries "(4,812 cached)"; it is in the `-qs` run summary on stderr |
| R3-10 | minor | root | maintainer | Open | review 3 · the index differs from the working tree: the staged `e2e/testdata/diagnostics/help.golden` drops HEAD's `audio v` voices entry and the staged `internal/cli/cli.go` keeps the comments R2-82 trimmed; a commit of the index alone fails `TestDiagnosticOutputGoldens`. Stage both (and the untracked files) before committing |
| R3-11 | minor | root | maintainer | Open | review 3 · Human checks "root, caching" and "root, cloud" have no journal record; the caching check runs without `-t`, so it does not exercise R3-01 |

## Session journal

- 2026-11-19 · plan · evidence: clai's conversation history (`~/.config/.clai/conversations`) holds 25 Claude turns (15 `claude-*` direct, 10 `anthropic/claude-*` through OpenRouter), none with cached tokens, 13 of them over 2k prompt tokens; DeepSeek 6,046 of 6,482 cached, OpenAI 9,105 of 14,349, xAI 8 of 8. `grep cache_control` finds no marker in any vendor. A Claude Code prime-and-two-forks run on Haiku read 51,542 of the primer's 51,544 tokens from cache in each fork.
- 2026-11-19 · maintainer · Q1 scope: forking plus prompt caching that works on Anthropic, OpenAI and DeepSeek; the orchestrator skill is a follow-up
- 2026-11-19 · maintainer · Q2: a flag, `clai -fork <chat-id> query`; "the rest is automatic on all vendors" (no cache setting; Claude needs markers to make it so)
- 2026-11-19 · plan · README drafted (in the notebook, `projects/clai/2026-11-19-chat-forks/`)
- 2026-11-23 · maintainer · the fork is the verb `clai chat fork <id> <prompt>`, not a flag (replaces Q2's flag); add a `chat list` marker for the previous query, and `clai chat continue cc:<id>`
- 2026-11-23 · maintainer · Q3: `cc:` reads the account's own conversation store, not the Hub
- 2026-11-23 · maintainer · Q4: `cc:X` missing from the cloud store is an error naming the local command
- 2026-11-23 · maintainer · Q5: the local copy becomes a fork only when it holds messages the cloud copy lacks
- 2026-11-23 · maintainer · Q6: the marker is a line above the table
- 2026-11-23 · plan · README amended: Goal, Glossary, Flows, Status board (phase 4 is the verb; phases 5–7 added; composition renumbered 8), Scope, Root facts (I2, I4 reworded; I7–I9, P6–P12 added), Human checks; moved into the repository
- 2026-11-23 · maintainer · asked whether manual cache keys are needed at all, expecting caching to be automatic
- 2026-11-23 · plan · investigation: OpenAI (developers.openai.com/api/docs/guides/prompt-caching): "Prompt caching is enabled by default for supported OpenAI models"; `prompt_cache_key` is optional and "Keys influence routing; they do not pin requests to a machine or guarantee a cache hit". Anthropic (platform.claude.com/docs/en/build-with-claude/prompt-caching): "Messages API: Opt in with `cache_control`, at the top level or on blocks"; the top-level field is automatic caching, one breakpoint on the last cacheable block moved forward as the chat grows, 20-position lookback, 5-minute TTL. OpenRouter (openrouter.ai/docs/features/prompt-caching): OpenAI, DeepSeek, Grok and Moonshot cache automatically; Anthropic needs `cache_control` and the top-level field is supported; sticky provider routing after a cached request (60 minutes); cached tokens in `prompt_tokens_details.cached_tokens`. Matches clai's history: OpenAI and DeepSeek turns cached with no key, Claude turns never.
- 2026-11-23 · plan · README amended: OpenAI cache key phase dropped (P3 none); Claude phases send the one top-level field instead of four block markers (P1, P2); phases renumbered 1–7; Goal, Glossary, Flows (OpenAI flow removed), I1, I5, I6, P5, Scope
- 2026-11-23 · plan · README amended: Flows rewritten as numbered steps in plain words, at the maintainer's request (the arrow diagrams were too dense to read)
- 2026-11-23 · plan · README amended: Flows back to diagrams at the maintainer's request, in the vertical tree style of `architecture/continue-from-claudex.md`: one step per line, branches for decisions, references on one line under each
- 2026-11-23 · maintainer · README signed
- 2026-11-23 · plan · phase 2 · A01 (design andon): generic layer has no hook for a top-level request field; phase files not written, line stopped
- 2026-11-23 · maintainer · Q7: a fork keeps the parent's query statistics (per-query cost and model, token totals), and each spend counts once in group totals
- 2026-11-23 · plan · README amended: I3 (fork copies every field but ID, Created, Source, SourceID), I10 and P13 added, phase 6 node and interface extended to group totals and the index's own-spend fields
- 2026-11-23 · maintainer · A01 closed · re-plan: Claude caching through OpenRouter dropped rather than adding a generic body hook
- 2026-11-23 · plan · README amended: phase 2 (OpenRouter Claude cache field) removed, phases renumbered 1–6; Goal, Claude flow, Scope, I5, I6, P2, binds, Human checks; line running
- 2026-11-23 · plan · phase files 1-6 written from the signed README; no facts or flows changed. Choices the README left open: fork sends with no preview and binds the directory to the fork; equal-message adoption rewrites the cloud bytes without a trash copy; chat->cloud/sync import for reader types only (alias cloudsync); asset-ref scan moves to internal/asset (phase 3) and trash move to sync.MoveToTrash (phase 4) to avoid clones
- 2026-11-23 · maintainer · Q8: a fork that adoption keeps counts its spend from where it split from the cloud copy
- 2026-11-23 · plan · README amended: I8 (an adoption fork's `Created` is its first query the cloud copy lacks)
- 2026-11-23 · plan · phase 2 Forker gains ForkAt (Created override), phases 4, 5, 6 updated for the amended I8 (adoption fork Created = first query the cloud copy lacks); no tree change
- 2026-11-23 · validate · V1 Not ready · go build (assembled interface blocks with stubs, scratchpad): 2 undefined types and 1 placeholder parse error; go list -deps ./internal/cloud/sync: no import of internal/chat; inspection of consumed members against internal/chat, internal/cloud, internal/cloud/cloudtest, internal/vendors/anthropic, internal/text, e2e/e2etest: all found except chatOwnSpend (phase 5 only) and a fetched-hash record in cloudtest · notes: 9
- 2026-11-23 · plan · phases 2, 3, 4, 5 re-planned for V1-01, V1-02, V1-03, V1-08, V1-09: phase 2 declares chatOwnSpend and the CloudResolver seam (findChatByRef, CommandDeps.CloudResolver) that phase 4 fills; phase 3 declares cloudtest FetchedHashes; phase 5 interface is the CLI output, index fields and chatIndexVersion 4 moved to Local facts; AdoptionStep and previousQueryKind declared. No tree change
- 2026-11-23 · fix · V1-04, V1-05, V1-06, V1-07 closed (documents only; phase 1 thinking case dropped, claudeReq has no thinking member): table repairs in phases 4 and 6, invariant citations and clause ids added; gates not run, no code changed
- 2026-11-23 · validate · V2 Not ready · go build (assembled interface blocks of phases 1-6 with stubs, scratchpad): type-checks, only the stub bodies missing; go list -deps ./internal/cloud/sync: no import of internal/chat; inspection of consumed members against the repo (ComputeGroupKey, errIndexOutOfRange, cloudtest.Server.Lock, blob routes found): V1-01 to V1-05, V1-07 to V1-09 verified closed, V1-06 regressed (`chatIndexVersion`, `the FetchedHashes addition`, `error management` still in `Proves`), V2-01 to V2-03 filed · notes: 2
- 2026-11-23 · fix · V1-06, V2-01, V2-02 closed (documents only): Proves entries re-pointed to declared clauses (FetchedHashes.1, list.6, root.13, fork.9), phase 1 I5 cited from constructRequest.1 with a command proof, phase 6 refusal citations re-pointed, status board cells for phases 2, 4, 5 updated; V2-03 left open; gates not run
- 2026-11-23 · validate · V3 Not ready · go vet (assembled interface blocks of phases 1-6 with stubs, scratchpad): type-checks, only stub bodies missing; inspection of every Proves entry, clause and criterion row: V1-01 to V1-09, V2-02 verified closed, V2-01 regressed (phase 1 acceptance row 8 proves `constructRequest.1` and I5 by a command that has no `Test cases` row, and `constructRequest.1` still states where the field is added, which is not caller-observable); V2-03 stays open (minor) · notes: 0
- 2026-11-23 · fix · V2-01 closed · constructRequest.1 states I5 as behaviour (no other vendor body changes); command row for the no-diff check added · no gates (documents only)
- 2026-11-23 · validate · V4 Ready · go vet (assembled interface blocks of phases 1-6 with stubs, scratchpad): type-checks, only stub bodies missing; inspection of all six phase files against checks 1-10: V1-01 to V1-09, V2-01 and V2-02 verified closed, V2-03 stays open (minor, check 7) · notes: 0
- 2026-11-23 · maintainer · Q9: `cc:` reuses the sync engine's conversation merge (`clai cloud pull`) instead of a separate Adopter in `internal/chat`
- 2026-11-23 · plan · README amended: Flow 3, status board rows 3 and 4, Scope (`cc:` records the base in `cloud/state/`; policy difference from `clai cloud pull`), I7
- 2026-11-23 · plan · phase 3 re-planned (Q9): ConversationReader replaced by Engine.PullConversation reusing Pull merge/trash/setBase/index hook, forkTheirs generalised to forkCopy, asset.RefsIn kept, MoveToTrash dropped; phase file renamed phase-3-one-conversation-pull.md
- 2026-11-23 · plan · phase 4 re-planned (Q9): Adopter removed, phase 4 is NewCloudResolver over phase 3 pull, P8 error and P10 notices; phase 2 loses ForkAt and ErrForkCreatedZero (no consumer); phases 5 and 6 re-pointed; statuses stay Not Started, no tree change
- 2026-11-23 · validate · V5 Not ready · go vet (assembled interface blocks of phases 1-6 with stubs, scratchpad): type-checks, only stub bodies missing; reuse claims checked against internal/cloud/sync (Engine.fetch nil-cache safe, trash, pruneTrash, hookPulled, setBase, linkage, abs, mergeChats, forkTheirs, newChatID, loadState, state.save, StateError, Hooks.Pulled, allChanges, writeFile, removeFile found with usable signatures; Pull itself untouched by the plan; cursor and commit left alone so Pull skips X by base rev) and against cli.go and cloudcmd runner for the Engine and Hooks: V1-01 to V1-09 and V2-01 to V2-02 still hold, V2-03 stays open (minor), V5-01 to V5-03 filed · notes: 4
- 2026-11-23 · plan · README amended for V5-03: I3 binds phases 2 and 3, I8 binds phase 3 (the pull stamps the kept fork); status row 2 no longer names `ForkAt`
- 2026-11-23 · plan · phase 3 re-planned for V5-02, V5-03 (the kept fork takes its ID from chatid.New, passed into forkCopy; I3 and I8 cited in phase 3, dropped from phase 4, Forker dropped from phase 4 Consumes)
- 2026-11-23 · fix · V5-01, V5-02, V5-03 closed (documents only): equal-message keepLocal is Unchanged with trash, manyBlobs test dropped, sidecar base removal and st.Hashes stated, Consumes completed; gates not run
- 2026-11-23 · validate · V6 Not ready · go vet (assembled interface blocks of phases 1-6 with stubs, new forkCopy signature, scratchpad): type-checks, only stub bodies missing; go list -deps ./internal/chatid: imports no package that imports internal/cloud/sync; reuse check against internal/cloud/sync repeated (mergeChats keepLocal title case, forkTheirs, setBase, state.save, trash, hookPulled found as claimed): V5-01 to V5-03 verified closed, V1-01 to V1-09 and V2-01 to V2-02 still hold, V2-03 stays open (minor), V6-01 filed · notes: 0
- 2026-11-23 · fix · V6-01 closed · phase 3 acceptance row 6 proves by `TestPullConversation_writesExactly`; `TestPullConversation_forkID` restored as a Test cases row; Error management row for an `Engine.newID` failure · no gates (documents only)
- 2026-11-23 · validate · V7 Ready · go vet (assembled interface blocks of phases 1-6 with stubs, scratchpad): type-checks, only stub bodies missing; go list -deps ./internal/chatid: no cycle with internal/cloud/sync; reuse check against internal/cloud/sync as in V6; inspection of all six phase files against checks 1-10: V1-01 to V1-09, V2-01, V2-02, V5-01 to V5-03 and V6-01 verified closed, V2-03 stays open (minor, check 7) · notes: 0
- 2026-11-23 · plan · phase 3 references re-pointed to the rewritten interface clauses (fetch, settle, putAside, writeCloud, record, PullConversation.1-5, forkCopy.1-3); phase 2 Forker clauses restyled as bullets, IDs unchanged; no meaning change
- 2026-11-23 · validate · V8 Not ready · go vet (assembled interface blocks of phases 1-6 with stubs, new step signatures fetchConversation, settle, putAside, writeCloud, recordInSync, cloudConversation, RemoteFile, scratchpad): type-checks, only stub bodies missing; round-7 text compared clause by clause with the restyled phase 3 (lost or unowned: LocalConversationError owner, forkCopy title fallback, proof of PullConversation.1 and .3); V1-01 to V1-09, V2-01, V2-02, V5-01 to V5-03, V6-01 still hold, V2-03 stays open (minor), V8-01 to V8-03 filed · notes: 3
- 2026-11-23 · fix · V8-01 V8-02 V8-03 closed · phase 3: ID and sync-state checks before any request (PullConversation.6), local read before settle (PullConversation.7), recordInSync takes the state read first, forkCopy title fallback restored, PullConversation.1 and .3 cited by tests and criteria · no gates (documents only)
- 2026-11-23 · validate · V9 Ready · go vet (assembled interface blocks of phases 1-6 with stubs, recordInSync(st *state, ...) included, scratchpad): type-checks, only stub bodies missing; reuse check against internal/cloud/sync unchanged (loadState first as Pull does); every clause of phase 3 (PullConversation.1-7, fetch, settle, putAside, writeCloud, record, forkCopy, RefsIn, FetchedHashes) has a Proves entry; V1-01 to V1-09, V2-01, V2-02, V5-01 to V5-03, V6-01, V8-01 to V8-03 verified closed, V2-03 stays open (minor, check 7) · notes: 0
- 2026-11-23 · maintainer · interfaces 1–6 signed
- 2026-11-23 · orchestrate · fleet 1: phases 1, 2, 3 In Progress, each in its own git worktree from 615303b; gates run as make qa QA_PROCS=5 (three concurrent runs on 16 physical cores)
- 2026-11-23 · work · phase 1 design andon A02: anthropic request body changes three goldens outside the phase (internal/text turn_text_only, internal/vendors image_encoder anthropic.text.golden); phase stays In Progress
- 2026-11-23 · work · phase 2 Complete · 6658380+wip, make qa QA_PROCS=5 twice, vet, dupl 49 groups (unchanged); no andon
- 2026-11-23 · work · phase 3 Complete
- 2026-11-23 · maintainer · A02 closed · patch: phase 1 also updates the three snapshots (internal/text/testdata/turn_text_only/request-1.json, request-2.json, internal/vendors/testdata/image_encoder/anthropic.text.golden), each gaining the cache field
- 2026-11-23 · orchestrate · phases 1–3 were implemented and gated in worktrees made from main (6658380), not from the branch (615303b); their diffs are not yet in the checkout, so phases 2 and 3 are back to In Progress until ported and gated on the branch
- 2026-11-23 · work · phases 1, 2 and 3 ported from their worktrees onto 615303b and Complete · make qa (API keys unset) exit 0 three times, go vet 0, dupl 49 groups outside .claude/worktrees (224 with them, 222 before); git diff 615303b -- internal/text/generic internal/vendors/openrouter empty; A02 snapshots updated
- 2026-11-23 · review · node 1 round 1 Ready · env -u ... make qa QA_PROCS=5 twice: exit 0; go vet ./...: 0; dupl -t 80 (outside .claude/worktrees): 49 groups, unchanged; git diff 615303b -- internal/text/generic internal/vendors/openrouter: empty; 0 findings, 5 notes
- 2026-11-23 · review · node 3 round 1 Ready · env -u ... make qa QA_PROCS=5 twice: exit 0 both, no differing test; go vet ./...: 0; dupl -t 80 (outside .claude/worktrees): 49 groups, unchanged; Engine.Pull diff: none (pull.go untouched, forkTheirs pinned); 0 blocker or major, 2 minor, 6 notes
- 2026-11-23 · review · phase 2 round 1 Ready · make qa QA_PROCS=5 twice: exit 0 (104 packages ok, internal/chat 87.7%); go vet ./...: exit 0; dupl -t 80: 49 groups outside .claude/worktrees (unchanged); previous-query (-re) behaviour verified kept · R1-20 minor filed
- 2026-11-23 · fix · R1-01 R1-02 R1-20 closed · env -u ... make qa: exit 0; go vet ./...: 0; dupl -t 80 (outside .claude/worktrees): 49 groups, unchanged; new tests TestPullConversation_failedWriteLosesNoLocalMessage, TestLiveAssetRefs_errorText, Test_fork_noticeLineJSON; `asset.RefScanError` added so the sweep text is restored; 12 added multi-line comment runs trimmed
- 2026-11-23 · orchestrate · phase 4 In Progress in the checkout (sequential: phases 4 and 5 share the checkout and its gate)
- 2026-11-23 · work · phase 4 Complete
- 2026-11-23 · orchestrate · phase 5 In Progress in the checkout; phase 4 review deferred until phase 5 is done, so no gate runs beside the executor
- 2026-11-23 · work · phase 5 Complete: -re continues line and own-spend group totals (index version 4); qa twice green, dupl 49
- 2026-11-23 · review · node 4 round 1 Ready · env -u ... make qa QA_PROCS=8 twice: exit 0 both; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups, unchanged; 0 blocker or major, 1 minor (R1-40), 2 notes
- 2026-11-23 · review · phase 5 round 1 Ready · env -u ... make qa QA_PROCS=8 twice: exit 0 both; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups, unchanged; 0 blocker or major, 4 minor (R1-60..R1-63), P11 cases, -r/-json, I9, I10 verified
- 2026-11-23 · fix · R1-40 R1-60 R1-62 closed (R1-61 and R1-63 stay Open: phase-5 file is empty, notes not writable) · env -u ... make qa: exit 0; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups; new tests TestCloudResolver_pulledWithError (3 rows), TestClearPreviousQueryLine, TestReadPreviousQueryState_goneAndInvalid, TestChatList_writesNothing (read-only tree), TestChatList_unreadableRecord invalid chat id, TestGroupTotal_cloudLocalForkCountsEachSpendOnce
- 2026-11-23 · orchestrate · phase-5 file was emptied at 13:51 by review 1 edit script (conditional write of an empty string); restored from the reviewer transcript with its intended Review 1 section
- 2026-11-23 · fix · R1-61 R1-63 closed · phase 5 Golden diff names the three persister goldens; ownSpend.2 note in Local facts; code and tests from the fixer run (make qa exit 0, vet 0, dupl 49)
- 2026-11-23 · orchestrate · phases 4 and 5 reviewed Ready, findings closed; phase 6 In Progress in the checkout
- 2026-11-23 · work · phase 6 waiting on human: root · make qa (API keys unset) exit 0 twice, go vet 0, dupl 49 groups (outside .claude); scenarios for flows 1-4, I4 child processes (e2etest.CLIBinDir), docs rewritten; F6-01 filed (major, owner fix)
- 2026-11-23 · maintainer · F6-01: drop the model carry-over; a fork runs on the profile's model like `chat continue`, and `-cm` chooses another
- 2026-11-23 · plan · README amended: P13 and I3 (no model carry-over); phase 2 re-planned in place for that change (fork.5, `LastQueryModel`)
- 2026-11-23 · plan · phase 2 re-planned: fork.5 reworded, LastQueryModel, ErrNoQueryModel and HasFlagToken removed from its Interface, acceptance row 6 and test rows re-pointed (replan row in its notes)
- 2026-11-23 · fix · F6-01 closed · env -u ... make qa: exit 0; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups; removed forkFlags, LastQueryModel, ErrNoQueryModel, HasFlagToken (text/chat_continue.go as at 615303b); e2e fork_model, openrouter and cloud scenarios re-pointed to the profile's model; docs and fork help text reworded
- 2026-11-23 · review · phase 2 round 2 Ready · env -u ... make qa QA_PROCS=8 twice: exit 0 both (208 packages ok, internal/chat 88.1-88.2%); go vet ./...: 0; no removed member left (grep), text/chat_continue.go identical to 615303b; docs and help agree; round 1 points hold · R2-20 minor filed
- 2026-11-23 · fix · R2-20 closed · phase 2 Goal and fork.6 reworded to P13 (profile's model) · documents only
- 2026-11-23 · review · node 6 round 2 Ready · env -u ... make qa QA_PROCS=8 twice: exit 0 both; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups, unchanged; 0 blocker or major, 5 minor (R2-80..R2-84); I1, I4, cc: flow, golden diff, docs verified; human checks left to the maintainer
- 2026-11-23 · fix · R2-80..R2-84 closed · env -u ... make qa twice: exit 0 both; go vet ./...: 0; dupl -t 80 (without .claude): 49 groups; phase 6 text aligned with the scenarios and the human-handoff fork commands repeat `-cm`; helpers moved to e2etest (fork.go); provider records in-flight maximum (HoldUntil, MaxInFlight) and the concurrent row asserts >= 2; parentUntouched asserts inherited usage and summary on a failed fork (a successful fork overwrites usage with its own session)
- 2026-11-23 · maintainer · asked for the -qs query stats to name the most recent conversation ID, to make forking a primed conversation easier
- 2026-11-23 · plan · phase 6.1 injected (contract, additive): the -qs summary gains a `chat <id>` row and the JSON record a `chat_id` fact
- 2026-11-23 · review · root round 3 Not ready · env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa twice (the second with GOFLAGS=-shuffle=on): exit 0 both, 109 packages ok, working tree and index unchanged; go vet ./...: 0; dupl -t 80 (Go files outside .claude): 49 groups, unchanged; probe (built binary, recording stub): three `clai -r -t '*' q hi` runs sent 36 equal tool specs in three orders · R3-01 major (phase 6 reopened), R3-02..R3-11 minor; phase 6.1, injected during the review, is Not Started and V2-03 is open, so the line stays waiting on human: root
- 2026-11-23 · maintainer · human check root (caching: fork reads the primer from the provider cache) · pass
