# `-re` forks a new conversation: make `globalScope.json` a pointer

Analysis date: 2026-11-17. Status: implemented 2026-11-23.

## Status board

| #   | Phase                                  | Status  | Summary                                                                   |
| --- | -------------------------------------- | ------- | ------------------------------------------------------------------------- |
| 1   | Root cause                             | Done    | `7d0b613` made the promoted conversation id random; `-re` lost the parent |
| 2   | Design                                 | Done    | `globalScope.json` becomes a `previousQuery` pointer record               |
| 3   | Failing tests first                    | Done    | e2e: `-re` adds no file, twice stays one file; unit: record write/load/migrate                                 |
| 4   | `previousQuery` store                  | Done    | `internal/chat/previous.go` replaces `globalscope.go`                                                          |
| 5   | `promote` + `-re` adoption             | Done    | record written last; `-re` adopts the conversation's id, label, origin and usage; sentinel deleted             |
| 6   | One-shot migration from the mirror     | Done    | idempotent `MigratePreviousQuery`, called from the write-path prep                                             |
| 7   | Readers: `replay`, `chat dir`, `photo` | Done    | all four resolve through the record                                                                            |
| 8   | Docs, goldens, quality gates           | Done    | tests re-pointed or inverted; `architecture/*.md` rewritten; `make qa` green                                   |

## The bug

A plain `-re` writes a **new conversation file** instead of continuing the one
the previous query wrote. Observable: `conversations/` gains a third file whose
messages are the mirror's plus one turn; the parent file stays untouched.

Evidence chain:

- `7d0b613` (2026-03-23, "Upgrade to unique uuid as chat ID") replaced
  `HashIDFromPrompt(firstUserMsg)` with `chatid.New()` for the promoted copy in
  `internal/chat/reply.go`. Before it, the promoted id was content-addressed, so
  a reply recomputed the same id and overwrote the same file: no fork.
- `internal/chat/globalscope.go:73-79` normalizes the mirror's id to the
  `globalScope` sentinel and rewrites the file, so the real chat id is
  destroyed on read.
- `internal/text/conf.go:401` adopts the mirror's id only when
  `iP.ID != "globalScope"` — true never. `InitialChat.ID` stays empty, a fresh
  uuid is minted, and `promote` (`internal/chat/reply.go:52`) writes a new file.

So the mirror carries the conversation's content but not its identity. `-dre`
and `chat continue` are unaffected because each is handed the id explicitly
(`applyDirReplyChatID`, `ReplyChatID`); `-re` is the one reply mode with no
pointer. The later "no more duplicate chats" fixes (`16ef22f`, `5f1a0f3`) only
taught `promote` to update an _already real_ source id, which `-re` never has.

`worklogs/2026-09-09-conversation-summaries/README.md` V1-02 recorded this as
observed ("Plain `-re` forks a new file; the README and phase 3 described an
in-place overwrite") and closed it by documenting the fork (D20, D28) instead of
fixing it. Those decisions are superseded by this worklog; the historical file
is not edited.

## Design: the previous-query record

The mirror stops being a conversation document. It becomes a machine-local
pointer under a directory the conversation namespace does not own:

`<confDir>/conversations/state/previousQuery.json`

```json
{ "version": 1, "chat_id": "7f3a9c…c91", "updated": "2026-11-16T15:58:36.402Z" }
```

`version` makes a future shape detectable; `chat_id` is the pointer; `updated`
is when the record was written (it replaces the `Created: time.Now()` fake that
`chat dir` shows for global scope today). No messages, no queries, no labels.

`internal/chat/previous.go` replaces `internal/chat/globalscope.go`:

```go
var (
	ErrNoPreviousQuery   = errors.New("no previous query")
	ErrPreviousQueryGone = errors.New("the previous query's conversation file is gone")
)

func PreviousQueryPath(confDir string) string
func SavePreviousQuery(ctx context.Context, confDir, chatID string, now time.Time) error
func LoadPreviousQuery(ctx context.Context, confDir string) (pub_models.Chat, error)
func MigratePreviousQuery(ctx context.Context, confDir string) error
```

`promote` writes the conversation file first and the record second, so a crash
between them leaves a record pointing at a real file, never a dangling one.

## Migration

Old installs hold a full mirror whose id is the sentinel, so the parent is not
recoverable from the file — but it is recoverable from the corpus: `promote`
copied the conversation's exact message slice, so the record can be rebuilt by
finding the conversation whose messages deep-equal the mirror's (cheap
prefilter on message count, then content length; newest mtime on a tie).

| Step                                                 | Behaviour                                                 |
| ---------------------------------------------------- | --------------------------------------------------------- |
| Record exists                                        | no-op                                                     |
| No legacy `conversations/globalScope.json`           | no-op                                                     |
| One matching conversation                            | write record, remove the legacy file                      |
| Several                                              | newest mtime wins                                         |
| None (deleted conversation, pre-`7d0b613` id scheme) | warn naming the legacy file, leave it in place, no record |

Re-runnable by construction: record-wins, write-then-delete.

## What the change deletes

| Deleted                                                                                                   | Where                                                                                                                                                    |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `LoadGlobalScope`, id normalization, id rewrite                                                           | `internal/chat/globalscope.go:73-79`                                                                                                                     |
| the `globalScope` sentinel and all six uses                                                               | `text/finalizer.go:183`, `chat/dirscope.go:246`, `chat/handler_list_chat.go:641,740,788`, `chat/dirscope_search.go:116`, `chat/handler_summarize.go:122` |
| `promote`'s `globalScopeChat` copy and its `sourceChat.ID == globalScopeChatID` branch                    | `internal/chat/reply.go:36-52`                                                                                                                           |
| title/summary adoption on `-re` (the conversation already has them)                                       | `internal/text/conf.go` reply branch                                                                                                                     |
| the reply-mode history gate (recording becomes always-on)                                                 | `internal/text/finalizer.go:183`                                                                                                                         |
| `chat.LoadPrevQuery`, `chat.GlobalScopePath`, `chat.SaveAsPreviousQuery`'s mirror write                   | `internal/chat/reply.go`, `internal/chat/paths.go:26`                                                                                                    |
| `conversations/globalScope.json` in the cloud-sync exclusion list (the new path is outside the allowlist) | `internal/cloud/sync/paths.go:70-74`                                                                                                                     |

## Decisions

| ID  | Date       | Decision                                                                                | Rationale                                                                                                                             | Alternative                                |
| --- | ---------- | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| D1  | 2026-11-17 | Record is `previousQuery`, not `globalScope`                                            | it records what a _query_ wrote; `globalScope` named a mirror that no longer exists                                                   | `previousReply`                            |
| D2  | 2026-11-17 | Record lives in `conversations/state/`                                                  | one directory level down removes three name-based special cases (cloud allowlist, index rebuild, asset sweep)                         | keep `conversations/globalScope.json`      |
| D3  | 2026-11-17 | Pointer only: no content in the record                                                  | one file, one truth; the four readers all want the whole conversation anyway                                                          | store the last N messages as a fallback    |
| D4  | 2026-11-17 | One-shot migration, no shipped legacy read path                                         | a branch that runs once should not live forever                                                                                       | permanent `ResolvedLegacy` mode            |
| D5  | 2026-11-17 | Migration runs in the write-path prep only, never in read-only listings                 | `architecture/config.md:122-134` promises `chat list/dir/dirv2/help` write nothing; `chat dirv2` runs on shell-prompt hot paths       | migrate lazily inside `LoadPreviousQuery`  |
| D6  | 2026-11-17 | Migration runs under `readonlyConfig` (raw runs)                                        | that flag governs config files and upgrade announcements, not conversation state; skipping it breaks raw `-r -re` scripts for one run | gate on `readonlyConfig`                   |
| D7  | 2026-11-17 | A record whose file vanished is `ErrPreviousQueryGone`, unless deleted by `chat delete` | deleting is an explicit act; a cloud-pull or manual rm is not                                                                         | always start fresh                         |
| D8  | 2026-11-17 | `chat delete <id>` clears a record naming `<id>`                                        | the next `-re` starts fresh instead of erroring                                                                                       | leave it and report `ErrPreviousQueryGone` |
| D9  | 2026-11-23 | A fresh query whose run failed before any answer is saved and the record moves to it (maintainer) | on `main` the mirror kept a failed prompt, so `-re` retried it; with no save the record named an older chat and `-re` silently continued it | store nothing (the state before D9) |

## Work plan

| Phase                | Proves                                                                                                                                                                                                                                                                                                                              |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 3. Tests first       | e2e: seed → `-re` → exactly one conversation file besides the state dir, same id as the seed, user chain `[seed, reply]`; seed → `-re` → `-re` → still one file, three user messages, `created` unchanged, one `chat list` row. Unit: record write/load, `ErrPreviousQueryGone`, migration idempotency and each migration row above |
| 4. Store             | `previousQuery` round-trips; empty/sentinel `chatID` is an error, not a written file; missing file is `ErrNoPreviousQuery`                                                                                                                                                                                                          |
| 5. `promote` + `-re` | one conversation file per conversation; parent gains the turn; no fork; `-re` right after `-dre` continues the `-dre`'d conversation                                                                                                                                                                                                |
| 6. Migration         | every row of the migration table; legacy file removed only after a match                                                                                                                                                                                                                                                            |
| 7. Readers           | `clai re`, `chat dir` (scope `global`), `photo -re` all resolve through the record                                                                                                                                                                                                                                                  |
| 8. Gates             | `make qa`; `internal/chat/testdata/persister/promote-*.golden` regenerated (they currently embed full mirror bodies)                                                                                                                                                                                                                |

Existing rows that encode the regression must be inverted, not patched:
`e2e/conversation/summary_e2e_test.go:110` (asserts seed + one forked
conversation), plus the `-r -re` image rows
(`e2e/interactive/image_e2e_test.go:194,401,424`), `e2e/remote/model_alias_e2e_test.go:462`
and `e2e/dispatch_e2e_test.go:240`.

## Docs to rewrite

`architecture/chat.md:379` (the mirror/fork paragraph),
`architecture/summaries.md:256` (D28 replies and forks),
`architecture/dirscope.md:143-146,360,403` (plain `-re` forks; `-dre` records),
`architecture/README.md` (chat summary line).

## Non-goals

- Retaining pre-upgrade mirrors that cannot be resolved (deleted conversation,
  pre-`7d0b613` id scheme): one stale `clai re` is lost, nothing is destroyed.
- Moving the directory binding (`conversations/dirs/`) to the same treatment.
  It is already machine-local by being a subdirectory; unifying the two is a
  separate cleanup.

## Notes

### 2026-11-23: phases 3 and 8 finished

- Added `e2etest.SeedPreviousConv` (conversation plus record) and the e2e rows
  `Test_e2e_reply_adds_no_conversation_file` and
  `Test_e2e_reply_twice_stays_in_one_conversation` in `e2e/conversation/summary_e2e_test.go`.
- Real bug found by the inverted `Test_e2e_reply_preserves_title_summary`: `-re` continued the file
  in place but the chat written back had no title, summary, `origin_dir`, group key or usage, so a
  reply blanked them. `SetupInitialChat` now adopts them from the previous conversation
  (`adoptConversationFields` in `internal/text/conf.go`).
- Inverted, not re-pointed: `Test_e2e_reply_preserves_title_summary` (now asserts the same file is
  continued and labelled), `TestSetupInitialChat_replyAdoptsFields` (now
  `TestSetupInitialChat_replyContinuesPreviousConversation`),
  `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount` (now
  `Test_Querier_DoesNotRecordPreviousQuery_WhenStreamSetupFailsDueToRateLimitTokenCount`: a fresh
  two-message query that never got a reply is not promoted, so no record exists; the mirror used to
  save it).
- `Test_e2e_replay_legacy_pending` seeds a record plus a conversation holding the pending legacy
  image part, because `replay` does not migrate (D5). `Test_e2e_chat_summarize_vendor_error_leaves_list_working`
  seeds index version 3.
- Removed the dead `"globalScope"` comparisons in `session_runner.go` and `json_display.go`, and the
  `turn_text_only/globalScope.json` golden (the mirror no longer exists). The `promote-*.golden`
  files needed no regeneration.
- The cloud-sync exclusion of `conversations/globalScope.json` stays: a pre-migration install still
  has that file until its first write-path run.
- `plain -re` now records the dirscope binding like `-dre` (the finalizer gate was removed in phase 5);
  `architecture/dirscope.md` was rewritten to say so.

### 2026-11-23: review findings R1 and R2

- R1: `-re` dropped fields of the conversation it continued (`Source`, `SourceID`, anything outside
  the hand-picked list of `adoptConversationFields`), so a cloned foreign chat lost its source and
  reappeared in `chat list`. `adoptConversationFields` is replaced by `continueConversation`, which
  adopts the previous conversation whole; the run's own messages, queries, id, created and profile
  stay. Tests: `TestSetupInitialChat_replyAdoptsThePreviousConversationWhole`,
  `Test_e2e_reply_keeps_source_and_source_id` (`e2e/conversation/summary_e2e_test.go`).
- R2 (D9): `chat.RunOutcome` (`RunAnswered`, `RunFailedBeforeAnswer`) is passed to `Persister.Promote`
  from the finalizer (`session.Failed`). Tests: `Test_promote_freshChatEndingOnUserPrompt`,
  `Test_Querier_RecordsPromptAsPreviousQuery_WhenStreamSetupFailsDueToRateLimitTokenCount`
  (inverted from `..._DoesNotRecordPreviousQuery_...`), `TestReply_RetriesTheFailedPrompt`
  (`e2e/remote/reply_after_failure_e2e_test.go`).
