# Phase 3 — One-conversation pull

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

`Engine.PullConversation` pulls one conversation, its reasoning sidecar and its images from the account's `conversations` store, settles it against the local copy with `clai cloud pull`'s own merge so the cloud copy keeps the ID and no local message is lost, and records it as in sync so the next `clai cloud pull` has nothing to do for it.

## Interface

| File | Change |
| ---- | ------ |
| `internal/cloud/sync/conversation.go` (new) | `PullConversation` and its four steps |
| `internal/cloud/sync/merge.go` | `forkTheirs` becomes a call of `forkCopy`; `clai cloud pull` output is unchanged |
| `internal/asset/refs.go` (new) | `RefsIn`, moved out of `internal/chat/persister.go`, because `sync` cannot import `chat` |
| `internal/cloud/cloudtest/server.go` | `FetchedHashes`, so tests can see which blobs a pull fetched |

### How a pull runs

```text
PullConversation(X)
  │
  ├─ X is not a valid conversation ID ── InvalidConversationIDError, no request
  ├─ sync state unreadable ───────────── StateError, no request
  │
  ▼
fetchConversation ── download X.json, reasoning/X/*, and the images X references that are missing here
  │                  ├─ X not in the store ── ConversationNotFoundError
  │                  └─ a blob is wrong ───── ChecksumError, MissingBlobError
  │                  nothing local has changed yet
  ▼
is there a local X?
  ├─ no ────────────────────── Adopted: skip to writeCloud
  ├─ unreadable, or not JSON ── LocalConversationError, nothing changed
  │
  ▼
settle ─────────────── compare local and cloud X (decision table below)
  ▼
putAside ──────────── Unchanged or Replaced: old local X to cloud/trash/ (when its bytes differ)
  │                    LocalForked: old local X saved as fork L
  ▼
writeCloud ────────── the cloud's X.json, reasoning files and images into place
  ▼
recordInSync ──────── base in cloud/state, chat index told about X (and L)
```

### Deciding the outcome

| Local X compared with the cloud copy | `mergeChats` says | Outcome | The old local X |
| ------------------------------------ | ----------------- | ------- | --------------- |
| No local X | (not called) | Adopted | none |
| Same messages | `keepRemote` (identical), `replaceLocal`, or `keepLocal` with equal message counts | Unchanged | to `cloud/trash/` if its bytes differ |
| Fewer messages, same start | `keepRemote` | Replaced | to `cloud/trash/` |
| More messages, same start | `keepLocal` with more local messages | LocalForked | saved as fork L |
| Diverged | `keepBoth` | LocalForked | saved as fork L |

`mergeChats` is reused unchanged. Only `PullConversation` reads its `keepLocal` by message count; `clai cloud pull` keeps its own policy.

### Entry point

```go
// ConversationPuller pulls one conversation from the cloud store; the cloud copy keeps the ID. Engine implements it.
type ConversationPuller interface {
	PullConversation(ctx context.Context, id string) (ConversationPull, error)
}

type ConversationPull struct {
	Outcome   ConversationOutcome
	ID        string // the pulled conversation
	TrashPath string // where the old local file went, under cloud/trash/, when it was moved
	ForkID    string // LocalForked: the ID of fork L
}

type ConversationOutcome int

const (
	ConversationUnchanged ConversationOutcome = iota + 1
	ConversationAdopted
	ConversationReplaced
	ConversationLocalForked
)
```

- `PullConversation.1` Runs the steps in the diagram's order. Nothing local changes before `fetchConversation` holds every blob and the local X has been read.
- `PullConversation.2` The outcome is the decision table's (P9).
- `PullConversation.3` No local message is lost: when the cloud files are written, the old local X is identical to the cloud copy, in `cloud/trash/`, or in fork L (I8).
- `PullConversation.4` Afterwards `clai cloud pull` has nothing to do for X (I7). Nothing is uploaded.
- `PullConversation.5` Every failure is one of the Errors table's. A failure before `putAside` leaves everything local as it was.
- `PullConversation.6` Before any request, an ID that cannot name `conversations/<id>.json` is `*InvalidConversationIDError`, and an unreadable sync state is `*StateError`.
- `PullConversation.7` A local X that cannot be read, or is not JSON, is `*LocalConversationError`, before `settle`.

### Steps

```go
func (e *Engine) fetchConversation(ctx context.Context, id string) (cloudConversation, error)
func settle(local, cloud []byte) ConversationOutcome
func (e *Engine) putAside(id string, local []byte, outcome ConversationOutcome, cloud cloudConversation) (ConversationPull, error)
func (e *Engine) writeCloud(c cloudConversation) (written []string, err error)
func (e *Engine) recordInSync(st *state, c cloudConversation, written []string) error // st is read before fetching

// cloudConversation is every file of one conversation as the store holds it.
type cloudConversation struct {
	ID     string
	Chat   RemoteFile   // conversations/<id>.json
	Notes  []RemoteFile // conversations/reasoning/<id>/*
	Images []RemoteFile // referenced images this machine lacks
}

type RemoteFile struct {
	Entry RemoteEntry
	Bytes []byte
}
```

- `fetch.1` Fetches only X's files: its chat file, its reasoning files and the referenced images this machine lacks (`asset.RefsIn`). It finds them by paging the store's change list once (P12).
- `fetch.2` Every blob must match its entry's hash (`*ChecksumError`). An ID with no live entry is `*ConversationNotFoundError`.
- `settle.1` Returns the decision table's outcome for an existing local copy. No I/O.
- `putAside.1` Unchanged with differing bytes, and Replaced: the old local file moves to `cloud/trash/` (`Engine.trash`).
- `putAside.2` LocalForked: the old local X and its reasoning files are saved as fork L through `forkCopy`. L gets a new ID from `chatid.New`, `source "clai"`, `source_id` X, and `created` = its first query the cloud copy lacks, else now. A local query counts as the cloud's when one there has the same `created_at` and model (I3, I8).
- `putAside.3` Finishes before `writeCloud` starts (I8).
- `writeCloud.1` Afterwards `X.json` holds the cloud bytes, `reasoning/X/` holds exactly the cloud's files, and every referenced image exists (I7).
- `record.1` Every written or kept path gets the pulled revision as its base, and open conflicts on those paths are cleared. A reasoning file removed here loses its base and its hash, so the next commit does not upload a deletion. The cursor is untouched (I7).
- `record.2` `Hooks.Pulled` is told which chat files were written (X, and L), so the chat index follows.

### Fork stamping, shared with `clai cloud pull`

```go
func forkCopy(rel string, raw []byte, id string, stamp forkStamp) (path string, out []byte, title string, err error)

type forkStamp struct {
	Title   string    // replaces "title" when set; otherwise the title is kept, or becomes the first user message when empty
	Created time.Time // replaces "created" when set
}
```

- `forkCopy.1` The output has the given `id`, `source "clai"` and `source_id` = the parent; every other member is unchanged except what the stamp sets. An empty title becomes the first user message's text, as `forkTheirs` does today (I3).
- `forkCopy.2` `forkTheirs` returns exactly what it returned before this phase, so pull is unchanged.
- `forkCopy.3` Input that is not a JSON object is an error.

### Errors

```go
type ConversationNotFoundError struct{ ID string }
type InvalidConversationIDError struct{ ID string }
type MalformedConversationError struct{ ID string; Cause error }
type LocalConversationError struct{ ID string; Cause error }
type MissingBlobError struct{ Path, SHA256 string }
type LinkedConversationError struct{ Path string }
```

| Error | When |
| ----- | ---- |
| `*ConversationNotFoundError` | the store has no live `conversations/<id>.json` |
| `*InvalidConversationIDError` | the ID cannot name `conversations/<id>.json` (`Allowed`), before any request |
| `*MalformedConversationError` | the cloud chat file does not decode, or names an image the store refuses |
| `*LocalConversationError` | the local chat file cannot be read or decoded, before `settle` |
| `*MissingBlobError` | the server lacks a listed blob or a referenced image |
| `*LinkedConversationError` | a local path goes through a symbolic link |
| a fork-ID error, wrapped | `chatid.New` fails; nothing has changed |
| `*ChecksumError`, `*BundleError`, `*StateError`, `cloud.APIError`, `cloud.TransportError` | as in `Pull`, passed through unchanged |

### Test support

```go
func RefsIn(conversation []byte) ([]string, error) // package asset
// cloudtest.Server gains: FetchedHashes []string
```

- `RefsIn.1` Returns the image refs a chat file references, sorted and each once. A part that does not decode is an error naming the message index.
- `FetchedHashes.1` Holds the blob hashes clients fetched, in request order; no other route records into it.

Consumes:

- Repository code in `internal/cloud/sync`: `Engine` and its `fetch`, `trash`, `pruneTrash`, `hookPulled`, `setBase`, `now`, `linkage` and `abs`; also `api.allChanges`, `mergeChats`, `forkTheirs`, `newChatID`, `loadState`, `state.save`, `writeFile`, `removeFile`, `hashBytes`, `Allowed`, `StoreConversations`, `TheirsPath`, `baseEntry`, `conflict`, `RemoteEntry`, `Hooks.Pulled`, `ChecksumError`, `BundleError` and `StateError`.
- `chatid.New` (repository code): the ID of fork L (I3).
- `cloud.Client`, `cloud.APIError`, `cloud.AsAPIError` and `cloud.TransportError` (repository code).
- `cloudtest.Server` as the fake clai.chat, with `Put`, `Delete`, `Content`, `Requests`, `CorruptFetch`, `PageSize` and the `FetchedHashes` addition above.

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | With no local `X`, the cloud bytes, two sidecar files and two images are written and the outcome is Adopted | PullConversation.2, writeCloud.1, I7 | `TestPullConversation_adopts` |
| 2  | Equal messages (identical bytes, other fields differing, or a titled local copy over an untitled cloud one): the cloud bytes are written, the outcome is Unchanged, nothing is trashed when the bytes were equal and the old local file is trashed otherwise | settle.1, putAside.1, I8 | `TestPullConversation_unchanged` |
| 3  | A shorter local `X` is in `cloud/trash/conversations/X.json.<time>` and the cloud copy is written | settle.1, putAside.1, I8 | `TestPullConversation_replacesShorterLocal` |
| 4  | A local `X` ahead of, or diverging from, the cloud copy becomes fork `L` holding every local message, `source "clai"`, `source_id X`, its sidecar copied; `X` is the cloud copy; `L` exists before `X.json` changes | settle.1, putAside.2, putAside.3, PullConversation.3, I3, I8 | `TestPullConversation_forksLocal`, `TestPullConversation_forkID` |
| 5  | `L`'s `created` is the `created_at` of the first local query the cloud copy lacks, else the engine's now | putAside.2, I8 | `TestPullConversation_forkCreated` |
| 6  | After a pull, `X.json` is the cloud bytes, the sidecar directory is exactly the cloud's files, every referenced image exists | writeCloud.1, I7 | `TestPullConversation_writesExactly` |
| 7  | The state records `X.json`, its sidecar and its images at the pulled revision, leaves cursor, commit and every other path alone, and a following `Pull(StoreConversations)` does nothing for them | record.1, I7 | `TestPullConversation_recordsBase` |
| 8  | Only the conversation's blobs are fetched, however many others the store holds | fetch.1, P12 | `TestPullConversation_fetchesOnlyItsBlobs` |
| 9  | The chat index hook receives the written conversation files | record.2 | `TestPullConversation_hookPulled` |
| 10 | An unknown, deleted, invalid or malformed conversation, a missing or corrupt blob, a linked path and an unreadable local copy fail with the specified types and change nothing | fetch.2, PullConversation.1, PullConversation.5, PullConversation.6, PullConversation.7 | `TestPullConversation_failures` |
| 11 | Nothing is uploaded and no commit is sent | PullConversation.4, Scope | `TestPullConversation_writesNothingToServer` |
| 12 | `Engine.Pull`'s behaviour is byte for byte as before: the existing pull, merge and fork tests pass unchanged, and `forkTheirs` returns what it did | forkCopy.2 | `engine_test.go`, `download_test.go`, `hardening_test.go` (existing, unchanged), `TestForkTheirs_unchanged` |
| 13 | `asset.RefsIn` returns the refs the old private scan found | RefsIn.1, I7 | `TestRefsIn`, and the existing sweep tests in `persister_test.go` unchanged |
| 14 | `internal/chat` has no second copy of the scan, and `go vet` is clean | RefsIn.1 | `dupl gate` (command row below) |
| 15 | The fixture records fetched hashes | FetchedHashes.1 | `TestServer_recordsFetchedHashes` |

## Test cases

All sync rows build a `machine` as `helpers_test.go` does (a temp `ConfDir`, an `Engine` over `cloudtest.Server`, `Now` fixed, a recording `Hooks.Pulled`).

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestPullConversation_adopts` | contract suite (real `Engine` over `cloudtest`) | PullConversation.2, writeCloud.1 | `X` on the server with two sidecar files and two images; no local `X` | `Adopted`; `X.json` equals `srv.Content`; both sidecar files and images exist | write other conversations |
| `TestPullConversation_unchanged` | contract suite (table: identical bytes; same messages, other title; same messages with a titled local copy and an untitled cloud copy, which mergeChats calls keepLocal) | settle.1, putAside.1 | each local/cloud pair | `Unchanged`; file bytes equal the cloud's; trash empty for identical, holds the old bytes for the differing and the titled pairs (`TrashPath` set) | fork, even for the titled pair |
| `TestPullConversation_replacesShorterLocal` | contract suite | settle.1, putAside.1 | local messages `[a b]`, cloud `[a b c d]`, `Now` fixed | `Replaced`; trash file `cloud/trash/conversations/X.json.20261026T120000Z` holds the old bytes | create a fork |
| `TestPullConversation_forksLocal` | contract suite (table: local ahead `[a b c]` vs cloud `[a b]`; diverged `[a b x]` vs `[a b c]`; one message differing in content) | settle.1, putAside.2, putAside.3, PullConversation.3 | each pair, with a local sidecar | `LocalForked`; `L` is a new chat file with every local message, `source "clai"`, `source_id X`, other members the local copy's, sidecar copied; `X` equals the cloud copy; an `OnCommit`-style probe sees `L` on disk before `X.json` changes | change `Pull`'s keepLocal behavior |
| `TestPullConversation_forkCreated` | contract suite (table) | putAside.2 | local queries `[q1 q2 q3]` and cloud `[q1 q2]`; local `[q1 q4 q5]` and cloud `[q1 q2]`; every local query in the cloud copy; no local queries | `L.created` is `q3.created_at`; `q4.created_at` (the earliest lacking); `Now()`; `Now()` | use the wall clock |
| `TestPullConversation_forkID` | contract suite | putAside.2, PullConversation.5, I3 | a LocalForked pull with the default engine; one with `newID` returning a fixed id; one with `newID` failing | `L`'s id matches `chatid.New`'s UUIDv7 shape (with `internal/chatid`'s own validity check); the fixed id; the wrapped error and no file changed | use `newChatID` |
| `TestPullConversation_writesExactly` | contract suite | writeCloud.1 | local sidecar `a.json`, `b.json`; cloud `b.json`, `c.json`; one image held, one missing | the directory holds exactly `b.json` and `c.json` with the cloud's bytes; the held image untouched (mtime), the missing one written | leave `a.json` |
| `TestPullConversation_recordsBase` | integration scenario | record.1 | a prior `Pull(StoreConfig)` and `Pull(StoreConversations)` of other files, `X` changed on the server since, then `PullConversation(X)`, then `Pull(StoreConversations)` | the second pull reports nothing added, updated, merged or conflicted for `X`'s paths; the state's cursor after `PullConversation` equals its value before; an open conflict on `X.json` is cleared and its `cloud/theirs` copy removed; a local sidecar file the cloud lacks has no base entry or hash left | advance the cursor |
| `TestPullConversation_fetchesOnlyItsBlobs` | contract suite | fetch.1, P12 | 50 other conversations, `X2`, an imported conversation, and `X` | `srv.FetchedHashes` is exactly `X`'s blob hashes | request `X2`'s blob |
| `TestPullConversation_pagedStore` | contract suite | fetch.1 | `srv.PageSize = 2`, 7 conversations, target on the last page | found | stop after the first page |
| `TestPullConversation_hookPulled` | contract suite | record.2 | a recording `Hooks.Pulled`; an Adopted pull and a LocalForked pull | the hook saw `conversations/X.json`; for the fork also `conversations/<L>.json`; a nil hook is skipped | call it with sidecar or image paths |
| `TestPullConversation_failures` | contract suite (table) | fetch.2, PullConversation.1, PullConversation.5, PullConversation.6, PullConversation.7 | an unreadable `cloud/state/conversations.json`; no entry; a deleted entry (`srv.Delete`); an id only in the config store; ids `""`, `"a/b"`, `".h"`; a cloud file `not json`; a cloud image ref `../x.png`; an image with no entry; `srv.CorruptFetch`; a blob missing from `srv.Blobs`; a symlinked local `X.json`; a local `X.json` that is not JSON; a revoked token; a closed server; a cancelled context | `*ConversationNotFoundError` (x3), `*InvalidConversationIDError` (x3), `*MalformedConversationError` (x2), `*MissingBlobError` (x2), `*ChecksumError`, `*LinkedConversationError`, `*LocalConversationError`, `*cloud.APIError` with `Unauthorized()`, `*cloud.TransportError`, the context's error; the confDir tree and the state file identical to before; invalid ids send no request | return partial data |
| `TestPullConversation_writesNothingToServer` | integration scenario | PullConversation.4 | a LocalForked pull | the server's commit count and entries are as before | upload `L` |
| `TestForkTheirs_unchanged` | unit | forkCopy.2 | the inputs the existing merge tests use, plus a title-less chat and a non-object | the same path, bytes, title and error as the pre-phase function (golden values recorded from the unmodified code first) | change the id shape |
| `TestForkCopy` | unit (table) | forkCopy.1, forkCopy.3, I3 | a chat with title and created, forked under a given id; with no title; a non-zero `stamp.Created`; `[]` | new id, `source`, `source_id`; created replaced only with a non-zero stamp; the title kept or falling back to `firstUserText`; an error and no bytes for `[]` | drop an unknown member |
| `TestServer_recordsFetchedHashes` | unit (fixture) | FetchedHashes.1 | a fetch of two hashes through the bundle route and a GET of one | the slice holds the three hashes in request order; an untouched server has an empty slice | record other routes |
| `TestRefsIn` | unit (table) | RefsIn.1 | string content, parts without image, two images one repeated, bad content array, missing `messages` | sorted unique refs; the bad array is an error naming the message index; no `messages` is empty | validate ref shape |
| `TestCollectAssetRefs_unchanged` | unit | RefsIn.1 (the sweep still finds every ref) | the existing `persister_test.go` sweep fixtures | pass unchanged | change `liveAssetRefs`' error text |
| `dupl gate` | command | criterion 14 | `go run github.com/mibk/dupl@latest -t 80 .` | no clone group beyond the count at the effort's start | leave `assetRefScan` in `internal/chat` |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| Conversation not in the store, deleted, or only in another store | `*ConversationNotFoundError{ID}`; nothing changes (PullConversation.5, fetch.2) | `TestPullConversation_failures` |
| Invalid id | `*InvalidConversationIDError{ID}`; no request (PullConversation.5, PullConversation.6) | `TestPullConversation_failures` |
| Cloud chat file does not decode, or references a disallowed image name | `*MalformedConversationError{ID, Cause}`; nothing changes (PullConversation.5, fetch.2) | `TestPullConversation_failures` |
| Local chat file unreadable or not JSON | `*LocalConversationError{ID, Cause}`; nothing changes (PullConversation.5, PullConversation.7) | `TestPullConversation_failures` |
| Blob bytes do not hash to the entry | `*ChecksumError` (PullConversation.5, fetch.2) | `TestPullConversation_failures` |
| Server lacks a listed blob, or an image has no entry | `*MissingBlobError{Path, SHA256}` (PullConversation.5, fetch.1) | `TestPullConversation_failures` |
| Fetch bundle not parseable | `*BundleError` (PullConversation.5, fetch.1) | `TestPullConversation_failures` |
| Local path reaches through a symlink | `*LinkedConversationError{Path}` (PullConversation.5) | `TestPullConversation_failures` |
| Sync state unreadable | `*StateError` before any request, as Pull (PullConversation.5, PullConversation.6) | `TestPullConversation_failures` |
| Token revoked / network failure | `*cloud.APIError` with `Unauthorized()` / `*cloud.TransportError`, unchanged (PullConversation.5, fetch.1) | `TestPullConversation_failures` |
| Context cancelled | the context's error; nothing local changed after the last complete write (PullConversation.5, PullConversation.1) | `TestPullConversation_failures` |
| A new fork ID cannot be made (`Engine.newID` fails) | that error wrapped; nothing local changes (PullConversation.5, putAside.2) | `TestPullConversation_forkID` |
| Forking, trashing or writing the local files fails | the underlying error; no local message is lost: the previous local file is in place, in the trash or in `L` at every point (PullConversation.5, putAside.1, putAside.2, putAside.3, PullConversation.3) | `TestPullConversation_forksLocal` (write-order probe), `TestPullConversation_replacesShorterLocal` |

## Golden diff

None.

## Removes

None.

## Local facts

- `clai cloud pull` is unchanged. `PullConversation` shares `Engine.fetch`, `mergeChats`, `Engine.trash`, `setBase`, `Engine.hookPulled` and `forkCopy` with it and adds no second copy of any of them; `forkTheirs` becomes a call of `forkCopy` and the existing tests pin it.
- The difference in policy from Pull is one branch: where Pull keeps the local copy that only adds messages (`keepLocal`) and forks the server's copy when the two diverge, `PullConversation` keeps the cloud copy and forks the local one in both cases (Q5, I8).
- `PullConversation` takes the whole store's change list (P12), one blob at a time through `Engine.fetch` (no bundle path, so no test of fetch limits: a conversation is a few blobs; `TestPullConversation_manyBlobs` is dropped for that reason), and does not advance the cursor, so the next `clai cloud pull` still sees every other change.
- A fork `L` is not in `cloud/state/`, so the next `clai cloud commit` uploads it like any new chat. Its `created` makes its local-only spend count once under I10 (the local queries it keeps predate nothing the cloud holds); phase 6 asserts the group total.
- Same-message copies with other fields differing go through the trash, as in Pull's `replaceLocal`: the local copy's title and query statistics are replaced by the cloud copy's and kept for 30 days in `cloud/trash/`.
- The chat index follows through `Hooks.Pulled` (`chat.ReindexPulled` in production); the engine knows nothing of the chat package.
- Concurrent `PullConversation` calls for one ID race on the same files; each write is atomic and the state file is written whole, so the result is one of the cloud copies. Not tested.
- Phase 4 and the composition root inject `Hooks` exactly as `internal/cli/cli.go` gives them to `clai cloud`.

- Moved out of the interface on 2026-11-23 (maintainer: the clause block was unreadable), unchanged in meaning:
  - A local query is the cloud copy's when one of the cloud's queries has the same `created_at` and model; L's `created` is the `created_at` of the first local query that is not.
  - `Engine` gains an unexported `newID func() (string, error)` for tests; nil means `chatid.New`. `internal/chatid` imports nothing that imports `sync`.
  - Files are written atomically. `Engine.pruneTrash` runs after a pull, as in `Pull`. `st.Hashes` is cleared for every written path, as `take` does.
  - A held image is left alone; only missing ones are fetched and written.

## Human required

None. The live pull is the root's Human check "cloud".

## Implementation notes

| Event | Date | Note |
| ----- | ---- | ---- |
| replan | 2026-11-23 | Re-planned for Q9: the `ConversationReader` and Adopter are replaced by `Engine.PullConversation`, which reuses `Pull`'s merge, trash, fork, base recording and index hook. `MoveToTrash` is dropped (`Engine.trash` is reused); `asset.RefsIn` is kept; `forkTheirs` is generalised to `forkCopy`. Status stays Not Started. |
| start | 6658380 | `make qa QA_PROCS=5`; `go vet ./...`; dupl | qa pass, vet clean, 49 clone groups |
| end | 6658380+wip | same | qa pass (109 packages ok), vet clean, 49 clone groups |
| end | 6658380+wip | same | qa pass (109 packages ok), vet clean, 49 clone groups |
| ported | 615303b+wip | same | Ported from the worktree onto 615303b (branch) after phase 1. `internal/cloud` and `internal/asset` are identical on both bases, so those files carried over unchanged. Reconciled: `internal/chat/persister.go` on the branch has the same asset-ref scan, so `collectAssetRefs` now calls `asset.RefsIn` and the local `assetRefScan`/`assetRefPart` types and the `bytes` and `encoding/json` imports are gone. qa pass (three runs), vet clean, 49 clone groups outside `.claude/worktrees` |
| fix-start | 615303b+wip | R1-01, R1-02 | `make qa; go vet ./...; dupl -t 80 .` run at the end only; baseline was the review's pass |
| fix-end | 615303b+wip | R1-01, R1-02 | qa exit 0, vet 0, dupl 49 groups outside `.claude/worktrees`. R1-01: `TestPullConversation_failedWriteLosesNoLocalMessage` (trash, fork file, fork sidecar and X.json write failures). R1-02: `asset.RefScanError` lets `collectAssetRefs` restore the old text; `TestLiveAssetRefs_errorText` pins it. |

Andon: none.
Surprises:

- The worktree is at 6658380, not 615303b.
- `PullConversation` fetches one blob at a time through `Engine.fetch` (GET), so `srv.CorruptFetch` (bundle route only) cannot trigger a `*ChecksumError` here; the failures test tampers `srv.Blobs[hash]` instead. `*BundleError` cannot occur on this path.
- `cloudConversation` gained an unexported-purpose field `Held []RemoteEntry` (referenced images already here) so their base is recorded (record.1).
- `internal/cloud/cloudtest` had no test file; `server_test.go` added with `testenv.Main`.
- `forkTheirs` goldens recorded from the unmodified function first (`fork_test.go`).

## Review findings

### Review 1, 2026-11-23

Verdict: Ready. Gates: `make qa QA_PROCS=5` twice, exit 0 both, no test differed; `go vet ./...` 0; dupl `-t 80` outside `.claude/worktrees`: 49 groups, unchanged from the effort's start. `Engine.Pull` is untouched (`pull.go` has no diff; `forkTheirs` is a wrapper whose title, id, error and output paths match the old function line by line, pinned by `TestForkTheirs_unchanged`).

- [ ] R1-01 (minor) `internal/cloud/sync/conversation.go:289-331,385-406`: the Error management row "Forking, trashing or writing the local files fails" names `TestPullConversation_forksLocal` and `TestPullConversation_replacesShorterLocal`, but no test makes a trash, fork write or cloud write fail. Coverage: `writeCloud` 71.4%, `forkLocal` 80.0%, `sidecarFiles` 78.6%. The safe order (L written before `X.json`) is proved only by calling `putAside` directly (`TestPullConversation_forkWrittenBeforeCloud`); the failure outcome (old X still in place, in the trash or in L) is unproved. Add rows that fail `trash` (a read-only `cloud/trash`), the fork write and the `X.json` write, and assert the old bytes survive.
- [ ] R1-02 (minor) `internal/chat/persister.go:275-278`: the sweep's error text changed. Before: `scan <path> for asset refs: ...` and `scan <path> message N for asset refs: ...`; now `scan <path>: scan for asset refs: ...` and `scan <path>: scan message N for asset refs: ...`. The Test cases row `TestCollectAssetRefs_unchanged` says the text must not change; no existing test pins it, so evidence and text disagree. Restore the text or record the change.

Verified-good notes:

- Order, `PullConversation.1`: ID check and state read precede any request; `fetchConversation` holds every blob, then `checkLinks`, `readLocal`, `settle`, `putAside`, `writeCloud`, `recordInSync`. Every early return before `putAside` changes nothing (`TestPullConversation_failures` compares the tree before and after).
- No message lost, `PullConversation.3`, each outcome: Adopted has no local X; identical bytes need no copy; Unchanged and Replaced `Rename` X.json into `cloud/trash/` before `writeCloud` and a later failure leaves it there; LocalForked writes the sidecar copy and then `L.json` (atomic) before `writeCloud` replaces `X.json`. `settle` never answers Unchanged or Replaced for a local copy with more messages than the cloud's (`keepLocal` with more is forked, `keepRemote` is only reached with fewer or equal).
- Caveat: `Engine.trash` renames onto a name with one-second resolution, so a second differing local X trashed in the same second overwrites the first trash file. Shared with `Pull`; unreachable by hand.
- Caveat: `forkLocal` copies the bytes read by `readLocal`; a `chat continue` appending to X between that read and `writeCloud` is not in L. A concurrent pull is stated untested in Local facts; a concurrent writer is the same window.
- Caveat: `writeCloud.1` deletes or overwrites local sidecar files in Unchanged and Replaced without a trash copy (`removeStaleNotes`); `putAside.1` moves only `X.json`. Matches the clauses (the messages are in the trash copy), but the reasoning notes of a trashed local copy are not.
- Caveat: when `pruneTrash` or `hookPulled` fails after the files are written, `PullConversation` answers the filled `ConversationPull` together with the error; phase 4 must treat that as "pulled, index stale", not as a failed pull.
- `RefsIn` returns the old scan's refs (sorted, once, error naming the message index); `chat` keeps no second copy. `FetchedHashes` records the bundle route and the single GET only. `PullConversation` does not touch `st.Cursor` or `st.Commit`.

### Review 3 (root), 2026-11-23

- [ ] R3-08 (minor, owner fix) `internal/cloud/sync/conversation.go:114`: the `PullConversation` comment cites "(P11)", which is the `chat list` line; the pull's parameters are P9 and P12. Item: drop or correct the citation.
- note: a failure in `writeCloud` or `recordInSync` after `putAside` wrote fork `L` returns an empty `ConversationPull`, so the error does not name `L` and the index gains it only on a rebuild; the local `X` stays, so no message is lost, but a retried `cc:X` forks the same local copy again.
