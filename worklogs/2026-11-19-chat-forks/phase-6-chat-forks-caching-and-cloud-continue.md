# Phase 6 — Chat forks, caching and cloud continue

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

The four Flows of the README work end to end through `cli.Run`, with stand-in providers that record request bodies and `internal/cloud/cloudtest` as clai.chat, and the architecture notes describe the verb, the cache field, `cc:` and own spend instead of a future feature.

## Interface

The root's interface is the four Flows. This phase writes the wiring that joins the five children, the test support the scenarios need, the scenarios, and the documentation. It adds no member the children do not declare.

Wiring, `internal/cli/cli.go` (`Commands`, the `"chat|c"` entry):

```go
"chat|c": chat.Command(chat.CommandDeps{
	ConfigPrep:    configPrep,
	NewSummarizer: deps.NewSummarizer,
	ParseSince:    summary.ParseSince,
	ForeignCache:  deps.ForeignCache,
	SendPrompt:    text.ContinueSender(queryDeps),
	CloudResolver: cloudResolver(deps), // phase 2's CommandDeps.CloudResolver, filled by phase 4
}),
```

```go
// cloudResolver builds, for a config dir, phase 4's CloudResolver over a sync.Engine: the client from the saved
// login (or CLAI_CLOUD_URL and CLAI_CLOUD_TOKEN), as `clai cloud` builds it (cloudcmd's runner), the same Hooks
// `clai cloud` gets in cli.go (one shared constructor, so the chat index follows a pull the way it follows
// `clai cloud pull`), and NewCloudResolver over the engine. A machine with no login gets the
// *cloud.NotLoggedInError when, and only when, a cc: reference asks for the resolver.
func cloudResolver(deps Deps) func(ctx context.Context, confDir string) (chat.CloudResolver, error)
```

Test support, written first, in `e2e/e2etest/` (new file `providers.go`); no support code reaches the product:

```go
// CachingProvider is an OpenAI-compatible stand-in that records every request body and answers a fixed
// reply with a usage chunk. It models prefix caching: prompt_tokens is 10 per message of the request;
// cached_tokens is 10 per leading message that is byte-equal to the same leading message of the most
// similar earlier request.
type CachingProvider struct{ /* unexported */ }

func StartCachingProvider(t testing.TB, reply string) *CachingProvider
// WriteModel points model (a gpt-* name) at the stand-in for the runs using confDir and sets a dummy key.
func (p *CachingProvider) WriteModel(t testing.TB, confDir, model string)
// Requests returns the bodies in arrival order; Decode splits one into model, messages and the rest.
func (p *CachingProvider) Requests() []RecordedRequest
func (p *CachingProvider) Usage(i int) UsageReport // prompt, cached, total tokens it reported for request i

type RecordedRequest struct {
	Body     string
	Model    string
	Messages []json.RawMessage
	Rest     map[string]json.RawMessage // every top-level member but messages
}

// ClaudeProvider is the same for the Messages API (SSE): it answers message_start with
// cache_read_input_tokens following the same rule, but only when the request body carries a top-level
// cache_control member, as Anthropic does.
func StartClaudeProvider(t testing.TB, reply string) *CachingProvider
func (p *CachingProvider) WriteClaudeModel(t testing.TB, confDir, model string) // anthropic_claude_<model>.json, ANTHROPIC_API_KEY
// WriteOpenRouterModel points an openrouter model at a stand-in speaking the chat-completions shape.
func (p *CachingProvider) WriteOpenRouterModel(t testing.TB, confDir, model string)
```

Scenario clauses, each proved by the rows named in `Proves`:

- `root.1` (I1) A fork's request repeats the parent's last request up to the new prompt, and the provider's cache is read.
- `root.2` (I2, I3) The parent is untouched; the fork carries the parent's fields.
- `root.3` (P13) The fork runs on the profile's model; `-cm` among its flags chooses another.
- `root.4` (I10) Group totals count each query once.
- `root.5` (I4) Concurrent forks leave complete files and rows.
- `root.6` (I1, I5, I6) Claude requests carry the cache field and a fork reads the cache; OpenRouter requests do not.
- `root.7` (I7, I8, I10) `continue cc:X` adopts, replaces or keeps a local fork, and the local-only spend counts once.
- `root.8` (I7) After adoption the next pull has nothing to do for `X`.
- `root.9` (P6, P8) The refusals exit 1 with the specified texts and write nothing.
- `root.10` (I9, P11) `chat list` names what `-re` continues and writes nothing.
- `root.11` A plain `-re` after a fork continues the fork.
- `root.12` (P7, I2) `fork cc:X` adopts then forks.
- `root.13` (I2) A fork whose request fails stays on disk and the parent is untouched (`fork.8`).

Consumes:

- `Claude cache field` (phase 1): `constructRequest` body.
- `chat fork`, `Forker` (phase 2).
- `ConversationPuller`, `Engine.PullConversation`, `ConversationPull`, `forkCopy`, `FetchedHashes` (phase 3).
- `Forker`, `chatOwnSpend`, `CloudResolver`, `CloudResolution`, `CloudRefError`, `CommandDeps.CloudResolver` (phase 2); `NewCloudResolver`, `cloudNotice`, `CloudConversationMissingError` (phase 4).
- The `-re continues:` line, `chatOwnSpend`, own-spend index fields and group totals (phase 5).
- `previous-query-pointer:SavePreviousQuery`, `previous-query-pointer:LoadPreviousQuery`, `previous-query-pointer:ErrNoPreviousQuery` (import): through `-re`, the finalize path and the list line.
- `e2etest.NewHarness`, `SetupMainTestConfigDir`, `CLIBinDir`, `SeedConv`, `SeedPreviousConv`, `LoopbackOnly`, `cloudtest.Server` (repository code).

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | A fork's request repeats the parent's last request: same `model`, same non-message members, parent messages (with the answer) as a byte-equal prefix, then the new prompt; the stand-in reports cached tokens equal to the parent's whole prompt, and stderr shows `(N cached)` under `-qs` | I1, flow 1 | `Test_e2e_chat_fork_repeatsParentRequest` |
| 2  | The parent's file and index row are unchanged by a fork, and the fork's file carries the parent's messages, queries, usage, title and group | I2, I3 | `Test_e2e_chat_fork_parentUntouched` |
| 3  | The fork runs on the profile's model, not the parent's; a repeated `-cm` chooses the model | P13 | `Test_e2e_chat_fork_model` |
| 4  | A parent and two forks list as one group whose Cost and Tokens count each query once; the fork's group-view row shows its whole history | root.4, I10 | `Test_e2e_chat_fork_groupTotals` |
| 5  | Four fork processes started together leave four complete chats and four index rows, and the previous-query record names one of them | I4 | `Test_e2e_chat_fork_concurrent` |
| 6  | A Claude request carries the top-level cache field, a fork of a Claude chat repeats it and reads the cache; an `anthropic/*` OpenRouter model's request has no cache field | I1, I5, I6, flow 2 | `Test_e2e_claude_cacheField`, `Test_e2e_claude_forkReadsCache`, `Test_e2e_openrouter_claude_isNotCached` |
| 7  | `continue cc:X` is Adopted with no local copy, Replaced after the server grows, LocalForked after a local turn; the notices are P10's; the local-only spend counts once in the group total | I7, I8, I10, flow 3 | `Test_e2e_cloud_continue_threeOutcomes`, `Test_e2e_cloud_continue_localForkTotals` |
| 8  | After each outcome the next `cloud pull conversations` has nothing to do for `X` and exits 0 | I7, root.8 | `Test_e2e_cloud_continue_then_pull_has_nothing_to_do` |
| 9  | The refusals (not in the cloud, offline, not logged in, no prompt for fork, `cc:` on delete) exit 1 with the specified texts and write nothing | P6, P8 | `Test_e2e_cloud_refusals`, `Test_e2e_chat_fork_withoutPrompt` |
| 10 | After a fork, `chat list` starts with `-re continues: 0 [group] · <label>`; `chat list -json` has no such text | P11, I9 | `Test_e2e_chat_list_continuesLine` |
| 11 | After a fork, a plain `-re <prompt>` continues the fork, not the parent | root.11, flow 1 | `Test_e2e_chat_fork_thenReContinuesFork` |
| 12 | `fork cc:X <prompt>` adopts then forks | P7 | `Test_e2e_cloud_fork` |
| 13 | The Golden diff below holds: nothing else in any golden changes | Golden diff | `make qa` |
| 14 | The architecture notes describe the shipped behavior; no "future" or "not implemented" wording for forking remains | Scope | `grep -rn "Forking (future)\|documented but not implemented" architecture` prints nothing |
| 15 | All gates are green | Validation policy | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa` |

## Test cases

`e2e/chat_fork_e2e_test.go` (package `e2e`), `e2e/claude_cache_e2e_test.go` (package `e2e`), `e2e/remote/cloud_continue_e2e_test.go` (package `remote`). Every row runs through `harness.RunOne`/`harness.Run` against a temp config dir, stand-in providers on loopback (`e2etest.LoopbackOnly` transport), and `cloudtest.Server` with `CLAI_CLOUD_URL`/`CLAI_CLOUD_TOKEN` set by `t.Setenv`.

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `Test_e2e_chat_fork_repeatsParentRequest` | integration scenario | root.1, I1 | `CachingProvider`, model `gpt-a`; `-r q first question` seeds P; then `-r chat fork 0 add a second step` | request 2 has the same `model` and `Rest` as request 1; `Messages[:len(request 1)]` byte-equal request 1's messages up to the parent's answer; the last message is the new prompt; `Usage(1).Cached == Usage(0).Prompt` (the parent's whole prompt); stderr under `-qs` contains `(N cached)` with that N; stderr has `fork <F> of <P>` | change the parent's request members |
| `Test_e2e_chat_fork_withTools` | integration scenario | root.1, I1 | a profile with two tools and `gpt-a` | request 2's `tools` member is byte-equal to request 1's, same order | reorder tools |
| `Test_e2e_chat_fork_parentUntouched` | integration scenario | root.2, I2, I3 | seed P with two queries (own price file), snapshot P's bytes and its index row, fork | P identical; F has new ID, `source "clai"`, `source_id P`, P's messages as prefix, P's `queries` as prefix of its own, title, `group_key`, `origin_dir`, `profile` | stamp P |
| `Test_e2e_chat_fork_model` | integration scenario (table) | root.3, P13 | default model `gpt-b`; P's last query `gpt-a` (seeded with `-cm gpt-a`); forks: none, `-cm gpt-a`, `-chat-model gpt-a`, `chat fork -cm gpt-a`; a parent without a recorded model (a seeded chat with no queries) | request models: `gpt-b`, `gpt-a`, `gpt-a`, `gpt-a`, default `gpt-b` | send the parent's model without `-cm` |
| `Test_e2e_chat_fork_groupTotals` | integration scenario | root.4, I10 | P (1 query), forks A and B (1 query each), the stand-in's usage known; `chat list`, then the group view | the group row's Tokens is `abbrevTokens` of `T_P + T_A + T_B` (each query's own tokens), Cost likewise from a price file; entering the group, row A shows `T_P + T_A`, P shows `T_P`; the index file's rows hold the matching `own_total_tokens` | show `3 * T_P` in the group row |
| `Test_e2e_chat_fork_concurrent` | integration scenario (child processes through `e2etest.CLIBinDir`) | root.5, I4 | `CachingProvider` with a small delay, P seeded, four `clai chat fork 0 task <i>` processes started together in the same config dir | four distinct F files, each loadable with P's messages plus its own prompt and answer; four rows in `chat_index.cache` besides P's; `LoadPreviousQuery` names one of the four; P unchanged | share an ID, corrupt the index |
| `Test_e2e_chat_fork_providerError` | integration scenario | root.13, fork.8 (phase 2), I2 | the stand-in answers 500 to the fork's request | exit 1 with the run's error; F's file and index row exist; P unchanged | delete F |
| `Test_e2e_chat_fork_thenReContinuesFork` | integration scenario | root.11 | after the fork, `-r -re q follow up` (a plain reply) | the request's messages hold the fork's turns; F gains the turn; P unchanged | continue P |
| `Test_e2e_chat_fork_withoutPrompt` | command | P6 | `chat fork 0` and `chat fork` | exit 1, the P6 text on stderr; conversations dir unchanged; provider saw no request | write a fork |
| `Test_e2e_chat_fork_externalRow` | integration scenario | fork.2 (phase 2), root.2 | a foreign (claude-code) row at index 0, fixtures as `e2e/foreign_cache_test.go` uses | a native clone and a fork of it exist; request carries the foreign messages | fork the foreign row in place |
| `Test_e2e_claude_cacheField` | integration scenario | root.6, I6 | `StartClaudeProvider`, model `claude-x`; `-r q hello` | the recorded body's `cache_control` equals `{"type":"ephemeral"}` at the top level and nowhere else; the answer prints | send it for any other vendor |
| `Test_e2e_claude_forkReadsCache` | integration scenario | root.6, I1, I6 | the same stub, seed then `chat fork 0 task` | request 2's non-message members equal request 1's, including `cache_control`; stderr under `-qs` shows `(N cached)`, `N` from the stub's `cache_read_input_tokens` | report cache read for a body without the field |
| `Test_e2e_openrouter_claude_isNotCached` | integration scenario | root.6, I5 | a stand-in OpenRouter route for `anthropic/claude-x` | the body has no `cache_control` anywhere | change `internal/text/generic` or `internal/vendors/openrouter` |
| `Test_e2e_cloud_continue_threeOutcomes` | integration scenario (package `remote`) | root.7, I7, I8, P10 | `cloudtest` holding `X` (2 messages, a sidecar file, an image); `CachingProvider` | `continue cc:X`: stderr `cc:X: taken from clai.chat`, local `X.json` equals `srv.Content`, sidecar and image present; then `srv.Put` a 4-message `X`: stderr names `cloud/trash/conversations/X.json.<time>`, the trash file is the old bytes; then a local turn `continue X more` and `srv.Put` a different 4-message `X`: stderr `taken from clai.chat; the local copy is now <L>`, `L` has every local message, `Source "clai"`, `SourceID X`, `chat list` shows `X` and `L` | upload anything (`srv` commit count stays zero) |
| `Test_e2e_cloud_continue_localForkTotals` | integration scenario (package `remote`) | root.7, I8, I10 | `X` adopted (1 query, tokens `T_X`), a local turn adds query `q` (tokens `T_q`), `srv.Put` a different longer `X` (cloud has `T_X` plus one other query `T_c`); `continue cc:X` | `L.created` equals `q.created_at`; `L`'s index row has `own_total_tokens == T_q`; `chat list` shows `X` and `L` as one group whose Tokens is the abbreviation of `T_X + T_c + T_q`, `q` counted once; the group view shows `L` with its whole history | count `q` in `X` or twice |
| `Test_e2e_cloud_continue_then_pull_has_nothing_to_do` | integration scenario | root.8, I7 | after each outcome above | `cloud/state/conversations.json` records `X.json` at the server's revision; `cloud pull conversations` exits 0, not 4; `X.json` bytes unchanged; no `cloud/theirs/` | fetch `X`'s blob again in the pull |
| `Test_e2e_cloud_continue_withPromptSendsToCloudCopy` | integration scenario | root.7 | `continue cc:X and then this` | the stand-in's request holds the cloud messages plus the prompt | send before adoption |
| `Test_e2e_cloud_fork` | integration scenario | root.12, P7 | `fork cc:X do it` with no local `X` | stderr notices in order: adoption, then `fork <F> of X`; `X` is the cloud copy; F's `source_id` is `X`; the stand-in's request carries the cloud messages | write `X` after the fork |
| `Test_e2e_cloud_refusals` | command (table) | root.9, P8, ref.2 (phase 4), chatref.4, chatref.6 (phase 2) | `continue cc:missing`; server closed; no login (`CLAI_CLOUD_TOKEN` unset, no credentials file); `fork cc:X` without prompt; `delete cc:X`; `continue cc:` | exit 1 each with P8's sentence, a transport error, the `clai cloud login` hint, the P6 text, phase 2's `chatref.6` text, phase 2's `chatref.4` text; the config dir unchanged | print the list UI |
| `Test_e2e_chat_list_continuesLine` | integration scenario | root.10, P11, I9 | after fork `F` of `P` (a group of two); then `-json chat list` | the first line is `-re continues: 0 [group] · <label>`, header next; `-json` has no such text; the config tree is identical before and after the list | write the record |
| `Test_e2e_help_listsForkAndCloudRef` | command | fork.9 (phase 2), Golden diff | `chat -h`, `chat help`, `chat fork -h`, `-h` | each lists `fork`; `chat fork -h` shows the synopsis and the `cc:` form | drop an existing line |
| `make qa` | command (gate) | criteria 13 and 15, Golden diff | every golden and test above in place | the gates in the Validation policy pass | edit the timeout, count or race flags |
| `Test_architecture_noFutureForking` | unit (reads `architecture/*.md`) | criterion 14 | the two rewritten files | no "Forking (future)" heading, no "documented but not implemented", each mentions `clai chat fork` and the lineage rules | pass on an empty file |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| `chat fork` without a prompt | exit 1, P6 text; nothing written, no request | `Test_e2e_chat_fork_withoutPrompt` |
| `cc:X` not in the cloud store | exit 1, P8 text; nothing written | `Test_e2e_cloud_refusals` |
| clai.chat unreachable | exit 1, transport error in the chain; local files unchanged | `Test_e2e_cloud_refusals` |
| Not logged in | exit 1, `not logged in to clai.chat: run 'clai cloud login'` | `Test_e2e_cloud_refusals` |
| `cc:` on `chat delete`, or with no ID | exit 1, phase 2's `chatref.6` text for delete and `chatref.4` text for a bare `cc:`; nothing deleted | `Test_e2e_cloud_refusals` |
| The provider answers an error to a fork's request | exit 1 with the run's error; the fork `F` remains on disk and `chat list` shows it | `Test_e2e_chat_fork_providerError` |
| The fork's `-cm` is unknown to the provider setup | the existing unknown-model error; `F` remains | `Test_e2e_chat_fork_unknownModelKeepsFork` |

## Golden diff

The net set across phases 1 to 6, as the checkout holds it (diff against 615303b):

| Golden | Change | Phase |
| ------ | ------ | ----- |
| `internal/vendors/anthropic/testdata/claude_request_{plain,schema,tools}.json` | new (recorded before phase 1's change; the tests strip `cache_control` before comparing) | 1 |
| `internal/text/testdata/turn_text_only/request-1.json`, `request-2.json` | each gains the top-level `cache_control` member (A02) | 1 |
| `internal/vendors/testdata/image_encoder/anthropic.text.golden` | gains the `cache_control` member (A02) | 1 |
| `e2e/testdata/diagnostics/help.golden` | the `chat\|c` line lists `fork`; no other line changes | 2 |
| `chat -h` (inline in `e2e/nested_e2e_test.go`, `Test_e2e_nested_help`) | `fork\|f` joins the expected sub table | 2 |
| `internal/cli/usage_error_test.go` | the `-cm` hint names `chat fork` | 2 |
| `internal/chat/testdata/persister/{save,promote-new,promote-existing}.golden` | index version 4, `own_total_*` fields | 5 |
| `e2e/conversation/summary_e2e_test.go` `setupSummaryE2E` | seeded cache version 3 to 4 | 5 |
| `e2e/chat_list_e2e_test.go` | two new rows; no existing `chat list` expectation gained the `-re continues:` line (none runs with a previous-query record) | 5 |
| every other golden, including `e2e/testdata/diagnostics/*.golden` other than `help.golden` | unchanged; phase 6 changes none | 6 |

## Removes

None.

## Local facts

Documentation this phase rewrites (the same commit as the scenarios that prove it):

- `architecture/continue-from-claudex.md`: "Forking (future)" becomes "Forking": `clai chat fork <chat> <prompt>`, the I3 stamping (every field but ID, Created, Source, SourceID is the parent's), the P13 rule (the fork runs on the profile's model; `-cm` is repeated to fork a chat run with `-cm`), that the fork's request repeats the parent's, and the prompt requirement; the sentence "documented but not implemented" goes; the first-section remark about a future `clai chat fork <id>` goes.
- `architecture/chat-groups.md`: "Forking integration" describes the shipped verb and the own-spend rule (I10): group rows total each member's own spend, a member's own row shows its whole history. "Forking from a pre-existing chat with empty GroupKey" states the shipped behavior: the parent is not written (I2), so it stays unstamped until an index rebuild or its next save, and the fork is stamped from its own first message on first persist, which is the key the rebuild gives the parent when the first messages match. The header's "upcoming conversation forking feature" wording goes.
- `architecture/chat.md`: the Chat struct comments for `Source`/`SourceID` (fork), `Queries` (an inherited query predates `Created`), the index row's `own_total_tokens`/`own_total_cost_usd` and the version history, the `-re continues:` line, and the `chat fork` flow beside `chat continue`.
- `architecture/cloud.md`: a `cc:` section: the one-conversation pull (full change list, only `X`'s blobs, P12), that it reuses the pull's merge with the cloud copy keeping the ID (the four outcomes, `cloud/trash/`, the fork of the local copy), that it records `X` in `cloud/state/` and uploads nothing, and the I7 equality that makes the next pull a no-op. The Key files table gains `conversation.go`.
- `architecture/query.md` (or `streaming.md`, whichever documents the Claude request): the top-level `cache_control` field, the 5-minute TTL, P5's cost, and that Claude through OpenRouter is not cached.
- `architecture/README.md`: the `chat.md`, `continue-from-claudex.md` and `chat-groups.md` bullets mention fork and `cc:`.
- `AGENTS.md` and `CLAUDE.md` are not changed.

Other facts:

- The scenarios use no real provider and no real clai.chat; the credentialed checks are the two Human checks.
- `Test_e2e_chat_fork_concurrent` uses child processes because `cli.Run` shares process state (working directory, stdout capture, `utils` flags); the children run this test binary as the CLI (`e2etest.CLIBinDir`), so no `go build` is paid.
- `cc:` goes through the sync engine (`Engine.PullConversation`, phase 3), not a chat-package adopter; the composition root builds the engine with the hooks `clai cloud` gets, so the chat index follows.
- The `LocalForked` copy `L` is created at its first query the cloud copy lacks (I8), so its local-only spend counts once in the lineage's group total; `Test_e2e_cloud_continue_localForkTotals` asserts it.

## Human required

- **Artifact produced:** a dated note in the notebook's `codebases/imago/clai/` holding, for each of a Claude model direct, an OpenAI model and a DeepSeek model, the primer's prompt tokens and each of two forks' "(N cached)" with the percentage; and the three `cc:` outcomes' stderr lines with the `cloud pull` result (README Human checks "root, caching" and "root, cloud").
- **Stopping point:** the agent stops with the line `waiting on human: root` when every gate and every scenario above is green and the documentation is rewritten, before running either credentialed check.
- **Before (agent):** prints the exact commands for both checks with `CLAI_CONFIG_DIR` and model placeholders, confirms the funded keys are named in Doppler or the shell without printing them, and leaves the working tree committed-ready. **After (human or agent with the human's keys):** runs the checks, records the numbers in the note, and the agent journals `human check root, caching · <result>` and `human check root, cloud · <result>`.

## Implementation notes

| Run   | Commit      | Command | Result |
| ----- | ----------- | ------- | ------ |
| start | 615303b+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; dupl -t 80 over the Go files outside `.claude` | qa exit 0, vet exit 0, 49 groups |
| end   | 615303b+wip | the same | qa exit 0 (104 packages ok), vet exit 0, 49 groups |
| end   | 615303b+wip | the same | qa exit 0 (104 packages ok), vet exit 0, 49 groups |
| end   | 615303b+wip | the same, after R2-80..R2-84 | qa exit 0 twice, vet exit 0, 49 groups |

Andon: none. The phase stops at its handoff: the line is `waiting on human: root` (the two credentialed checks, below).

Files: `internal/cli/cli.go` (`syncHooks`, `cloudResolver`, `CloudResolver` wired into the `chat|c` entry; `cloud` now takes `Hooks: syncHooks(deps)`), `internal/cli/cloud_resolver_test.go` (new), `e2e/e2etest/providers.go` (new: `CachingProvider`, `StartCachingProvider`, `StartClaudeProvider`, `WriteModel`, `WriteClaudeModel`, `WriteOpenRouterModel`, `Requests`, `Usage`, plus `SetDelay` and `FailFrom`), `e2e/chat_fork_e2e_test.go`, `e2e/claude_cache_e2e_test.go`, `e2e/architecture_forking_test.go`, `e2e/remote/cloud_continue_e2e_test.go` (all new), `architecture/{continue-from-claudex,chat-groups,cloud,streaming,README}.md` (`chat.md` was done by phases 2 and 5).

Wall time of the e2e binary: 6.5 s alone under `-race -count=3 -cover`, 7.5 s and 9.8 s inside the two full `make qa` runs (`e2e/remote` 6.4 s and 8.7 s); the child-process row `Test_e2e_chat_fork_concurrent` is about 0.25 s per count.

Surprises:

- "(N cached)" is the run summary's `tokens` line, printed on stderr and only under `-qs`; stdout holds the answer alone. The rows assert it on stderr with `-qs` (acceptance row 1 and `Test_e2e_claude_forkReadsCache` say stdout).
- `Test_e2e_chat_fork_repeatsParentRequest` asserts `Usage(1).cached == Usage(0).prompt` (the parent's whole request). The row's text adds "the parent's reply message", but the reply was never part of any earlier request, so no provider (nor the stand-in's byte-equal rule) can read it from cache.
- The group row's Tokens column, and the Tokens column of a group view, render only on a terminal wider than 120 columns, and a run without a terminal is 80 wide (`dimensions.Fallback`). The group rows assert the Cost column (the same own-spend totals) and the index rows' `own_total_tokens` and `total_tokens`.
- Finding F6-01: a fork of an OpenRouter chat runs on the vendor the bare model name suggests. The chat records `anthropic/claude-x` (the `modelVersion`), so `-cm=anthropic/claude-x` routes to the anthropic vendor (it asks for `ANTHROPIC_API_KEY`), not to `or:anthropic/claude-x`. `Test_e2e_openrouter_claude_isNotCached` names `-cm or:anthropic/claude-x` on its fork; a plain fork of such a chat fails. The same holds for any model recorded without its route prefix (`berget:`, `novita:`, `hf:`).
- OpenRouter ignores the model config's `url`, so `WriteOpenRouterModel` redirects `openrouter.ai` on `http.DefaultTransport` to the stand-in. `chat fork 0` finds a foreign row only when the index already holds a native chat (`Test_e2e_chat_fork_externalRow` seeds one and forks index 1).

Human handoff (README Human checks, not run):

- caching: with funded keys, per model in {a Claude model direct, an OpenAI model, a DeepSeek model}: `CLAI_CONFIG_DIR=$(mktemp -d) clai -r -qs -cm <model> q <a prompt over 5k tokens>`, then twice `clai -r -qs chat fork -cm <model> <id from 'chat list'> <task>` (`-cm` must match the parent's model); each fork's `(N cached)` should cover at least 90 % of the parent's prompt tokens. Record the numbers in a dated note under `codebases/imago/clai/` of the notebook. The shell's `ANTHROPIC_API_KEY` had no credit on 2026-11-19.
- cloud: logged in to clai.chat, from a second config dir (`CLAI_CONFIG_DIR=<dir2> clai cloud commit conversations` after a chat `X`), then here `clai chat continue cc:X` (Adopted), again after continuing `X` in `<dir2>` and committing (Replaced), and again after a local turn (LocalForked). Pass: the stderr notices match P10 and `clai cloud pull conversations` reports nothing to do for `X`.

## Review findings

### Review 2, 2026-11-23

Verdict: Ready (0 blocker, 0 major, 5 minor). Gates: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa QA_PROCS=8` twice, exit 0 both; `go vet ./...` 0; dupl -t 80 (without .claude) 49 groups, unchanged.

Findings:

- [x] R2-80 (minor) `phase-6-chat-forks-caching-and-cloud-continue.md:97,119,130,102,141-142`: the phase text disagrees with the shipped scenarios. Acceptance row 1 and the `repeatsParentRequest` and `claude_forkReadsCache` rows say stdout and "prompt + parent's reply message"; the code asserts stderr under `-qs` and the parent's whole prompt. Acceptance row 3 and the last Error management row run the `Test_e2e_chat_fork_model` test row into themselves and still carry "carry the parent's model"; `Test_e2e_chat_fork_unknownModelKeepsFork` and `Test_e2e_chat_fork_externalRow` evidence has no clean row.
- [x] R2-81 (minor) `e2e/remote/cloud_continue_e2e_test.go:383`, `e2e/chat_fork_e2e_test.go:108`: `indexRows`, the tree walk (`cloudEnv.tree` / `configTree`), `forkNoticeLine` and the text-config setup are written twice across packages `e2e` and `remote`; move them to `e2etest` (CLAUDE.md: written twice is abstracted).
- [x] R2-82 (minor) `internal/cli/cli.go:310,329`, `e2e/chat_fork_e2e_test.go:353,365,367`, `e2e/remote/cloud_continue_e2e_test.go:183,367,371`: multi-line comments that carry rationale (maintainer rule: one line, no rationale).
- [x] R2-83 (minor) `e2e/chat_fork_e2e_test.go:380`: `Test_e2e_chat_fork_concurrent` never asserts that the four processes overlapped (the 200 ms delay only makes it likely); a provider-side in-flight maximum of at least 2 would make I4 evidence non-vacuous.
- [x] R2-84 (minor) `e2e/chat_fork_e2e_test.go:239`: `Test_e2e_chat_fork_parentUntouched` compares messages, queries, group, title, profile and origin, but not the inherited `TokenUsage`, `RecentTokenUsage` or `Summary` that I3 and acceptance row 2 ("usage") list.

Verified good:

- I1: both fork scenarios compare `model`, every non-message member (tools, `cache_control`, system) and the message prefix byte for byte on the recorded bodies; the stand-in computes cached tokens only from byte-equal leading messages, so `(20 cached)` and `(10 cached)` fail if the prefix drifts. The Claude stand-in reports a cache read only with the top-level field.
- Deviations judged acceptable: "(N cached)" on stderr under `-qs` (it is the run summary; `streaming.md` says so); cached equals the parent's whole prompt (the reply was never sent before, so no provider can cache it); group totals through the Cost column (`$0.135` from hand-computed prices, independent) and `own_total_tokens` and `total_tokens` index rows (the Tokens column needs a width over 120 that a run without a terminal never has; the aggregate is unit-tested in phase 5); `WriteOpenRouterModel` rewrites `openrouter.ai` on the transport because the vendor ignores the config url (cleanup LIFO restores both wrappers); `SetDelay` and `FailFrom` are test-only helpers.
- I4: four real child processes, four distinct complete files, four index rows beside the parent's, the previous-query record names one, the parent bytes unchanged.
- `cc:` end to end: the three outcomes assert bytes, sidecar, image, trash content, `L` with every local message, no commit (zero uploads); after each outcome `cloud pull conversations` exits 0, fetches no blob, leaves `X.json` unchanged and has no `cloud/theirs/`; `cloud/state` is checked for the path only, not the revision, which the no-fetch assertion covers.
- Root Golden diff: the changed snapshots in the tree (turn_text_only request-1/2, anthropic.text.golden, help.golden `fork`, three persister goldens, new claude_request_* files, and the inline help, usage, summary and chat_list tests) equal the table; nothing under `internal/text/generic` or `internal/vendors/openrouter` changed.
- Docs: `architecture/*.md` describe the shipped verb, I3 stamping, the profile's model with no carry-over, `cc:`, own spend and the cache field; the two banned phrases are gone (`grep` prints nothing).
- Human checks left to the maintainer; not run.

### Review 3 (root), 2026-11-23

Verdict: Not ready (0 blocker, 1 major, 8 minor). Gates: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa` twice (the second with `GOFLAGS=-shuffle=on`), exit 0 both, 109 packages ok, working tree and index unchanged by the gate; `go vet ./...` 0; dupl -t 80 over the Go files outside `.claude` 49 groups, unchanged, none in a new file.

Findings:

- [ ] R3-01 (major, owner plan) `internal/text/querier_setup_tools.go:638,707`, `internal/tools/registry.go:55`: a chat run with every tool (`-t '*'`, or a profile with `use_tools` and no list) or with a tool glob (`-t 'f*'`, `mcp_*`) sends its `tools` array in a new order in every process, because `setupTooling` registers tools by ranging over the map `available`, `matchingTools` ranges over the same map, and `registry.WildcardGet` over `r.tools`. Anthropic renders tools first (tools, system, messages), and OpenAI and DeepSeek cache by the whole prefix, so a fork or a `-re` of such a chat reads 0 cached tokens on every vendor. Unmet: I1 ("tools in the same order"), `fork.6`, `root.1`, and the Goal (forks of a primer read its part from the cache), which is the maintainer's reason for the verb: subagents forked from a primer use tools. Probe: `clai -r -t '*' q hi` run three times against a recording stub sent the same 36 tool specs (byte-equal per name, same system) in three different orders (first names `audio_record`, `ffprobe`, `pwd`). `Test_e2e_chat_fork_withTools` names two tools explicitly, so it never reaches these paths. Item: offer tools in a stable order (by name) on the all-tools and glob paths, and add a scenario that forks a chat run with `-t '*'` (and one with a glob) and compares the `tools` member byte for byte; the change lies in `internal/text` and `internal/tools`, which no phase owns.
- [ ] R3-02 (minor, owner fix) `internal/chat/cmd.go:113-114`, `architecture/chat.md:306`, `architecture/continue-from-claudex.md:948`, `architecture/streaming.md:178-179`: the help and the notes name `-cm` as the one flag to repeat, but `-t` and `-lb` also decide the request's `tools` member, and no chat records them. A fork without `-t` of a chat primed with `-t '*'` sends no tools and reads no cache. Item: name the request-shaping flags to repeat (`-cm`, `-t`, `-lb`) wherever the `-cm` rule is stated.
- [ ] R3-04 (minor, owner fix) `architecture/cloud.md:117-118`: "No local `X`, or the same messages: the cloud copy is written (Adopted)". For the same messages `settle` answers `ConversationUnchanged` (`internal/cloud/sync/conversation.go:282`), which prints no notice (`internal/chat/cloud_resolver.go:189`), and `putAside` moves a same-message local copy that differs elsewhere (a title) to `cloud/trash/` (`conversation.go:289-296`), which the section omits. Item: give the same-messages case its Unchanged label and say the differing local file goes to `cloud/trash/` without a notice.
- [ ] R3-07 (minor, owner fix) `e2e/e2etest/providers.go:35-38,54-55,140-141`: three multi-line doc comments (`CachingProvider`, `StartClaudeProvider`, `WriteOpenRouterModel`) against the one-line rule that R2-82 applied to the other files. Item: one line each.
- [ ] R3-09 (minor, owner plan) `README.md:65`: Flow 1 says "stdout: the answer, and "(4,812 cached)" in the usage line"; the product prints "(N cached)" in the run summary on stderr and only under `-qs` (accepted in review 2, `streaming.md:173`). Item: reword the flow line.
- [ ] R3-10 (minor, owner maintainer) index against working tree: the staged `e2e/testdata/diagnostics/help.golden` drops HEAD's `audio v|voices` (`git diff --cached HEAD`), and the staged `internal/cli/cli.go` keeps the two multi-line comments R2-82 trimmed; a commit of the index alone fails `TestDiagnosticOutputGoldens`. The working tree is right. Item: stage both files (and the untracked files) before committing.
- [ ] R3-11 (minor, owner maintainer) README Human checks: neither "root, caching" nor "root, cloud" has a journal record. The caching check runs without `-t`, so it passes with R3-01 open; after R3-01 is fixed, one primer and two forks with `-t '*'` on a Claude model would show the fix.

Verified good:

- Flows: flow 1 (`Test_e2e_chat_fork_*`), flow 2 (`Test_e2e_claude_cacheField`, `Test_e2e_claude_forkReadsCache`, `Test_e2e_openrouter_claude_isNotCached`), flow 3 (`Test_e2e_cloud_*` in `e2e/remote`) and flow 4 (`Test_e2e_chat_list_continuesLine`) run end to end through `cli.Run`, and each fails when its flow breaks (cache field removed, parent written, no local fork, no line).
- I2: `fork` writes only the copy (`forker.go:52`); the parent is read through `findChatByRef`. I3: `forker.go:47-51` and `copyChat` (deep copies of messages, tool calls, reasoning items, parts, queries, usage); the reasoning sidecar is rewritten for the fork by `Save` (`TestForker_sidecarAndImages`). I4: child processes with an in-flight maximum of at least 2. I5: no change under `internal/text/generic` or `internal/vendors/openrouter` against HEAD. I6: the field is set unconditionally in `constructRequest` (`claude_stream.go:410`); `CountInputTokens` sends none, which is a count, not a Messages request. I7: `writeCloud` writes the fetched bytes verbatim, `removeStaleNotes` drops sidecar files the cloud lacks, `recordInSync` sets the base. I8: `putAside` (fork or trash) runs before `writeCloud`; `forkCreated` takes the first query the cloud copy lacks. I9: the line reads `LoadPreviousQuery` only. I10: `chatOwnSpend` and the group row's `OwnTotal*`.
- Caching facts in `streaming.md` match Anthropic's caching reference: automatic caching is the top-level field, one breakpoint on the last cacheable block, 20-position lookback, 512-token minimum on the Claude 5 generation, 1.25x write at the 5-minute TTL, reads at 0.05x on Opus 5.5 and Sonnet 5.5. With byte-equal tools, a fork's breakpoint sits two positions after the parent's last entry (its answer and the new prompt), so the read lands; reasoning blocks clai does not replay come after that entry and do not move it.
- The maintainer's request: items 1 (`-re` in place, `491fe01`), 2 (`chat fork`, automatic caching, no flag), 3 (`-re continues:` line) and 4 (`cc:`; plain IDs stay local; the cloud copy keeps the ID; a conflicting local copy becomes a fork under a `chatid.New` UUIDv7) ship. The gap is R3-01 (and R3-02): tool-using primers and forks never read the cache.
- Docs: no model carry-over is stated in the fork help and in `chat.md`, `continue-from-claudex.md` and `streaming.md`; the `cc:` policy difference from `clai cloud pull` is in `cloud.md:111-115` ("except that the cloud copy keeps the ID"); the help gap is R3-03 (phase 4).
- Errors and logging: public failures are typed (`ForkError`, `CloudRefError`, `CloudPullError`, `CloudConversationMissingError`, the `sync` errors); the refusals in `fork` and `refuseCloudDelete` are untyped `errors.New`/`fmt.Errorf` in private functions, acceptable. No reusable component logs: `sync` returns outcomes, the resolver returns its notice, and only the command handler prints.
- Hermetic tests: every package with a new test file has a `testenv` or `e2etest` `TestMain`; no new test resolves a program through `PATH`.
- The rebase: `615303b`, the base the phase notes cite, is no longer an ancestor of HEAD; the same golden set holds against `491fe01`.
