# Chat list upgrade v4

## Executive summary

v3 got the shared table foundation and initial integration in place, but it still misses two critical product outcomes:

1. `clai chat list` startup is still too slow on large datasets
2. chat search is too permissive and returns too many noisy matches

This v4 plan tightens the design around those failures.

The final requirement is operational, not architectural:

> when the work is done, `go run . c l` must itself demonstrate the final behavior.

That means no parallel legacy flow may remain as the real runtime path for chat listing.

---

## What was learned from v3

### 1. The UI changed, but startup still proves eager work is happening

The current list command now renders through the new paged table flow, but observed startup latency still scales with chat corpus size.

That strongly suggests the expensive part is not table rendering anymore, but data preparation before page 1:

- index rebuilds
- consistency scans
- loading/parsing too much chat data to rehydrate metadata

So the remaining bottleneck is the metadata layer, not the prompt loop.

### 2. Search quality is too lenient

The current scorer accepts very weak matches.

In practice:

- random-looking inputs still match many chats
- ordered subsequence matching is too generous
- there is no minimum-quality threshold
- matching and ranking are too conflated

This makes search feel “smart” in implementation, but “sloppy” in use.

### 3. The old eager implementation still exists as a fallback-style path

Even though `handleListCmd()` now uses the provider path, the codebase still contains the old eager chat list machinery (`list()`, old assumptions around scanning conversations, etc).

That is dangerous because:

- future changes may accidentally call the old path again
- tests may still accidentally validate the wrong thing
- duplicated logic drifts

v4 must explicitly retire the old list implementation as the runtime source of truth.

---

## Goals

v4 must satisfy all of these:

1. `go run . c l` must open quickly even with many conversations
2. page 1 must be rendered from metadata only
3. loading a page must not require loading all full chat JSON bodies
4. chat selection must load exactly one full chat after selection
5. search must reject low-quality/random matches
6. numeric ordering for list/continue/delete must remain unified
7. the old eager `chat list` implementation must no longer be the real runtime path
8. the final implementation must be directly verifiable by manually running `go run . c l`

---

## Root cause analysis

## A. Why startup is still slow

The likely root cause is the current chat index lifecycle:

- `Load()` can rebuild eagerly
- consistency validation can require scanning all chat files
- rebuilding requires unmarshaling full chat JSON to recover metadata

That means the steady-state read path is still not cheap enough.

For `chat list` to be actually fast, the steady-state path must be:

1. read `<clai-cache>/conversations/index.json`
2. decode compact metadata
3. render page 1

Anything that touches every conversation file on normal startup is too expensive.

## B. Why search is too noisy

The current search accepts weak evidence:

- broad substring checks
- token checks with weak acceptance
- ordered subsequence fallback

This is fine for ranking known matches, but not for filtering.

v4 must separate:

- **eligibility**: does this row match at all?
- **ranking**: among eligible rows, which should come first?

Weak subsequence signals may help ranking, but must not by themselves admit garbage matches.

---

## Final design

## 1. Make `index.json` the real steady-state source of truth

The index must be cheap to read and sufficient to render the list.

This index should live in the clai cache dir, not the clai config dir, because it is derived runtime cache data rather than user-authored configuration.

### Required rule

On normal `chat list` startup:

- read `<clai-cache>/conversations/index.json`
- do **not** scan every chat file
- do **not** rebuild unless the index is clearly missing or unusable

### Acceptable startup behavior

Allowed:

- `os.ReadFile(<clai-cache>/conversations/index.json)`
- `json.Unmarshal([]ChatListEntry)`

Not allowed on steady-state startup:

- `os.ReadDir(conversations)` followed by opening every chat
- rebuilding metadata before showing page 1
- full-chat unmarshaling for list startup

This is the central missing piece for actual lazy loading.

---

## 2. Move consistency repair off the hot path

The current self-healing model is good in spirit, but too expensive if done eagerly.

### New rule

There are two categories of reads:

#### Fast path

Used by:

- `chat list`
- numeric index resolution for `continue <index>`
- numeric index resolution for `delete <index>`

Behavior:

- trust `index.json` if it is syntactically valid
- do not scan all chat files
- if selected chat file is missing later, report/recover then

#### Repair path

Used only when needed:

- `index.json` missing
- `index.json` malformed
- explicit repair command/helper
- targeted recovery after detecting a stale entry on actual access

### Practical implication

Self-healing stays, but it becomes demand-driven instead of startup-driven.

That gives:

- fast normal startup
- safe recovery when the index is actually broken

---

## 3. Update index incrementally on every mutating write

To make the fast path trustworthy, all normal mutations must keep the index current.

### Must update index on:

- `Save(...)` for normal chats
- chat deletion
- message edit
- message deletion
- saving profile changes into a chat
- any persisted mutation of a conversation file

### Design rule

Mutations should update the chat file and then update the index entry immediately.

This is simpler and more robust than trying to infer freshness later by rescanning everything.

### Consequence

If normal writes maintain the index well, steady-state reads can remain fast and dumb.

That is exactly what we want.

---

## 4. Replace the old eager chat-list implementation, not just route around it

v4 must make the new provider/index flow the only real implementation for chat list behavior.

### Required outcome

The old eager list code must no longer sit around as an alternative implementation of chat listing.

That means:

- remove or demote old `listChats()` style eager chat-list logic
- avoid any runtime path where `chat list` depends on full transcript loading before page 1
- keep only small helpers that are still genuinely useful elsewhere

### Recommended cleanup

- delete the old list UI helper if no longer used
- rename helpers to reflect metadata-backed behavior
- add comments documenting that chat list is index-backed by design

The goal is to prevent future regression by structure, not just by tests.

---

## 5. Strengthen search with a strict match gate

The fix is not only to tweak scores.
The fix is to add a proper acceptance step before ranking.

### Search should use two phases

#### Phase 1: eligibility

A row is eligible only if one of these is true:

1. case-insensitive substring match on the normalized corpus
2. all non-trivial query tokens match as substrings or token-prefixes
3. exact/prefix match on high-value fields:
   - chat ID
   - profile
   - first user text tokens

### Explicit rule

Ordered subsequence alone must **not** make a row eligible.

It may only be used as a tie-breaker among already-eligible rows, if at all.

That single change will remove most noisy results.

#### Phase 2: ranking

Among eligible rows:

1. exact whole-query substring match
2. all-tokens-present with compact span
3. token-prefix quality
4. earlier match position
5. shorter span
6. newer chats as final tiebreak

### Token hygiene

Ignore trivial tokens for matching, for example:

- length 1 tokens unless the whole query is length 1
- repeated whitespace tokens

This prevents junk queries from matching almost everything.

---

## 6. Narrow and normalize the search corpus

The corpus should remain metadata-based, but it should not be overly noisy.

### Keep

- chat ID
- profile
- first user message text
- normalized date forms

### Consider removing from primary corpus

- cost string
- token count string

These fields are low-signal for most searches and may create irrelevant matches.

Recommended approach:

- keep them out of default eligibility matching
- optionally include them only in late ranking or explicit advanced matching

This will make normal prompt-based searches much cleaner.

---

## 7. Add explicit stale-index recovery on selected chat access

If the index says a chat exists but the file is gone:

- treat that as stale index data
- remove the stale entry
- continue gracefully where possible

### For interactive list

If selected row points to a missing chat:

1. warn the user
2. prune the stale index entry
3. return to the list

### For `continue <index>` / `delete <index>`

If indexed ID resolves to missing chat file:

1. prune stale entry
2. return a contextual error saying the selected index referred to a stale/missing chat

This lets the fast path stay cheap while still recovering from drift.

---

## 8. Add targeted performance-oriented tests

We cannot benchmark perfectly in unit tests, but we can test behavior that implies good performance.

### Add tests proving `chat list` fast path does not read full chats

Recommended strategy:

- create a valid `index.json`
- create one or more intentionally invalid chat JSON files on disk
- call the provider-backed list path
- assert page 1 still renders successfully

If page 1 renders despite invalid full chat files, then the startup path truly used metadata only.

This is the strongest behavioral test for lazy listing.

### Add tests proving selection loads just one full chat

Recommended strategy:

- valid index with multiple rows
- only the selected chat file is valid
- non-selected chat files are malformed
- interactive selection of that row succeeds

That proves only the selected chat needs full loading.

---

## 9. Add search-quality tests

These are required.

### Must-have test cases

1. exact substring query matches expected row
2. two-token query matches rows containing both tokens
3. random-noise query returns zero rows
4. single-character garbage does not match broad corpus
5. token-prefix query works for realistic prompts
6. newer chats win only when actual match quality is tied
7. ordered-subsequence alone does not admit a row

Without these tests, search quality will drift again.

---

## 10. Clarify final runtime contract

When the work is complete, this must be true:

### `go run . c l`

- opens quickly with many chats
- shows the paged/searchable prompt
- uses metadata only to render the initial page

### `go run . c c 0`

- resolves against canonical index order
- does not eagerly list/load all chats

### `go run . c d 0`

- resolves against canonical index order
- updates the index after deletion

This manual verification must be part of the implementation definition of done.

---

## Implementation plan

## Phase 1: lock down the intended behavior with tests

Add failing tests for:

1. `handleListCmd()` renders successfully even if non-selected full chat files are malformed, as long as `index.json` is valid
2. selected-row loading touches only the selected chat
3. `findChatByID(<index>)` uses only index ordering
4. random/noisy search queries return no results
5. ordered subsequence alone does not count as a match

Run:

```bash
go test ./internal/chat ./internal/utils -timeout=30s
```

Confirm failure first.

---

## Phase 2: make index loading cheap on steady-state startup

Refactor `ChatListIndex.Load()` into two distinct modes:

- `LoadFast()` or equivalent internal fast path
- `Repair/Rebuild()` path for broken/missing index

Then switch:

- `chat list`
- numeric index resolution

to the fast path.

Key requirement:

- valid `index.json` must not trigger a corpus-wide rebuild/scan during normal startup

---

## Phase 3: tighten search eligibility and ranking

Implement:

- normalized tokenization
- strict eligibility gate
- ranking only among eligible items
- removal or severe restriction of subsequence-only matches

Then add/adjust tests until noisy queries stop matching.

---

## Phase 4: remove old eager runtime path

Clean up:

- old eager list UI function(s)
- dead code that duplicates metadata list behavior
- comments/tests still assuming eager startup behavior

At the end of this phase, there should be one obvious implementation path for chat list.

---

## Phase 5: final validation

Required automated validation:

```bash
go test ./internal/chat ./internal/utils -timeout=30s
go test ./... -race -cover -timeout=10s
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run mvdan.cc/gofumpt@latest -w .
```

Required manual validation:

1. create a config dir with many chats and a cache dir containing the chat metadata index
2. run:

```bash
go run . c l
```

3. confirm:
   - startup is materially faster than before
   - prompt is the new paged/search prompt
   - random garbage queries return no/very few results
   - selecting a chat still works

This manual command is mandatory because it is the exact user-visible contract.

---

## Non-goals

Still out of scope:

- full transcript full-text search
- advanced indexing backend
- background watcher/daemon for index maintenance
- introducing third-party fuzzy-search libraries

We only need a strict, fast, metadata-backed search/list flow.

---

## Definition of done

This work is done only when all of the following are true:

1. `go run . c l` is visibly using the new list path
2. startup no longer scales like eager full-chat loading on steady-state indexed data
3. random string queries do not match broad swaths of conversations
4. `continue <index>` and `delete <index>` use the same canonical indexed order
5. the old eager chat-list implementation is no longer the runtime implementation
6. automated validation passes
7. manual verification via `go run . c l` passes

That is the holistic end-state this feature needs.