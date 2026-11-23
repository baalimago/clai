# Phase 2 — Fork verb

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

`clai chat fork [query flags] <chat> <prompt>` writes a new chat that holds a copy of the parent, leaves the parent untouched, and sends the prompt to the copy on the profile's model (P13), so on the parent's model the provider reads the parent's part from its cache.

## Interface

Three surfaces: the CLI verb, the `Forker` it is built on and two seams later phases use without editing this phase (phase 4 fills the resolver hook, phase 5 consumes the own-spend function): the chat resolver with its `cc:` hook (`findChatByRef`, `CloudResolver`), and the own-spend function (`chatOwnSpend`).

### CLI

Registered in `internal/chat/cmd.go` as `"fork|f"`, with the query flags (`withQuery`) and `fullChatSetup(deps, cf, &qf)`, exactly as `continue|c` is. Implemented in `internal/chat/handler_fork.go` (`(*ChatHandler).fork`) and `internal/chat/forker.go`.

```text
clai chat fork [query flags] <chatID|index> <prompt...>
clai c f 0 add a test for the empty case

  <chatID|index>  a chat ID, or an index of `clai chat list`; an external row is cloned first, then forked,
                  as `chat continue` does. `cc:<id>` names the cloud copy of a chat; this phase resolves it through the `CloudResolver` seam below, and phase 4 supplies the resolver.
  <prompt...>     required. The words after the chat.
  query flags     -p, -prp, -cm, ... go after `fork` and before the chat, as for `chat continue`; they apply to
                  the run that sends the prompt. A -cm among them chooses the model; with none the run uses the profile's model (P13).

stdout   what `chat continue <F> <prompt>` prints for the sent prompt (the answer, the usage line with
         "(N cached)"); no preview of the parent.
stderr   one line, at the default log level: `fork <F> of <P>` (P4). Under -json it is a record of the
         chat subsystem instead, and stdout carries only the run's records.
exit 0   the prompt was sent and answered.
exit 1   every refusal below, `cc:` with no resolver injected included; the run's own errors after the fork was written (the fork stays on disk).
```

Clauses:

- `fork.1` Refusal before any read of the store or write: no prompt (empty, or only spaces) exits 1 with exactly `chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'` (P6); no file, index row, directory binding or request exists afterwards.
- `fork.2` A parent that cannot be found exits 1 with an error naming the first token (`chat fork: could not find chat "<token>"`); unlike `chat continue` it never falls back to the list UI, and writes nothing.
- `fork.3` The fork F is written by the `Forker` (below) before any request is sent (I3); then one notice `fork <F> of <P>` (P4) is reported; then the working directory is bound to F, as `chat continue <id> <prompt>` binds it; then the prompt is sent to F through `CommandDeps.SendPrompt` as a `ContinueRequest{ChatID: F, Prompt, Profile, Raw, Flags}`.
- `fork.4` `Profile` of the request is the parent's profile; a parent with none takes the `-p` value, stamped on F only. The parent is not stamped (I2).
- `fork.5` No model is carried over; the request's `Flags` are the fork's own query flags (P13). The run uses the profile's model, as `chat continue` does, and a `-cm` among the flags chooses another. `text.ContinueSender` is unchanged: it already appends `req.Flags` after the profile arguments.
- `fork.6` The request F's run sends is the parent's last request, unchanged, plus the prompt (I1): same system prompt and tools (same profile and, when the fork runs on the parent's model, same model), the parent's messages in order, then the new user message. Proven end to end in phase 6; at this node by `Test_fork_requestFlagsMatchParent`.
- `fork.7` A `SendPrompt` of nil (a handler wired without a sender) is a refusal before any write: `chat fork: this build cannot send a prompt`.
- `fork.8` After a failed send, F stays: its file and index row exist and `chat continue <F>` works. The error is `failed to send prompt to chat <F>: <cause>`, exit 1.
- `fork.10` (I10) The fork carries the parent's `Queries` and `TokenUsage` and `Created` is the fork time, so `chatOwnSpend(F)` is zero until F's own run adds a query, and the parent's spend is counted once across the pair.
- `fork.11` The parent token is resolved by `findChatByRef` (below), so `cc:<id>` works for `fork` and `continue` alike (P7); with no resolver injected the verb is refused as `chatref.5` says.
- `fork.9` `chat delete`, `chat list` and every other verb behave as before. `chat help`, `chat -h`, the top-level `clai -h` line for `chat` and the `chatUsage` text list `fork`; the `chat continue` and `chat fork` help texts name the `cc:<chatID>` form; the existing expectations that pin those texts (`internal/chat/cmd_test.go`, `e2e/nested_e2e_test.go`, `e2e/dispatch_e2e_test.go`, `e2e/testdata/diagnostics/help.golden`) change in this phase, by hand or by their documented regeneration command.

### Chat resolver and cloud hook

`findChatByRef(ctx, token, foreign)` in `internal/chat/chat_ref.go` (new) is the one resolver `chat continue` and `chat fork` call (`cont` changes its `findChatByID` call to it). It has a local branch, which is `findChatByID` unchanged, and a `cc:` branch that calls an injected `CloudResolver`. Phase 4 implements `CloudResolver`; the composition root injects it; this phase never imports the cloud packages.

```go
// ChatRef is a chat argument: a local ID or index, or a cloud reference.
type ChatRef struct {
	Cloud bool   // the argument was cc:<value>
	Value string // the ID for a cloud reference; the whole token otherwise
}

// ParseChatRef splits the first token of a chat argument. It never fails: "cc:" with no ID is
// {Cloud: true, Value: ""}, which the resolver refuses (chatref.4). `cc:3` is the conversation "3": a cloud
// reference never takes a list index.
func ParseChatRef(token string) ChatRef

// CloudResolver makes this machine's copy of a cloud conversation current and returns it.
type CloudResolver interface {
	ResolveCloud(ctx context.Context, id string) (CloudResolution, error)
}

// CloudResolution is the local chat after resolution and the notice to report, "" for none.
type CloudResolution struct {
	Chat   pub_models.Chat
	Notice string
}

// CommandDeps gains the factory (cmd.go). It runs only when a cc: reference is used, so a machine that never
// logged in meets the login error only then. Nil means chatref.5.
//   CloudResolver func(ctx context.Context, confDir string) (CloudResolver, error)

// CloudRefError is a cc: reference that could not be resolved; Error() is the cause's text.
type CloudRefError struct {
	ChatID string
	Cause  error
}

func (e *CloudRefError) Error() string { return e.Cause.Error() }
func (e *CloudRefError) Unwrap() error { return e.Cause }

// ErrCloudRefsUnavailable is a cc: reference with no resolver injected.
var ErrCloudRefsUnavailable = errors.New("clai.chat references are not available in this build")
```

- `chatref.1` A token that is not a cloud reference resolves exactly as `findChatByID` does, with the same prompt remainder and errors.
- `chatref.2` `cc:X` calls `ResolveCloud(ctx, X)`; on success the `Notice`, when not empty, is reported at the default log level (a record under `-json`), the returned chat is the parent, and the rest of the argument string is the prompt.
- `chatref.3` Any resolver or factory error is returned as `*CloudRefError{ChatID, Cause}`; `chat continue` and `chat fork` return it as it is (exit 1) and never fall back to the list UI.
- `chatref.4` `cc:` with no ID exits 1 with `chat <verb>: "cc:" needs a conversation ID, as in cc:<chatID>`; the resolver is not called.
- `chatref.5` With `CommandDeps.CloudResolver` nil, `cc:X` exits 1 with `chat <verb>: clai.chat references are not available in this build` (`ErrCloudRefsUnavailable` in the chain); nothing is written.
- `chatref.6` `chat delete cc:X` exits 1 with `chat delete: cc: references are for continue and fork; delete the local copy with 'clai chat delete X'`; nothing is deleted. Other verbs do not read `cc:`.
- `chatref.7` The resolver is called before any write by `fork`, and after the prompt check (`fork.1`), so a refused prompt never reaches the network.

### Own spend

`internal/chat/own_spend.go` (new). `chatOwnSpend` is declared here, beside the `Forker`, because I10 binds this phase's fork copy; phase 5 stores and totals it, and phase 6 asserts it on the fork a `cc:` pull keeps.

```go
// OwnSpend is what a chat spent itself.
type OwnSpend struct {
	Tokens  int
	CostUSD float64
}

// chatOwnSpend totals the queries a chat created itself (I10).
//  ownSpend.1 A query counts when CreatedAt is at or after chat.Created.
//  ownSpend.2 A query with a zero CreatedAt counts unless the chat is a fork (Source "clai" with a SourceID).
//  ownSpend.3 Tokens are the sum of the counted queries' Usage.TotalTokens (aggregateQueryTotalTokens); a chat
//             with no queries at all that is not a fork falls back to TokenUsage.TotalTokens, as TotalTokens does.
//  ownSpend.4 A fork with no queries has zero own tokens, whatever TokenUsage it inherited. Cost is the sum
//             of the counted queries' CostUSD.
func chatOwnSpend(chat pub_models.Chat) OwnSpend
```

### Forker

```go
// Forker writes a fork of a conversation and returns it as written.
type Forker interface {
	Fork(ctx context.Context, parent pub_models.Chat) (pub_models.Chat, error)
}

// NewForker builds the Forker over a Persister. newID and now are injected (chatid.New, time.Now).
func NewForker(p Persister, newID func() (string, error), now func() time.Time) Forker

var ErrForkParentUnsaved = errors.New("the chat to fork has no ID")

// ForkError is a fork that could not be written.
type ForkError struct {
	ParentID string
	Cause    error
}

func (e *ForkError) Error() string { return "fork chat " + e.ParentID + ": " + e.Cause.Error() }
func (e *ForkError) Unwrap() error { return e.Cause }
```

- `Forker.1` The fork's ID is new (`chatid.New`), `Created` is the fork time, `Source` is `"clai"` and `SourceID` is `parent.ID` (I3).
- `Forker.2` Every other field is the parent's: `Messages` (reasoning items included), `GroupKey`, `Title`, `Summary`, `SummaryAt`, `Profile`, `OriginDir`, `Queries` (each with its `CreatedAt`, model and cost), `TokenUsage` and `RecentTokenUsage` (I3). Slices and pointers are copies: changing the fork never changes the parent value passed in.
- `Forker.3` The fork's conversation file, its index row and its reasoning sidecar are written before `Fork` returns, through the `Persister`; its images are shared by ref, none is copied.
- `Forker.4` The parent's conversation file, index row and sidecar are not read for writing, touched or rewritten (I2).
- `Forker.5` Concurrent calls for one parent write one complete fork each, with distinct IDs (I4).
- `Forker.6` A parent with an empty ID returns `ErrForkParentUnsaved` and writes nothing. A failure of the `Persister` is returned as `*ForkError` and leaves no file referenced by the index.
- `Forker.8` A fork's own spend is zero until it queries: `chatOwnSpend(fork)` is `{0, 0}` when the parent is saved with queries made before the fork time (I10).

Consumes:

- `aggregateQueryTotalTokens`, `ComputeGroupKey` (repository code, `internal/chat`).
- `findChatByID`, `convPersister`, `NewPersister`, `UpdateDirScopeFromCWD`, `CommandDeps.SendPrompt`, `ContinueRequest` (repository code, `internal/chat`).
- `chatid.New` (`internal/chatid`).
- `previous-query-pointer:SavePreviousQuery (import)`: the sent run's finalize names F as the previous query through the unchanged `text` finalize path; this phase adds no call to it.
- `previous-query-pointer:LoadPreviousQuery (import)`: read only by the tests, to assert F is the record's chat.

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | `chat fork 0` and `chat fork 0 "  "` exit 1 with the exact P6 text; no conversation file, index row, binding, notice or request | fork.1, P6 | `Test_fork_withoutPromptIsRefused` |
| 2  | A fork's file holds the parent's messages, GroupKey, Title, Summary, Profile, OriginDir, Queries, TokenUsage and RecentTokenUsage, and a new ID, fork-time Created, `Source "clai"` and `SourceID` the parent's ID | Forker.1, Forker.2, I3 | `TestForker_stampsAndCopies` |
| 3  | The parent's file bytes, index row and sidecar are identical before and after | Forker.4, I2 | `TestForker_neverWritesTheParent`, `Test_fork_parentUntouched` |
| 4  | 16 concurrent forks of one parent leave 16 complete files with distinct IDs and 16 index rows | Forker.5, I4 | `TestForker_concurrentForks` |
| 5  | The fork exists (file and row) before the sender is called | fork.3, I3 | `Test_fork_writesBeforeSending` |
| 6  | The sender gets the fork's ID, the prompt, the parent's profile, and exactly the query flags; no `-cm` is added | fork.3 to fork.5, P13 | `Test_fork_requestFlagsMatchParent` |
| 7  | stderr carries `fork <F> of <P>`; under `-json` it is a chat-subsystem record and stdout holds only records | P4 | `Test_fork_noticeLine` |
| 8  | A parent that is a listed external row is cloned, then forked | fork.2, Scope | `Test_fork_externalRowIsClonedFirst` |
| 9  | The reasoning sidecar and image refs of the parent are present on the fork | Forker.2, Forker.3 | `TestForker_sidecarAndImages` |
| 10 | `fork` appears in `chat -h`, `chat help` and the top-level help; the changed expectations pass | fork.9 | `TestCommand_forkSub`, `Test_e2e_nested_help`, `TestDiagnosticOutputGoldens` |
| 11 | A fork carries the parent's queries and its own spend starts at zero; `chatOwnSpend` follows its four clauses | fork.10, Forker.8, ownSpend.1 to ownSpend.4, I10 | `TestForker_ownSpendStartsAtZero`, `TestChatOwnSpend` |
| 12 | `cc:X` resolves through an injected resolver for `fork` and `continue`; the refusals are as specified | chatref.1 to chatref.7, P7 | `Test_chatRef_localBranch`, `Test_chatRef_cloudBranch`, `Test_chatRef_refusals`, `Test_parseChatRef` |

## Test cases

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestForker_stampsAndCopies` | contract suite (run against the real `Forker` over a temp-dir `Persister`, and a fake `Persister` that records `Save` calls) | Forker.1, Forker.2 | a parent with 4 messages, 2 queries on 2 models, usage, title, summary, profile, origin dir, group key | the returned chat and the saved file differ from the parent in ID, Created, Source, SourceID only; `Queries[i]` deep-equal; mutating the fork's slices leaves the parent value intact | share a backing array with the parent |
| `TestForker_neverWritesTheParent` | contract suite | Forker.4, I2 | a saved parent; SHA-256 of its file, its index row, its sidecar dir listing and mtimes recorded | all equal after `Fork` | call `Save` with the parent's ID |
| `TestForker_concurrentForks` | contract suite (`-race`) | Forker.5, I4 | 16 goroutines forking one parent, errors sent on a channel to the test | 16 distinct IDs, 16 loadable files, 16 index rows, parent unchanged | lose a row, share an ID |
| `TestForker_sidecarAndImages` | contract suite | Forker.2, Forker.3 | a parent with a tool-call turn that has reasoning items, and an image part by ref | the fork's `reasoning/<F>/` holds the same files; `conversations/assets/` has no new file | copy an asset |
| `TestChatOwnSpend` | unit (table) | ownSpend.1 to ownSpend.4, I10 | a parent with queries at T1, T2; a fork created at T3 carrying them plus a query at T4; a zero-time query on a non-fork and on a fork; a no-query chat with `TokenUsage`; a fork with only inherited `TokenUsage`; a chat with a zero `Created` | own spend: parent T1+T2, fork T4, the zero-time query counted only for the non-fork, tokens via fallback, zero for the fork, every dated query counted for zero `Created`; no panic | count an inherited query |
| `TestForker_ownSpendStartsAtZero` | contract suite | Forker.8, fork.10, I10 | a parent with two queries and an injected `now` after them | `chatOwnSpend(fork)` is `{0, 0}` and `chatOwnSpend(parent)` is the parent's whole spend | change the copied queries |
| `Test_chatRef_localBranch` | unit | chatref.1 | tokens `3`, an ID, an unknown ID, an out-of-range index | the same chat, prompt remainder and errors as `findChatByID` | call the resolver |
| `Test_chatRef_cloudBranch` | unit (fake `CloudResolver`) | chatref.2, chatref.3, chatref.7 | `cc:X do it`; a resolver returning `CloudResolution{Chat, "n"}`; one returning an error; `fork cc:X` with no prompt | the chat and prompt `do it`, the notice `n` reported once; `*CloudRefError` with the cause, exit 1, no list UI; the P6 refusal with zero resolver calls | fall back to `handleListCmd` |
| `Test_chatRef_refusals` | command (table) | chatref.4 to chatref.6 | `continue cc:`, `fork cc:`, `delete cc:X`, `continue cc:X` and `fork cc:X p` with a nil factory, a factory that errors | the exact messages; nothing written or deleted | call a resolver that is nil |
| `Test_parseChatRef` | unit (table) | chatref.1, chatref.4 | `cc:abc`, `cc:`, `abc`, `3`, `cc:3`, `CC:abc` | `{true abc}`, `{true ""}`, `{false abc}`, `{false 3}`, `{true 3}`, `{false CC:abc}` | trim or lower-case |
| `TestForker_emptyParentID` | contract suite | Forker.6 | `Chat{}` | `errors.Is(err, ErrForkParentUnsaved)`; no file | write anything |
| `TestForker_persisterFailure` | contract suite | Forker.6 | a `Persister` whose `Save` fails | `errors.As(err, *ForkError)` with `ParentID`, cause unwrappable | leave an index row for the failed fork |
| `TestForker_idFailure` | unit | Forker.6 | `newID` returns an error | wrapped in `*ForkError`; nothing written | panic |
| `Test_fork_withoutPromptIsRefused` | command (in-process through `Command(deps)` with a fake sender) | fork.1, P6 | `fork 0`, `fork 0 `, `fork` | each exits with the exact P6 error; sender not called; conversations dir byte-identical | write, bind or print a preview |
| `Test_fork_parentNotFound` | command | fork.2 | `fork nope hello` | error `chat fork: could not find chat "nope"`; no list UI output | fall through to `handleListCmd` |
| `Test_fork_writesBeforeSending` | command | fork.3, I3 | a sender that, when called, loads the fork by `req.ChatID` | the file and index row already exist and hold the parent's messages without the prompt | send before the write |
| `Test_fork_requestFlagsMatchParent` | command | fork.3 to fork.5, P13 | table: no flags; flags `-cm=m2` with a parent last query on `m1`; a parent last query on `m1` with no flags; flags `-n`; parent profile `p`; parent with none and `-p q` | request flags: `[]`, `[-cm=m2]`, `[]`, `[-n]`; profile `p`; `q` stamped on the fork and not on the parent | add a `-cm` the user did not give |
| `Test_fork_parentUntouched` | command | I2 | parent saved, fork run with a sender that fails | parent bytes and row unchanged; the fork exists | stamp the parent's profile |
| `Test_fork_sendFailureKeepsFork` | command | fork.8 | a sender returning an error | exit error names F; F loadable | delete F |
| `Test_fork_indexUnreadable` | command | fork.2 | `fork 0 hi` with `chat_index.cache` replaced by a directory | the wrapped `failed to read chat index` error, exit 1 | write a fork |
| `Test_fork_bindFailureKeepsFork` | command | fork.3 | a conversations `dirs/` that is a file | `failed to update directory-scoped binding`; F exists; sender not called | send the prompt |
| `Test_fork_noSenderRefused` | command | fork.7 | `deps.SendPrompt` nil | refusal text; nothing written | fall back to a preview |
| `Test_fork_noticeLine` | command | P4 | default level; then `-json` | `fork <F> of <P>` on stderr; a record under `-json`, stdout only records | print it on stdout in text mode |
| `Test_fork_bindsDirectory` | command | fork.3 | a fork run from a temp cwd | `LoadDirScopeChatID` returns F | bind the parent |
| `Test_fork_externalRowIsClonedFirst` | command | fork.2 | an index whose row 0 is a foreign (claude-code) conversation | a clone exists with the foreign `Source`; F has `Source "clai"` and `SourceID` the clone's ID | fork the foreign row in place |
| `TestCommand_forkSub` | unit | fork.9 | `Command(deps).Subcommands()` | `"fork\|f"` exists, registers the query flags, and `chatUsage`/`Desc` mention it | break the existing five-verb assertions |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| No prompt, or only spaces | exit 1, `chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'`; nothing written, no request | `Test_fork_withoutPromptIsRefused` |
| Parent not found (ID unknown, index out of range) | exit 1, `chat fork: could not find chat "<token>"`; index out of range keeps `errIndexOutOfRange` in the chain; nothing written | `Test_fork_parentNotFound` |
| The chat index cannot be read | exit 1, the existing `failed to read chat index` error wrapped; nothing written | `Test_fork_indexUnreadable` |
| `SendPrompt` is nil | exit 1, `chat fork: this build cannot send a prompt`; nothing written | `Test_fork_noSenderRefused` |
| `cc:` with no ID | exit 1, `chat <verb>: "cc:" needs a conversation ID, as in cc:<chatID>` | `Test_chatRef_refusals` |
| `cc:` with no resolver injected | exit 1, `chat <verb>: clai.chat references are not available in this build` | `Test_chatRef_refusals` |
| The resolver fails | exit 1, `*CloudRefError` with the cause; nothing written; no list UI | `Test_chatRef_cloudBranch` |
| `chat delete cc:X` | exit 1, the `chatref.6` text; nothing deleted | `Test_chatRef_refusals` |
| Parent has no ID | `ErrForkParentUnsaved`, nothing written | `TestForker_emptyParentID` |
| The Persister fails to write the fork | `*ForkError`; no index row for it; no request sent | `TestForker_persisterFailure` |
| A new ID cannot be generated | `*ForkError` wrapping the cause; nothing written | `TestForker_idFailure` |
| The directory binding cannot be written | exit 1, `failed to update directory-scoped binding`; F stays; no request sent | `Test_fork_bindFailureKeepsFork` |
| The send fails | exit 1, `failed to send prompt to chat <F>: ...`; F stays | `Test_fork_sendFailureKeepsFork` |

## Golden diff

None. (The help expectations this phase changes are named under `fork.9`; the root's Golden diff records the net result.)

## Removes

None.

## Local facts

- The fork is a file copy at the model level, not a link: `Source`/`SourceID` are the only link back (I3), and its `GroupKey` puts it in the parent's group (`architecture/chat-groups.md`).
- A parent saved before `GroupKey` existed has an empty key; the fork's key is then stamped by the Persister on first persist from the fork's own first message, which equals what the parent would compute. The parent is not written (I2), so it stays ungrouped. Phase 6 documents this.
- The cloned row of an external chat is written by the existing selection mechanism before the fork; I2 binds chats that already exist.
- Copying `Queries` makes the fork's own `chat list` row show the parent's whole history; phase 5 stops group rows counting that history twice (I10). Until phase 5 is done a lineage's group total double counts: the phases land in one wave, and phase 6 asserts the totals.
- `fork` runs on the profile's model and carries no model over: a chat records only the bare model name, not its vendor route (`or:`, `berget:`, `novita:`, `hf:`). A fork of a chat run with `-cm` repeats `-cm` to read the same cache.
- `text.ContinueSender` and `continueArgs` build the run's arguments: `-r`, the profile arguments, `req.Flags`, `--`, the prompt, and are unchanged by this phase.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command | Result |
| ----- | ----------- | ------- | ------ |
| start | 6658380 | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa QA_PROCS=5; go vet ./...; dupl -t 80 .` | qa exit 0 (all packages ok), vet 0, dupl 49 clone groups |
| end   | 6658380+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa QA_PROCS=5; go vet ./...; dupl -t 80 .` | qa exit 0 (108 packages ok, 0 failed; internal/chat 88.0% coverage), vet 0, dupl 49 clone groups |
| end   | 6658380+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa QA_PROCS=5; go vet ./...; dupl -t 80 .` | qa exit 0 (108 packages ok, 0 failed), vet 0, dupl 49 clone groups |
| ported | 615303b+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa; go vet ./...; dupl -t 80 .` | Ported from the worktree onto 615303b (branch), after phases 1 and 3. Reconciled: `handler.go` keeps the branch's `-re` code (`LoadPreviousQuery` fallback in `cont`, `clearPreviousQueryRecord` in delete) with `findChatByRef` routing, `isCloudRefFailure`, `refuseCloudDelete`, the `fork` case, the two new fields and the usage lines added on top; `memPersister.Promote` in `forker_test.go` takes the branch's `(ctx, chat, RunOutcome)` signature. `help.golden` regenerated (only the `chat\|c` line changed). qa exit 0 (three runs), vet 0 |
| fix-start | 615303b+wip | `make qa; go vet ./...; dupl -t 80 .` | R1-20; baseline was the review's pass |
| fix-end | 615303b+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa; go vet ./...; dupl -t 80 .` | qa exit 0, vet 0, dupl 49 groups outside `.claude/worktrees`; R1-20 closed by `Test_fork_noticeLineJSON` |
| replan | 615303b+wip | F6-01 | P13 amended by the maintainer (no model carry-over): `fork.5` reworded; `LastQueryModel`, `ErrNoQueryModel` and `HasFlagToken` removed from the Interface (no other user); acceptance row 6, `Test_fork_requestFlagsMatchParent` re-pointed; `TestLastQueryModel` and `TestHasFlagToken` rows removed |

Andon: none.
Surprises:

- The worktree HEAD is 6658380, not 615303b as the brief said; the start gate passed on it.
- `internal/cli` `TestUsageErrorHints` pins the `-cm` hint text, which is generated from the command tree and now also names `chat fork`; the expectation was updated (not listed under fork.9).
- Tests are in `chat_ref_test.go`, `forker_test.go`, `handler_fork_test.go`, `own_spend_test.go`. The `-json` record variant of `Test_fork_noticeLine` is not covered at unit level (the notice goes through `runenv.Noticef`, whose `-json` rendering is runenv's own contract); only the default-level stderr line is asserted.
- `HasFlagToken` and `LastQueryModel` were removed by the F6-01 re-plan; `internal/text/chat_continue.go` is as at 615303b.
- Docs: `architecture/chat.md` gained a `chat fork` section; `help.golden` regenerated (only the chat line changed).

## Review findings

### Review 1, 2026-11-23

- [ ] R1-20 minor `internal/chat/handler_fork_test.go:292` `Test_fork_noticeLine` asserts only the default-level stderr line; acceptance row 7 and `Test_fork_noticeLine` in Test cases also require the `-json` variant (a chat-subsystem record, stdout holding only records). The implementation notes admit the gap. Add the `-json` case (runenv with a JSON renderer) or narrow the row.
- note: gates run twice (`make qa QA_PROCS=5` exit 0 both times, 104 packages ok, `internal/chat` 87.7%; `go vet ./...` exit 0; dupl 49 clone groups outside `.claude/worktrees`, none in the phase's files).
- note: the `-re` behaviour of the branch survived the port: `cont` with an empty prompt still falls back to `LoadPreviousQuery` (`handler.go:275`), and `deleteFromPrompt` still calls `clearPreviousQueryRecord` (`handler.go:362`) after the `refuseCloudDelete` guard.
- note: every test row of Test cases and Acceptance criteria exists under its named function; the `fork.1` to `fork.8`, `fork.10`, `fork.11`, `chatref.1` to `chatref.7`, `Forker.1` to `Forker.8` and `ownSpend.1` to `ownSpend.4` code paths were traced, including the failure paths (nil sender, unreadable index, bind failure, send failure, resolver failure); no write happens before the checks the clauses name, and the parent is never passed to `Persister.Save`.
- note: `chat_ref.go`, `forker.go` and `own_spend.go` match the Interface block; only the declared members plus unexported helpers (`refRefusal`, `forkParentNotFoundError`, `verbName`) are added. Docs: `architecture/chat.md`, `help.golden`, usage text and the `-cm` hint test updated.

### Review 2, 2026-11-23

- [ ] R2-20 minor `worklogs/2026-11-19-chat-forks/phase-2-fork-verb.md:7` the Goal still says the prompt is sent "on the parent's model", and `fork.6` (line 42) says "same profile and model"; P13 and `fork.5` say the run uses the profile's model, so a fork of a chat run with `-cm` is not on the parent's model. Reword the Goal to the profile's model, and `fork.6` to "same profile", the model being equal only when the profile's model is the parent's.
- note: round 1 held: R1-20 is closed by `Test_fork_noticeLineJSON`; the parent is still never passed to `Persister.Save` (`Forker` unchanged, `Test_fork_parentUntouched` and `TestForker_neverWritesTheParent` pass); `-re` kept (`cont` falls back to `LoadPreviousQuery`, delete still clears the record).
- note: `LastQueryModel`, `ErrNoQueryModel`, `HasFlagToken` and `forkFlags` have no hit outside the worklog's own history rows; `internal/text/chat_continue.go` is identical to 615303b, so `text.ContinueSender` appends `req.Flags` as `fork.5` says.
- note: `handler_fork.go` sends `slices.Clone(cq.queryFlags)` and `parent.Profile`; `Test_fork_requestFlagsMatchParent` rows (`-cm=m2` passes through; a parent last query on `m1` adds nothing; `-n` kept; profile stamped on the fork only) match acceptance row 6 and P13. The test name predates the re-plan.
- note: fork help text (`internal/chat/cmd.go:113`), `architecture/chat.md:306`, `continue-from-claudex.md:948` and `streaming.md:178` say the same: the profile's model, no carry-over, repeat `-cm` to fork a chat run with `-cm`. `chat-groups.md` and `cloud.md` changes concern lineage only.

### Review 3 (root), 2026-11-23

- [ ] R3-05 (minor, owner fix) `architecture/chat.md:300,309,321,347`: the `### chat fork` section sits between `chat continue`'s body and its `#### Output format (preview)` and replyable notice, which now read as part of fork (fork prints no preview); line 347 cites "(I9: ...)", a worklog invariant ID that means nothing in `architecture/`. Item: move the fork section after `chat continue`'s subsections and drop the ID.
- note: `fork.6` holds for an explicit tool list and for no tools; with every tool or a tool glob the request's tool order changes per process (R3-01, phase 6).
