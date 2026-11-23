# Phase 4 — Cloud references

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

`clai chat continue cc:<id>` and `clai chat fork cc:<id> <prompt>` take the conversation from the account's cloud store through phase 3's one-conversation pull, then continue or fork it the way a local chat is.

## Interface

Phase 2 owns the argument form, the resolver (`findChatByRef`), the `CloudResolver` seam, `CloudRefError`, `CommandDeps.CloudResolver` and every refusal that needs no network (`cc:` with no ID, no resolver injected, `delete cc:X`). Phase 3 owns the pull and the merge. This phase fills phase 2's seam with phase 3's pull and edits no member of either. It adds `internal/chat/cloud_resolver.go` (new); no merge, trash, fork or download logic lives in `internal/chat`. The composition root (phase 6) builds the `Engine` and the resolver and injects it through `CommandDeps.CloudResolver`.

### CLI behavior this phase adds

```text
clai chat continue [query flags] cc:<chatID> [prompt...]
clai chat fork     [query flags] cc:<chatID> <prompt...>

stdout   as the verb prints for a local chat.
stderr   zero or one pull notice (P10), at the default log level (a record under -json), then the verb's own.
exit 0   as the verb does for a local chat.
exit 1   a conversation the store lacks, a failed download, a failed pull; nothing is written to clai.chat.
```

Clauses:

- `ref.1` `cc:X` resolves by pulling `X` (flow 3) and loading the local `X`; the resolver returns it with the pull's notice. `chat continue` with no prompt previews the pulled chat and binds the directory to it; with a prompt it sends it. `chat fork` pulls, forks `X`, and sends (P7).
- `ref.2` A conversation the store lacks returns `*CloudConversationMissingError`, whose text is exactly `clai.chat has no conversation X; 'clai chat continue X' continues the local one` (P8), with `*cloudsync.ConversationNotFoundError` as its cause. Nothing is written. Phase 2 wraps it as a `*CloudRefError`, so the verb exits 1 without the list UI.
- `ref.3` Not logged in, a revoked key, a network failure, a corrupt or incomplete download, a failed pull step: phase 3's error (`*cloud.NotLoggedInError`, `*cloud.APIError`, `*cloud.TransportError`, `*ChecksumError`, `*MissingBlobError`, `*MalformedConversationError`, `*LocalConversationError`, `*LinkedConversationError`) is in the chain of the error the resolver returns, unchanged.
- `ref.4` Notices (P10), reported by the handler from `CloudResolution.Notice`, never by the resolver or the engine: Adopted `cc:X: taken from clai.chat`; Replaced `cc:X: replaced a shorter local copy (kept in cloud/trash/<path>)`, `<path>` the pull's `TrashPath`; LocalForked `cc:X: taken from clai.chat; the local copy is now <L>`; Unchanged prints nothing.
- `ref.5` The pull records `X` in `cloud/state/` as phase 3 does (I7); the resolver and the verbs touch the previous-query record and the directory binding only as they do for a local chat.
- `ref.6` (I2) `chat fork cc:X` writes `X` only through the pull, before the fork exists; the fork step never writes `X`, so after the fork `X` is the pulled copy byte for byte.

### Resolver (fills phase 2's `CloudResolver`)

```go
// NewCloudResolver is the CloudResolver that pulls.
//  ResolveCloud.1 It calls p.PullConversation(ctx, id), loads the conversation from confDir
//     (loadConversation) and returns CloudResolution{Chat, Notice: cloudNotice(id, pull)}.
//  ResolveCloud.2 A *cloudsync.ConversationNotFoundError is returned as *CloudConversationMissingError{ID};
//     every other error is returned unchanged (ref.3).
//  ResolveCloud.3 The loaded chat is the pulled copy: its ID is id, and it is never the local fork L.
func NewCloudResolver(p cloudsync.ConversationPuller, confDir string) CloudResolver

// cloudNotice is the P10 text for a pull, "" for ConversationUnchanged.
func cloudNotice(id string, p cloudsync.ConversationPull) string

// CloudConversationMissingError is a conversation the cloud store lacks; Error() is the P8 sentence.
type CloudConversationMissingError struct {
	ID    string
	Cause error
}

func (e *CloudConversationMissingError) Error() string
func (e *CloudConversationMissingError) Unwrap() error
```

Consumes:

- `ConversationPuller`, `ConversationPull`, `ConversationOutcome` and its four constants, `ConversationNotFoundError`, `Engine.PullConversation` (phase 3).
- `CloudResolver`, `CloudResolution`, `CloudRefError`, `findChatByRef`, `CommandDeps.CloudResolver` (phase 2): implemented and used, never edited.
- `loadConversation` (repository code); the import alias is `cloudsync`, since `handler.go` already imports the standard `sync`.
- `previous-query-pointer:LoadPreviousQuery (import)`: read only by the tests, to assert the pull leaves the record as it was.
- `cloudtest.Server` as the fake clai.chat; `cloud.NewClient`, `cloud.NotLoggedInError` (repository code).

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | `continue cc:X` with no local `X` writes the cloud copy and reports `cc:X: taken from clai.chat` | ref.1, ref.4, ResolveCloud.1 | `Test_continueCloud_adopted` |
| 2  | The four outcomes give the four P10 notices, the unchanged one none | ref.4 | `TestCloudNotice` |
| 3  | A conversation the store lacks exits 1 with the P8 text; nothing written, no list UI | ref.2, ResolveCloud.2, P8 | `TestCloudResolver_missing`, `Test_continueCloud_notInCloud` |
| 4  | Download failures (401, offline, corrupt blob, not logged in) exit 1 with phase 3's error in the chain, local files byte-identical | ref.3 | `Test_continueCloud_downloadFailures` |
| 5  | `continue cc:X and then this` sends the prompt to the pulled `X`, not to a fork, with no `-cm` | ref.1, ResolveCloud.3 | `Test_continueCloud_withPrompt` |
| 6  | `fork cc:X "task"` pulls `X`, then forks it; `X` is not written by the fork | P7, ref.6, I2 | `Test_forkCloud_pullsThenForks` |
| 7  | With a local `X` ahead of the cloud, `fork cc:X` leaves three chats: local copy `L`, pulled `X`, fork `F` | P7, ResolveCloud.3 | `Test_forkCloud_localForkAndFork` |
| 8  | The pull records `X` in `cloud/state/` and leaves the previous-query record unchanged | ref.5 | `Test_continueCloud_stateAndRecord` |

## Test cases

Merge, trash, fork stamping, sidecar, images, base recording and index hook are phase 3's rows; this phase proves only the resolver and the verbs through it.

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestCloudResolver_pulls` | unit (fake `ConversationPuller`) | ResolveCloud.1, ResolveCloud.3 | a pull of each outcome over a confDir holding `X` (and `L` for the fork) | the chat is `X` loaded from confDir; `Notice` equals `cloudNotice(id, pull)` | return `L`, or report the notice itself |
| `TestCloudResolver_missing` | unit | ResolveCloud.2, ref.2, P8 | a puller returning `*cloudsync.ConversationNotFoundError{X}`; a transport error | `*CloudConversationMissingError` with the exact P8 sentence and `Unwrap` reaching the cause; the transport error unchanged | lose `Unwrap` |
| `TestCloudNotice` | unit (table) | ref.4, P10 | each outcome, with a `TrashPath` and a `ForkID` | the four exact P10 strings, `""` for unchanged | include the full confDir path |
| `Test_continueCloud_adopted` | command (real `Engine` and resolver over `cloudtest`, a temp confDir, `CommandDeps.CloudResolver` built from them) | ref.1 | `cloudtest` holds `X`; `chat continue cc:X` | exit 0; local `X` is the cloud bytes; the directory is bound to `X`; one stderr notice | print a preview twice |
| `Test_continueCloud_withPrompt` | command | ref.1, ResolveCloud.3 | as above plus a prompt and a recording sender | the sender gets `ChatID X`, the prompt, and no `-cm` | send to a fork |
| `Test_continueCloud_notInCloud` | command | ref.2, P8 | a store without `X` | exit 1 with the exact sentence; no list UI; confDir unchanged | fall back to `handleListCmd` |
| `Test_continueCloud_downloadFailures` | command (table) | ref.3 | revoked token; closed server; `CorruptFetch`; not logged in (factory returns `*NotLoggedInError`) | exit 1 with the matching type in the chain; local `X` byte-identical | change local files |
| `Test_forkCloud_pullsThenForks` | command | P7, ref.6, I2 | `fork cc:X do it` with a fake sender | `X` equals the cloud bytes after the run; fork `F` has `source_id X`; the sender got `F` and `-cm=<last query model>` | skip the pull |
| `Test_forkCloud_localForkAndFork` | command | P7, ResolveCloud.3 | local `X` ahead of the cloud; `fork cc:X do it` | `L` (local copy), `X` (cloud) and `F` (fork of cloud `X`) exist with three distinct IDs | confuse `L` and `F` |
| `Test_continueCloud_stateAndRecord` | command | ref.5 | a record naming chat `R`, a `cloud/state/conversations.json` with a cursor, a commit counter on the server | the state's cursor and commit are unchanged, `X`'s paths are recorded; the record byte-identical; zero commits | advance the cursor |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| The conversation is not in the cloud store | exit 1, `clai.chat has no conversation X; 'clai chat continue X' continues the local one` (P8); nothing written | `Test_continueCloud_notInCloud` |
| Not logged in | exit 1, `not logged in to clai.chat: run 'clai cloud login'` in the chain (`*cloud.NotLoggedInError`) | `Test_continueCloud_downloadFailures` |
| Token revoked | exit 1, `*cloud.APIError` with `Unauthorized()` in the chain | `Test_continueCloud_downloadFailures` |
| Network failure | exit 1, `*cloud.TransportError` in the chain | `Test_continueCloud_downloadFailures` |
| A downloaded blob is corrupt or missing | exit 1, `*ChecksumError` or `*MissingBlobError` in the chain; local files unchanged | `Test_continueCloud_downloadFailures` |
| Any other pull error | returned unchanged inside `*CloudRefError`; exit 1 | `TestCloudResolver_pulls` |

The refusals that need no network (`cc:` with no ID, no resolver injected, `chat delete cc:X`) are phase 2's `Test_chatRef_refusals`; the merge failures are phase 3's `TestPullConversation_failures`.

## Golden diff

None.

## Removes

None.

## Local facts

- `internal/chat` imports `internal/cloud/sync` only for the puller's interface and types; there is no cycle (`sync` imports `internal/cloud`, `utils`, `diag`; neither imports `chat`).
- The reader runs before any write inside the engine, so an offline machine keeps its local chat: `chat continue X` (no `cc:`) never touches the network.
- Phase 2 declared the seam, `chatUsage`, `continue`'s help and `fork`'s synopsis already name `cc:<chatID>` there; this phase changes none of them.
- Phase 2 has no `ForkAt`: the fork `L` that a pull keeps is stamped by phase 3 in raw JSON (`forkCopy`), because `internal/cloud/sync` cannot use the chat package's `Forker`.

## Human required

None. The live pull is the root's Human check "cloud".

## Implementation notes

| Event | Date | Note |
| ----- | ---- | ---- |
| replan | 2026-11-23 | Re-planned for Q9: the Adopter, its outcomes, trash and fork logic leave this phase for phase 3's `PullConversation`; this phase keeps the resolver, the P8 error, the P10 notices and the `cc:` rows. |

| Run   | Commit      | Command                                                                                              | Result                                                    |
| ----- | ----------- | ---------------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| start | 615303b+wip | `make qa` (provider keys unset); `go vet ./...`; dupl -t 80 (without `.claude`)                     | all packages ok, vet clean, 49 clone groups               |
| end   | 615303b+wip | same                                                                                                 | all 104 package lines ok, vet clean, 49 clone groups (226 with `.claude`) |
| end   | 615303b+wip | same                                                                                                 | all 104 package lines ok, vet clean, 49 clone groups      |
| fix-start | 615303b+wip | same | all packages ok, vet clean, 49 clone groups |
| fix-end | 615303b+wip | same | R1-40 closed: `CloudPullError` carries the P10 notice, cause kept in the chain; all packages ok, vet clean, 49 clone groups |

Andon: none.

Pulled with an error: `PullConversation` returns a filled `ConversationPull` with the error when `pruneTrash` or `hookPulled` fails after the files were written. No clause covers it, so ResolveCloud.2 applies: the error is returned unchanged with no chat and no notice (`TestCloudResolver_pulledWithError`). Nothing is lost: the files and the `cloud/state/` record are written, and `clai chat continue X` reaches the pulled copy. The verb exits 1 with the error reported.

Surprises:

- `cloudtest.Server.CorruptFetch` only corrupts bundle fetches; a single-blob pull goes through `GET /blobs/<sha>`, so `Test_continueCloud_downloadFailures` flips a stored blob to get `*ChecksumError`.
- `ConversationPull.TrashPath` is absolute; `cloudNotice` cuts it at the last `cloud/trash/`.

## Review findings

### Review 1, 2026-11-23

- R1-40 · minor · `internal/chat/cloud_resolver.go:36-42` (`internal/cloud/sync/conversation.go:156`) · ref.4, P10, I8 · When `PullConversation` writes and then returns an error (`pruneTrash` or `hookPulled` fails), the resolver returns an empty resolution and drops the `ConversationPull`. With a local `X` ahead of the cloud, `L` (the kept local copy) is written and `X` replaced, the verb exits 1, and neither the error nor stderr names `L` or the trash file. No message is lost (I8 holds on disk), but the copy that holds the local messages is unannounced and the error text gives no way to find it. Test `TestCloudResolver_pulledWithError` pins the empty resolution with an Adopted outcome only. Fix: when `pull.Outcome` is Replaced or LocalForked and `err != nil`, wrap the error so its text carries the P10 notice (keep it in the chain, ref.3), and add a LocalForked row to the test.
- R1-41 · note · Executor's choice judged: returning the error with an empty resolution and exit 1 is right for ref.3 (a failed pull step is exit 1, error in chain) and for I2/I7 (no continue or fork on a half-pulled state); only the lost notice is R1-40.
- R1-42 · note · `TrashPath` is absolute (`Engine.trash` returns `filepath.Join(trashDir(ConfDir), rel+"."+stamp)`), which phase 3's comment ("where the old local file went, under cloud/trash/") permits; `cloudNotice` cuts at the last `cloud/trash/` so the notice shows `conversations/X.json.<stamp>` and no confDir path (`TestCloudNotice`, `trashRelative` 100%). The Replaced and Unchanged notices have only unit rows; command rows cover Adopted and LocalForked only.
- Verified good: P8 text exact (`TestCloudResolver_missing`, `Test_continueCloud_notInCloud`, no list UI, confDir unchanged); P10 four strings exact; `cc:` works through `findChatByRef` for continue and fork (`Test_forkCloud_*`); resolver coverage 100%, `internal/chat` 88.1%; no logging in the resolver; `git` shows no edit to phase 2/3 members.

### Review 3 (root), 2026-11-23

- [ ] R3-03 (minor, owner fix) `internal/chat/cmd.go:103-104,116`: `chat continue -h` and `chat fork -h` say only "cc:<chatID> names the copy of a chat on clai.chat". Nothing tells the user that the cloud copy takes the ID, a local copy holding messages it lacks is kept under a new ID, a shorter one goes to `cloud/trash/`, or that `clai cloud pull` keeps the local copy's ID instead. Item: one sentence in the `continue` help, referenced from `fork`.
