# Dirscope: directory bindings & conversation history

This document defines how clai binds a **conversation to a directory** and how it records a
per-directory **conversation history** for lookback. It is the authoritative note for the dirscope
storage that `chat.md`, `dre.md`, and `replay.md` refer to.

The feature has three layers with deliberately different postures:

- **Binding + history recording** is *always-on and unconditional*. Chats are always stored (core
  clai behavior, unchanged), and recording only enriches the binding write that already happens on
  every non-reply query — so it costs nothing extra and has no enablement switch. There is no mode in
  which clai stops storing chats or recording the binding.
- **Conversation lookback** — surfacing recent conversations to the agent and exposing the
  `list_conversations` / `read_conversation` tools — is the gated part. It is opt-in via the
  `-lb`/`-lookback` flag (and the `use_lookback` profile field). When enabled it injects a
  directory-scoped context block, "effectively making the directory's past conversations the agent's
  memory."
- **The `[d]ir` table filter** is a UI affordance built on the same recorded history: an interactive
  toggle button in `clai chat list` that filters the table to the conversations bound to the current
  directory. It needs no flag and no new entrypoint — recording is always on.

Recording is independent of the lookback by construction: the binding and history are written
regardless of `-lb`; the flag only decides whether they are surfaced to the model.

## Scope

The implementation supports:

- the existing directory binding (`chat_id` head), keyed by the canonical-path **`sha256` hash**
  (unchanged from before this feature)
- promotion of the binding record to `version: 2` with an `abs_path` and a `history` list
- always-on recording of a deduplicated, capped, timestamped per-directory conversation history
- opt-in lookback (`-lb`/`-lookback`): a recent-conversations descriptor block carrying statistics,
  plus the `list_conversations` and `read_conversation` tools
- the `[d]ir` toggle filter in `clai chat list` over recorded history
- typed timestamps (`time.Time`) for all date fields

It does **not** support:

- promoting the binding to a folder — it stays a single JSON file under `conversations/dirs/`, keyed
  by the `sha256` hash of the canonical directory (the pre-feature key, deliberately retained)
- any switch that stops chat storage or binding/history recording
- retroactive history reconstruction — the list accumulates going forward only
- a cross-directory or global history tier
- semantic search over transcripts — lookup is id-based

## Binding storage

A directory binding lives under the clai **config** directory (durable, never the cache dir):

```text
<clai-config>/conversations/dirs/<hash>.json
```

`<hash>` is the **hex-encoded `sha256` of the canonical working directory** — the key clai has always
used, deliberately retained rather than swapped for a Claude-style directory-name slug. (The slug was
introduced to share one directory-identity encoding with a Claude-derived memory store; that store has
been dropped, so the slug's only reason to exist went with it, and the `sha256` key carries less risk
of weirdness.) Canonicalization is `filepath.Abs` → `filepath.Clean` → best-effort
`filepath.EvalSymlinks` (`internal/chat/dirscope.go:canonicalDir`), and `dirHash` is `sha256` over that
canonical path.

A cryptographic hash is **collision-resistant**: distinct directories practically never share a key,
and a moved directory simply hashes to a different key (a fresh binding). The hash therefore *is* the
directory-identity guard, so no separate integrity check is needed.

Writes use the atomic temp-write + rename idiom already in `SaveDirScope`
(`os.CreateTemp` in the binding's directory → encode → `os.Rename`).

### `DirScope` record (version 2)

```go
type DirScope struct {
    Version int          `json:"version"`             // 1 → 2
    DirHash string       `json:"dir_hash,omitempty"`  // sha256 filename key, written for self-description
    AbsPath string       `json:"abs_path,omitempty"`  // canonical dir, captured at write; informational only
    ChatID  string       `json:"chat_id"`             // current binding (head), unchanged
    History []ScopedChat `json:"history,omitempty"`   // newest-first, deduped, capped
    Updated time.Time    `json:"updated"`             // was string; now typed
}

type ScopedChat struct {
    ChatID      string    `json:"chat_id"`
    FirstScoped time.Time `json:"first_scoped"` // when THIS dir first bound the chat
    LastScoped  time.Time `json:"last_scoped"`  // when THIS dir last bound the chat
}
```

All timestamps are `time.Time`, stamped with `time.Now().UTC()` and left to JSON marshalling for the
RFC3339 representation — fields are never pre-formatted into strings. This matches the existing
`Chat.Created time.Time` convention (`pkg/text/models/chat.go`); the previous string-typed `Updated`
was the outlier and is corrected here.

`abs_path` is **informational, not an integrity field**. Because the `sha256` key already encodes
directory identity, the stored canonical path is not re-checked on read; it exists so a binding is
self-describing (e.g. for a "directories you've used" view) without a side index. The one
consciously-accepted caveat is that the path is stored in **cleartext** (the hash key is a mild
obfuscation, the `abs_path` is not). There is no foreign-directory guard and no slug-collision case to
handle.

### Backward compatibility (version 1 → 2, in place)

A pre-existing `version: 1` binding lives at `dirs/<hash>.json` with no `abs_path` and no `history`.
Because the key is unchanged, resolution is a single read:

1. Compute the `sha256` hash and read `dirs/<hash>.json`. A `version: 1` record unmarshals cleanly —
   missing fields (`abs_path`, `history`) are zero values.
2. On the next non-reply write, the record is re-persisted as `version: 2` at the **same**
   `dirs/<hash>.json` path, gaining `abs_path` and a `history` seeded with the current head. There is
   no file rename and no stale file to remove — the binding lives under one key for its whole life.

Because `time.Time` marshals to the same RFC3339 string already stored under `updated`, the type
change needs no data migration; the v1 → v2 upgrade only adds fields.

## History recording (always-on)

Every non-reply query already finalizes by updating the binding
(`internal/text/finalizer.go` → `chat.UpdateDirScopeFromCWD` → `SaveDirScope`). That path is extended
to **read-modify-write** the record:

1. Load the existing binding for the directory (if any).
2. Set `ChatID` to the new head and refresh `AbsPath` + `Updated`.
3. Upsert the chat into `History`: if an entry with the same `chat_id` exists, update its `LastScoped`
   and move it to the front; otherwise prepend a new entry with `FirstScoped == LastScoped == now`.
4. Cap `History` to `dirScopeHistoryCap` (a dirscope constant), newest-first. The lookback decides
   separately how many of these it *surfaces* (`LookbackInjectCount`).
5. Persist with the atomic temp + rename idiom.

Because only a query *in this directory* writes this file, continuing the same chat in another
directory touches that other directory's record, never this one. So `LastScoped` answers precisely
"when was this chat last active **here**," decoupled from the chat's global timeline.

`Chat` does not record its origin directory, so the history accumulates **going forward** from
feature availability; it is not derived retroactively.

**Concurrency caveat:** the read-modify-write is not serialized across processes. Two concurrent
`clai` runs in the same directory can lose-update the list — the atomic rename guarantees a
non-torn file, not a merged history. This is acceptable for a shell-driven CLI and is noted rather
than locked.

## Conversation lookback (opt-in via `-lb`/`-lookback`)

The recorded history is surfaced to the agent only when the lookback is enabled. Precedence: the CLI
flag `-lb`/`-lookback` (`*` enables, `none` disables) overrides the `use_lookback` profile field,
which overrides the default (`false`). Resolution lives in `internal/setup.go:setupLookback`. The
lookback owns its switch; it does not piggyback on any other subsystem.

### Descriptor block

When the lookback is enabled and the bound directory has history, clai injects a compact block into
the system prompt (the same neutral channel skills use), deriving each one-line summary and message
count from the chat index (`internal/chat/index.go`) — never inlining a transcript. The header
carries **statistics**: the total number of recorded conversations, how many are shown, and the
aggregate message count.

```text
This directory has 7 recorded conversation(s) (showing the 5 most recent, 42 message(s) total).
Call `list_conversations` to enumerate all of them and `read_conversation` to read one in full.

<recent_conversations>
  <conversation id="ab12" last_scoped="2d" messages="8">fixing the auth signing bug...</conversation>
  <conversation id="9fd3" last_scoped="5d" messages="14">k8s deploy debugging...</conversation>
</recent_conversations>
```

The block shows the most recent `LookbackInjectCount` (default 5) entries from `history`. The full
history (up to `dirScopeHistoryCap`) remains reachable via `list_conversations`. When the directory
has no history, no block is injected.

### `list_conversations` and `read_conversation` tools

Both are internal tools, defined as `pub_models.LLMTool`s and dispatched in the tool executor (their
`Call()` is a no-op marker, like `load_skill`). They are registered into the model-visible tool list
whenever the lookback is enabled and the directory has history, **including when `-t/-tools` narrows
the external tool set**.

- `list_conversations()` enumerates the directory's full recorded history (newest-first, up to
  `dirScopeHistoryCap`) as a plain-text listing of `id`, age, message count and preview. It is the
  index beyond the few conversations advertised in the descriptor.
- `read_conversation(id)` resolves `id` only against the directory's `history` and returns the
  conversation as a plain-text, role-tagged transcript. It loads via the same `FromPath` path `dre`
  uses, but builds the string itself rather than calling `AttemptPrettyPrint` — that helper writes a
  single terminal-formatted message to a writer and so cannot yield a full transcript. Assistant
  tool-call markers carry no content (the model-safe message shape) and are omitted; tool outputs are
  retained. An id outside `history` returns an error tool result rather than reading an arbitrary
  conversation; the run continues.

## `[d]ir` table filter

`clai chat list` stays the single listing entrypoint; a positional filter token was deliberately
rejected to avoid setting a precedent for ad-hoc CLI sub-filters. Instead the directory filter is an
interactive **toggle button** in the listing table, alongside the built-in `/substring` filter and
the page/back/quit actions.

It is implemented as a `utils.TableAction` with a `Filter func(any) bool` predicate
(`ChatHandler.dirFilterAction`): pressing `d` shows only rows whose `chat_id` is in the current
directory's `history`; pressing `d` again — or pressing **enter** while any filter is active —
clears the filter, the same way `/` clears a substring search. The table applies the predicate over its
original row set and records reverse indices, so a selection still resolves to the correct chat
(`internal/utils/table.go:togglePredicateFilter`). The button only appears when the directory has
recorded history, so it is never a dead control. The predicate mechanism is generic — any
`SelectFromTable` caller can add a hotkey-toggled filter without a bespoke entrypoint.

## UI and logging

Recording is silent. When the lookback is enabled and the directory has history, setup emits a single
notice (`lookback: surfaced N recent conversation(s) for this directory`). Tool activity renders
through the standard tool-call pretty-print:

```text
assistant called read_conversation(ab12)
read conversation ab12
```

## Acceptance criteria

1. A non-reply query writes a `version: 2` binding at `dirs/<hash>.json` (`<hash>` = `sha256` of the
   canonical CWD) with `abs_path` set to the canonical CWD and `updated` as a `time.Time`-marshalled
   RFC3339 value.
2. Each non-reply query upserts its chat into `history` (dedup by `chat_id`, newest-first, capped),
   updating `last_scoped` on a repeat and preserving `first_scoped`.
3. `chat_id` continues to track the head; `history[0].chat_id` equals `chat_id` immediately after a
   non-reply query.
4. A `version: 1` binding at `dirs/<hash>.json` resolves directly and upgrades to `version: 2` in
   place at the **same** `dirs/<hash>.json` path on the next write (preserving `chat_id`, seeding
   `history`); there is no rename and no second key.
5. Recording is always-on and independent of the lookback flag and reply mode: chats are always
   stored, reply queries do not record, and no switch disables binding/history recording.
6. All date fields are typed `time.Time`; no date is stored as a manually formatted string.
7. Lookback is surfaced (recent-conversations descriptor + `list_conversations` / `read_conversation`
   registration) only when `-lb`/`-lookback` is enabled, including when `-t/-tools` filters external
   tools, and nothing is injected when the lookback is disabled or the directory has no history.
8. The descriptor shows at most `LookbackInjectCount` entries and a statistics header (total count,
   shown count, aggregate message count); `list_conversations` enumerates the full capped history.
9. `read_conversation` returns the full transcript for any id in `history` and refuses ids not in it,
   continuing the run on refusal.
10. `clai chat list` offers a toggleable `[d]ir` filter button (only when the directory has recorded
    history) that restricts the table to conversations bound to the current directory and clears on a
    second press, with selections still resolving to the correct chat.
11. The lookback works across vendors with no vendor-specific code (system-prompt text + `ToolBox`
    registration only).
12. `chat.md` references this document for directory bindings (replacing the obsolete
    `CHAT_DIRSCOPED.md` reference), and the architecture index lists it.
13. The binding key is the hex `sha256` of the canonical directory (`Abs` → `Clean` → best-effort
    `EvalSymlinks`), stable across path forms that canonicalize identically; `abs_path` is stored for
    self-description but is informational only (no foreign-directory guard).

## E2E test expectations

E2E tests (`main_dirscope_e2e_test.go`) mirror `main_skills_e2e_test.go`, using the `mock` vendor with
scripted tool calls and a temporary `CLAI_CONFIG_DIR`. The suite asserts:

1. **Recording is unconditional** — two sequential non-reply queries in one temp directory produce a
   `version: 2` `dirs/<hash>.json` binding whose `history` lists both chat ids (newest-first,
   deduped), with `abs_path` set and `updated` typed as `time.Time`; this holds without the lookback
   flag.
2. **v1 → v2 in-place upgrade** — a `version: 1` fixture at `dirs/<hash>.json` reads without error and
   is rewritten as `version: 2` at the **same** `dirs/<hash>.json` path on the next query (preserving
   `chat_id`, gaining `history`); the key never changes.
3. **Lookback off by default** — without `-lb`, the system prompt has no `<recent_conversations>`
   block and the lookback tools are absent, even though `history` is recorded.
4. **Lookback on** — with `-lb=*` and a seeded `history`, the system prompt contains the
   `<recent_conversations>` block with its statistics header, and `list_conversations` /
   `read_conversation` are registered.
5. **list_conversations** — a scripted call enumerates the directory's history (referencing recorded
   chat ids).
6. **read_conversation** — a scripted call for an id in `history` returns that transcript as a tool
   message; an id outside `history` returns an error tool result and the run continues.

Unit tests cover, in `internal/chat`: the `history` upsert/dedup/cap and `first/last` timestamp
semantics, the in-place `version: 1 → 2` upgrade at the same `sha256` key, the stable hash after
canonicalization, `time.Time` round-tripping (`dirscope_test.go`); the lookback descriptor statistics
+ cap and `ListDirHistoryConversations` (`dirscope_lookback_test.go`); the `[d]ir` filter action
wiring **and its end-to-end behavior through `listChats`** — pressing `d` restricts the listing to the
directory's recorded conversations and a selection over the filtered view resolves to the bound chat
(`handler_list_chat_test.go`); and, in `internal/utils`, the generic predicate-filter toggle + reverse
index translation (`table_test.go`). `go test ./...` stays green.

## Documentation alignment

This is the authoritative note for directory bindings and conversation history. `chat.md` and
`dre.md` reference it for binding storage and lookup. The `sha256(canonicalDir)` directory-identity
encoding is owned here, derived from the shared `canonicalDir` helper. The always-on recording, the
opt-in surfacing, and the terse logging style are intentionally consistent with `skills.md`.
