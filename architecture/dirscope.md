# Dirscope: directory bindings, conversation history & search

This document defines how clai binds a **conversation to a directory**, records a per-directory
**conversation history**, stamps each chat with its **origin directory**, and exposes **directory-anchored
conversation search** to the agent. It is the authoritative note for the dirscope storage that `chat.md`,
`dre.md`, and `replay.md` refer to.

The feature has three layers with deliberately different postures:

- **Binding + history recording + origin stamping** is *always-on and unconditional*. Chats are always
  stored (core clai behavior, unchanged); recording enriches the binding write that already happens on
  every non-reply query, and origin stamping writes the canonical CWD onto each chat the first time it is
  persisted. None of this has an enablement switch.
- **Conversation lookback** — the passive recent-conversations descriptor *plus* the
  `search_conversations` / `inspect_conversation` / `read_message` tools — is the gated part. It is opt-in via the
  `-lb`/`-lookback` flag (and the `use_lookback` profile field). When enabled it injects a
  directory-scoped descriptor block and registers the search/read tools, "effectively making the
  directory's past conversations the agent's memory."
- **The `[d]ir` table filter** is a UI affordance built on the recorded history: an interactive toggle
  button in `clai chat list` that filters the table to the conversations bound to the current directory.
  It needs no flag and no new entrypoint.

Recording and origin stamping are independent of the lookback by construction: the binding, history, and
`origin_dir` are written regardless of `-lb`; the flag only decides whether they are surfaced to the model.

## Scope

The implementation supports:

- the existing directory binding (`chat_id` head), keyed by the canonical-path **`sha256` hash**
- promotion of the binding record to `version: 2` with an `abs_path` and a `history` list
- always-on recording of a deduplicated, capped, timestamped per-directory conversation history
- always-on stamping of each `Chat` with its **`origin_dir`** (canonical CWD at first persist), mirrored into
  the chat index so search can filter by directory cheaply
- opt-in lookback (`-lb`/`-lookback`): a recent-conversations descriptor block carrying statistics, plus the
  `search_conversations`, `inspect_conversation`, and `read_message` tools
- directory-anchored, full-content **keyword search** (AND semantics + quoted phrases) over conversations,
  paginated and ranked, defaulting to the current directory but able to target any path
- conversation introspection without context poisoning: a paginated, filtered per-message metadata listing
  (`inspect_conversation`) and single-message reads by index (`read_message`)
- the `[d]ir` toggle filter in `clai chat list` over recorded history
- typed timestamps (`time.Time`) for all date fields

It does **not** support:

- promoting the binding to a folder — it stays a single JSON file under `conversations/dirs/`, keyed by the
  `sha256` hash of the canonical directory
- any switch that stops chat storage, binding/history recording, or origin stamping
- **retroactive** origin/history reconstruction — both the history list and `origin_dir` accumulate going
  forward only; conversations saved before this feature carry no `origin_dir` and are not attributed to a
  directory by search
- a single undifferentiated global pool — search is always **anchored to a directory** (the CWD by default,
  or an explicit path); there is no "search all 7k conversations at once" mode
- **semantic / embedding** search — lookup is exact keyword/phrase matching with a transparent score, never
  fuzzy similarity

## Binding storage

A directory binding lives under the clai **config** directory (durable, never the cache dir):

```text
<clai-config>/conversations/dirs/<hash>.json
```

`<hash>` is the **hex-encoded `sha256` of the canonical working directory**. Canonicalization is
`filepath.Abs` → `filepath.Clean` → best-effort `filepath.EvalSymlinks`
(`internal/chat/dirscope.go:canonicalDir`), and `dirHash` is `sha256` over that canonical path. A
cryptographic hash is collision-resistant, so distinct directories practically never share a key and a moved
directory simply hashes to a fresh binding; the hash therefore *is* the directory-identity guard.

Writes use the atomic temp-write + rename idiom in `SaveDirScope` (`os.CreateTemp` in the binding's
directory → encode → `os.Rename`).

### `DirScope` record (version 2)

```go
type DirScope struct {
    Version int          `json:"version"`             // 1 → 2
    DirHash string       `json:"dir_hash,omitempty"`  // sha256 filename key, written for self-description
    AbsPath string       `json:"abs_path,omitempty"`  // canonical dir, captured at write; informational only
    ChatID  string       `json:"chat_id"`             // current binding (head), unchanged
    History []ScopedChat `json:"history,omitempty"`   // newest-first, deduped, capped
    Updated time.Time    `json:"updated"`             // typed
}

type ScopedChat struct {
    ChatID      string    `json:"chat_id"`
    FirstScoped time.Time `json:"first_scoped"` // when THIS dir first bound the chat
    LastScoped  time.Time `json:"last_scoped"`  // when THIS dir last bound the chat
}
```

All timestamps are `time.Time`, stamped with `time.Now().UTC()` and left to JSON marshalling for the RFC3339
representation. `abs_path` is **informational, not an integrity field** — the `sha256` key already encodes
directory identity, so the stored path is not re-checked on read. The one accepted caveat is that the path is
stored in cleartext.

### Backward compatibility (version 1 → 2, in place)

A pre-existing `version: 1` binding at `dirs/<hash>.json` (no `abs_path`, no `history`) unmarshals cleanly —
missing fields are zero values. On the next non-reply write it is re-persisted as `version: 2` at the **same**
path, gaining `abs_path` and a `history` seeded with the current head. No rename, no second key. Because
`time.Time` marshals to the RFC3339 string already stored under `updated`, the type change needs no data
migration.

## Origin-directory stamping (always-on)

Where the *binding* answers "what chat is bound to this directory," `origin_dir` answers the inverse and
unbounded question search needs: "which conversations were started in this directory." The dirscope `history`
cannot serve search — it is capped at `dirScopeHistoryCap` and only tracks recently-bound heads — so each
conversation records its own origin.

- `Chat` gains `OriginDir string \`json:"origin_dir,omitempty"\``. It is set to the **canonical CWD**
  (`canonicalDir`, the same helper the binding uses) the **first time the chat is persisted**, and never
  rewritten afterward, so a chat's origin is stable even if later replied to from elsewhere.
- The value is mirrored into the chat index row (`chatIndexRow.OriginDir`) so directory filtering is a cheap
  in-index comparison and does not require opening every conversation file.
- Stamping is **forward-only**: conversations persisted before this feature have an empty `origin_dir` and are
  simply invisible to directory-anchored search. This mirrors the existing "history accumulates going forward"
  stance and needs no backfill.

Directory matching is **subtree-inclusive by default**: a conversation matches a queried `directory` when its
canonical `origin_dir` equals the canonical queried path **or is nested beneath it**. Searching a project root
therefore surfaces conversations started in its subdirectories — the natural "search this codebase" behavior,
and searching `/` would list everything. The `search_conversations` `subtree` parameter (default `true`)
restricts to an **exact** `origin_dir == directory` match when descendant-directory conversations are noise for
a focused task. Nesting is a canonical-path prefix test on a path boundary (so `/a/b` matches `/a/b/c` but not
`/a/bc`).

## History recording (always-on)

Every non-reply query finalizes by updating the binding (`internal/text/finalizer.go` →
`chat.UpdateDirScopeFromCWD` → `SaveDirScope`), extended to **read-modify-write** the record:

1. Load the existing binding for the directory (if any).
2. Set `ChatID` to the new head and refresh `AbsPath` + `Updated`.
3. Upsert the chat into `History`: on a `chat_id` hit, update `LastScoped` and move it to the front,
   preserving `FirstScoped`; otherwise prepend with `FirstScoped == LastScoped == now`.
4. Cap `History` to `dirScopeHistoryCap`, newest-first.
5. Persist with the atomic temp + rename idiom.

**Plain `-re` replies do not record; `-dre` replies do.** Recording is an always-on enrichment of *non-reply*
queries **and of directory replies**. A plain `-re` reply continues the global previous query and forks a
*fresh promoted id*, so recording it would pollute the history with near-duplicates — it is excluded. A
directory reply (`-dre`), by contrast, continues the conversation **bound to the current directory in place**
(same id, no fork — `applyDirReplyChatID` pins the bound id before the run), so upserting it merely bumps that
existing history entry's `LastScoped` and moves it to the front. That keeps the directory's history current and
the binding chainable across successive `-dre` turns (and records the conversation into the history of a *new*
directory it is continued from). The finalizer therefore gates the binding write on
`!replyMode || dirReplyMode` (`Querier.replyMode` / `Querier.dirReplyMode`, set from
`Configurations.ReplyMode` / `Configurations.DirReplyMode`).

**Concurrency caveat:** the read-modify-write is not serialized across processes; two concurrent runs in the
same directory can lose-update the list. The atomic rename guarantees a non-torn file, not a merged history.
Accepted for a shell-driven CLI.

## Conversation lookback (opt-in via `-lb`/`-lookback`)

Surfaced only when the lookback is enabled. Precedence: CLI `-lb`/`-lookback` (boolean flag) overrides the `use_lookback` profile field, which overrides the default (`false`). Resolution lives in
`internal/setup.go:setupLookback`.

### Descriptor block (passive, dir-scoped memory)

When the lookback is enabled and the **CWD** binding has history, clai injects a compact block into the system
prompt, deriving each one-line summary and message count from the chat index — never inlining a transcript.
The header carries statistics: total recorded conversations, how many are shown, and aggregate message count.

```text
This directory has 7 recorded conversation(s) (showing the 5 most recent, 42 message(s) total).
Call `search_conversations` to find more (by keyword, here or in another path), `inspect_conversation`
to list a conversation's messages, and `read_message` to read one.

<recent_conversations>
  <conversation id="ab12" last_scoped="2d" messages="8">fixing the auth signing bug...</conversation>
  <conversation id="9fd3" last_scoped="5d" messages="14">k8s deploy debugging...</conversation>
</recent_conversations>
```

The block shows the most recent `LookbackInjectCount` (default 5) entries from the CWD binding's `history`.
The descriptor remains **strictly dir-scoped to the CWD** — it is the passive "what was I just doing here"
memory. Breadth beyond it is reached through `search_conversations`. When the directory has no history, no
block is injected.

### Conversation search subsystem

Search is intentionally **brute-force, not an inverted index.** Measurement on a real corpus (7,675
conversations, 286 MB) showed a full-content keyword scan completing in **~290 ms warm**. A persistent
full-text index would add a large, must-stay-in-sync subsystem (postings, backfill, corruption handling) to
optimize a sub-second operation — premature at this scale. Brute force sits behind a `conversationSearcher`
interface so an index-backed implementation can drop in later **without caller changes**.

**Upgrade trigger (documented, not built):** brute force is linear in corpus bytes — ~0.3 s at 286 MB, ~1 s
near 1 GB, degrading past ~2–3 GB (roughly 50k+ conversations). Only when a real corpus crosses that
threshold does an inverted-index `conversationSearcher` become justified.

Pipeline (`internal/chat`):

```text
search_conversations(query, directory?, page?, page_size?)

 0. Metadata prefilter — read chat_index.cache (already maintained). Restrict
    candidates to rows whose origin_dir is the queried directory or nested under
    it (default: canonical CWD). Cheap, no per-file reads.

 1. Content prefilter — scan the metadata-filtered candidates (the origin_dir
    prefilter keeps this set small), lowercased bytes.Contains for EACH query
    token (AND) on RAW JSON bytes. No JSON parse. Quoted "phrases" matched as
    contiguous substrings.

 2. Rank + snippet — parse ONLY the survivors. Score = summed token-hit count
    with light weighting (first user message + recency). Extract a keyword-
    centred snippet from the matching message.

 3. Paginate — offset = page * page_size. Return total_matches + the page slice.
```

Disk reads in stage 2 are bounded to the matched set; the response carries only `page_size` rows. Only the
raw-byte prefilter scales with total bytes.

#### Anti-mislead measures

A real corpus is full of noise (scraped HTML, dumped source, error pages saved as "conversations"). Honesty of
relevance matters more than recall:

- **Exact keyword/phrase matching only** — no fuzzy/semantic similarity. The score is a transparent hit count.
- **Return a real snippet** with surrounding context, so the agent judges fit itself rather than trusting a rank.
- **Skip the leading system message** when scoring and snippeting, so an injected skills/lookback/shell-context
  block can never produce a phantom match.
- **Expose `msg_count` and byte size**, so a 1-message 80 KB scrape is visibly distinguishable from a real
  multi-turn session.
- **Return `total_matches` + page info**, so the agent knows the breadth and can refine rather than over-trust
  page one.

### `search_conversations` tool

```text
search_conversations(query, directory?, subtree?, page?, page_size?)
  query       AND keywords, plus "quoted phrases" matched as contiguous substrings (required)
  directory   canonical path to anchor the search; defaults to the session CWD captured at setup;
              the agent may pass another path to investigate a different codebase
  subtree     match origin_dir at directory AND nested beneath it (default true); set false to
              restrict to an exact directory match and skip descendant conversations
  page        0-based page index (default 0)
  page_size   rows per page (default e.g. 10, capped)

  -> "N match(es) in <dir> (page P, showing M):" then one line per row:
       id=<chat_id> created=<age> model=<model> msgs=<n> score=<s>: <snippet>
```

It replaces the previous `list_conversations` enumeration: a directory's full history is now reached by
searching it (an empty-query convention may list all rows for the directory, newest-first). Registered into the
model-visible tool list whenever the lookback is enabled — **regardless of whether the CWD has local history**,
so a fresh directory can still search other paths — and **including when `-t/-tools` narrows the external tool
set** (mirrors `load_skill`).

### `inspect_conversation` tool (per-message outline)

A conversation may be very long (86 KB is common); dumping it wholesale poisons the context. `inspect_conversation`
gives the agent a cheap, paginated, filtered **per-message metadata listing** — the message-level analog of how
the `clai chat list` table paginates and substring-filters conversations — so the agent sees structure and reads
only the messages it chooses.

```text
inspect_conversation(chat_id, page?, page_size?, role?, match?)
  chat_id     the conversation to inspect (as surfaced by search_conversations / the descriptor) (required)
  page        0-based page index (default 0)
  page_size   messages per page (default e.g. 20, capped)
  role        restrict to user | assistant | tool | system
  match       substring; list only messages whose content contains it

  -> "Conversation <chat_id>: T message(s) (page P, showing M[, role=…][, match=…]):" then one row per
     message: index=<i> role=<role> length=<chars>: <first N chars preview>
```

Message **`index` is the true position in the stored message array**, so it maps 1:1 to `read_message` — no
offsetting even when filters hide rows. The configured system prompt sits at index 0; it is listed (role
`system`) rather than hidden, keeping indices honest, and its preview makes it obvious to skip. (The persisted
assistant final answer is also stored with role `system` — an existing storage quirk — so multiple `system`
rows can appear; the preview disambiguates.) Pagination and the substring filter reuse the same
`SlicePaginator` + match helpers the table uses, rendered as plain text since a tool is non-interactive.

### `read_message` tool (one message by index)

```text
read_message(chat_id, message_index)
  chat_id        the conversation (required)
  message_index  the index from inspect_conversation (required)

  -> the full content of that single message, role-tagged.
```

A single message may be long, and that is fine: the standard tool-output rune limit (`toolOutputRuneLimit`,
`limitToolOutput` in `tool_executor.go`) truncates only the worst case. When truncated, the result names the
conversation's on-disk path (`<conversations>/<chat_id>.json`) so the agent can fall back to ordinary file
tools (`cat`, `rg`, `rows_between`) on the raw file. An out-of-range `message_index` or unresolvable `chat_id`
returns an error tool result and the run continues.

Resolution is global for both tools: any `chat_id` is loadable (no directory guard — search already scopes
discovery). All three tools are defined as `pub_models.LLMTool`s whose `Call()` is a no-op marker; execution is
dispatched internally by the tool executor (it needs the resolving config dir and the session CWD), like
`load_skill`.

## `[d]ir` table filter

`clai chat list` stays the single listing entrypoint; the directory filter is an interactive **toggle button**
in the table, alongside the `/substring` filter and page/back/quit actions. It is a `utils.TableAction` with a
`Filter func(any) bool` predicate (`ChatHandler.dirFilterAction`): pressing `d` shows only rows whose `chat_id`
is in the current directory's `history`; pressing `d` again — or **enter** while any filter is active — clears
it. The table applies the predicate over its original row set and records reverse indices, so a selection still
resolves to the correct chat (`internal/utils/table.go:togglePredicateFilter`). The button only appears when
the directory has recorded history. The predicate mechanism is generic.

## UI and logging

Recording, origin stamping, and search are silent except for terse tool-activity lines. When the lookback is
enabled and the directory has history, setup emits a single notice
(`lookback: surfaced N recent conversation(s) for this directory`). Tool activity renders through the standard
tool-call pretty-print:

```text
assistant called search_conversations('oauth refresh', directory: '.')
3 match(es) in /home/me/proj (page 0, showing 3)

assistant called inspect_conversation(ab12, match: 'oauth')
conversation ab12: 14 message(s), 2 match

assistant called read_message(ab12, 7)
read message ab12[7]
```

## Acceptance criteria

1. A non-reply query writes a `version: 2` binding at `dirs/<hash>.json` (`<hash>` = `sha256` of the canonical
   CWD) with `abs_path` set and `updated` as a `time.Time`-marshalled RFC3339 value.
2. Each non-reply query upserts its chat into `history` (dedup by `chat_id`, newest-first, capped), updating
   `last_scoped` on a repeat and preserving `first_scoped`; `history[0].chat_id == chat_id` immediately after.
3. A `version: 1` binding resolves directly and upgrades to `version: 2` in place at the **same** path on the
   next write (preserving `chat_id`, seeding `history`); no rename, no second key.
4. Recording is always-on and independent of the lookback flag; **a plain `-re` reply does not record** (it
   forks a fresh promoted id), but a **`-dre` directory reply does record** — it continues the bound
   conversation in place (same id) and upserts/bumps its history entry; chats are always stored; no switch
   disables binding/history recording.
5. Every chat is stamped with `origin_dir` = canonical CWD on first persist, never rewritten; the value is
   mirrored into the chat index. Stamping is forward-only and has no enablement switch.
6. All date fields are typed `time.Time`; no date is stored as a manually formatted string.
7. The `search_conversations` / `inspect_conversation` / `read_message` tools are registered **whenever**
   `-lb`/`-lookback` is enabled — including when `-t/-tools` filters external tools, and **even when the CWD has
   no recorded history** (so the agent can search other paths from a fresh directory). The passive descriptor
   block is the part gated on local history: it is injected only when the lookback is enabled **and** the CWD
   binding has history. Nothing at all is surfaced when the lookback is disabled.
8. The descriptor shows at most `LookbackInjectCount` entries from the CWD binding and a statistics header
   (total, shown, aggregate message count).
9. `search_conversations` matches **all** query tokens (AND) plus quoted phrases, against full conversation
   content, anchored to `directory` (default canonical CWD; subtree-inclusive), ranked, paginated, with
   `total_matches` and a per-row snippet; it never matches against the leading system message.
10. `search_conversations` with an explicit `directory` finds conversations whose `origin_dir` is that path or
    (with `subtree` true, the default) nested beneath it; `subtree=false` restricts to an exact `origin_dir`
    match; a directory with no stamped conversations returns none.
11. `inspect_conversation(chat_id, …)` returns a paginated per-message metadata listing (index, role, length,
    preview) — never message bodies — with `role`/`match` filters and stable storage-true indices.
    `read_message(chat_id, message_index)` returns exactly that one message's content (truncated only by the
    standard tool-output limit, with the on-disk path surfaced on truncation). An unresolvable `chat_id` or
    out-of-range index returns an error tool result and the run continues.
12. The lookback works across vendors with no vendor-specific code (system-prompt text + `ToolBox` registration).
13. `clai chat list` offers a toggleable `[d]ir` filter button (only when the directory has recorded history)
    that restricts the table to the directory's conversations and clears on a second press, selections still
    resolving correctly.
14. The binding key is the hex `sha256` of the canonical directory (`Abs` → `Clean` → best-effort
    `EvalSymlinks`); `abs_path` is informational only.
15. Search is brute-force behind a `conversationSearcher` interface (no persistent inverted index); the upgrade
    threshold is documented.
16. `chat.md`/`dre.md` reference this document, and the architecture index lists it.

## E2E test expectations

E2E tests (`main_dirscope_e2e_test.go`) use the `mock` vendor with scripted tool calls and a temporary
`CLAI_CONFIG_DIR`. The suite asserts:

1. **Recording is unconditional** — two sequential non-reply queries in one temp directory produce a
   `version: 2` binding whose `history` lists both ids (newest-first, deduped), with `abs_path` set and
   `updated` typed; holds without `-lb`.
2. **Plain `-re` does not record; `-dre` does** — a non-reply query seeds one history entry; a subsequent `-re`
   reply (which forks a fresh promoted id) leaves the history unchanged, while a `-dre` reply continues the bound
   conversation in place (no fork) and bumps that same history entry's `last_scoped` (preserving `first_scoped`).
3. **v1 → v2 in-place upgrade** — a `version: 1` fixture reads without error and is rewritten as `version: 2` at
   the same path on the next query.
4. **Origin stamping** — a non-reply query persists a chat whose `origin_dir` equals the canonical CWD, mirrored
   into the chat index; a reply does not change an existing chat's `origin_dir`.
5. **Lookback off by default** — without `-lb`, no `<recent_conversations>` block and no lookback tools, even
   though history and `origin_dir` are recorded.
6. **Lookback on** — with `-lb` and seeded history, the system prompt contains the descriptor block with its
   statistics header, and `search_conversations` / `inspect_conversation` / `read_message` are registered. With
   `-lb` but **no** local history, the tools are still registered (no descriptor block injected), so the agent
   can search other paths from a fresh directory.
7. **search_conversations** — a scripted call with a keyword finds the seeded conversation in the CWD; a search
   from a parent directory still finds it (subtree default) while `subtree=false` from that parent does not; an
   explicit `directory` pointing elsewhere returns no rows; AND semantics exclude a conversation missing one token.
8. **inspect_conversation + read_message** — a scripted `inspect_conversation` lists the seeded conversation's
   messages with storage-true indices (and respects a `role`/`match` filter); a `read_message` for one of those
   indices returns that message's content; an out-of-range index and an unresolvable `chat_id` each return an
   error tool result and the run continues.

## Unit tests

In `internal/chat`: the `history` upsert/dedup/cap and `first/last` semantics, the in-place `v1 → v2` upgrade,
the stable hash after canonicalization, `time.Time` round-tripping (`dirscope_test.go`); `origin_dir` stamping
on first persist and its immutability on reply, plus index mirroring; the descriptor statistics + cap; the
`conversationSearcher` brute-force impl — AND semantics, phrase matching, subtree directory anchoring, ranking,
pagination, and the leading-system-message exclusion; the `inspect_conversation` listing (pagination,
`role`/`match` filters, storage-true indices) and `read_message` (index resolution, out-of-range error)
(`dirscope_lookback_test.go`); the `[d]ir` filter action wiring and its end-to-end behavior through
`listChats` (`handler_list_chat_test.go`); and, in `internal/utils`, the generic predicate-filter toggle +
reverse-index translation (`table_test.go`). `go test ./...` stays green.

## Documentation alignment

This is the authoritative note for directory bindings, conversation history, origin stamping, and search.
`chat.md` and `dre.md` reference it. The `sha256(canonicalDir)` directory-identity encoding is owned here. The
always-on recording/stamping, the opt-in surfacing, the brute-force-with-documented-threshold search posture,
and the terse logging style are intentionally consistent with `skills.md`.
