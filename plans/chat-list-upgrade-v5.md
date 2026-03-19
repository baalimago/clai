# chat list upgrade v5

## Purpose

This document defines the full scope of a new `chat list` implementation from scratch.

It is written to stand on its own, without assuming knowledge of any previous implementation.

The goal is to prevent a repeat of these kinds of user-visible failures:

- the first page is not sorted correctly
- the paging display is misleading
- only a fraction of chats appear even though many more exist
- tests pass while real usage is broken

This document defines:

- the product contract
- the data model
- the runtime behavior
- failure handling
- the required test strategy
- the acceptance criteria for sign-off

---

## Product contract

`chat list` must provide a correct, fast, and predictable way to browse existing chats.

At minimum, it must guarantee:

1. chats are listed in descending creation order by default
2. the first visible page is correct
3. all existing chats are discoverable in the list
4. page indicators are human-readable
5. list startup does not require loading every full chat body when metadata is sufficient
6. search results are deterministic
7. selecting a chat loads that chat reliably
8. creating, updating, and deleting chats is reflected in the list
9. corrupted cache state cannot silently produce incomplete results

If any of those are not true, the feature is not complete.

---

## Core design principles

### 1. Correctness before speed

A fast list that omits chats or shows them in the wrong order is a failure.

Performance matters, but correctness is the primary contract.

### 2. User-visible behavior must be tested directly

It is not enough to test helper functions or internal sort calls.

Tests must prove what the user actually sees:

- first page contents
- ordering
- page indicator text
- search behavior
- update-after-mutation behavior

### 3. Cache is an optimization, not a source of truth

The source of truth is the real chat corpus on disk.

Any metadata index or cache exists only to make listing fast.

If cache state disagrees with the real corpus, the implementation must repair it or fail loudly. It must never silently present partial truth as complete truth.

### 4. Full lifecycle coverage is mandatory

The list feature is not just a renderer.

It depends on:

- chat creation
- chat updates
- chat deletion
- metadata refresh
- cache rebuild
- search
- pagination

If those flows are not tested together, the feature is underspecified.

---

## Functional requirements

## 1. Default ordering

By default, `chat list` must sort chats by creation timestamp descending:

- newest first
- oldest last

If two chats share the same creation timestamp, a deterministic tie-breaker must be used.

Recommended tie-breaker:

- stable sort by chat ID as a secondary key

The tie-breaker must be documented and tested.

---

## 2. Paging behavior

Paging must use user-facing numbering, not internal zero-based indexing.

Examples:

- one page of results: `page 1/1`
- first page of many: `page 1/300`
- second page: `page 2/300`

The interface must not show ambiguous output such as:

- `0/0`
- `0/299`

Even if internal code uses zero-based indexes, that must never leak into the user-facing prompt.

---

## 3. Complete corpus visibility

If 3000 chats exist, the list must behave as though 3000 chats exist.

This means:

- first page is built from the full known corpus metadata
- page count reflects the full corpus
- search operates across the full corpus
- navigation can eventually reach every chat

A valid-looking but incomplete metadata cache must not cause the system to silently pretend that only a subset exists.

---

## 4. Search behavior

Search must be:

- deterministic
- based on indexed metadata where possible
- safe for large corpora
- tested against realistic inputs

The search contract must define:

- what fields are searchable
- whether matching is exact, token-based, substring-based, or ranked
- how ties are ordered
- how empty-state results are presented

Search must not:

- return broad nonsense matches for random input
- reorder identical queries unpredictably between runs

---

## 5. Metadata-first startup

Startup for `chat list` should rely on chat metadata, not full transcript loading, whenever possible.

That means the steady-state path should be:

1. load metadata/index
2. sort/filter/page metadata
3. render first page
4. load full chat only when the user selects one

This is essential for large corpora.

However, metadata-first startup must still preserve correctness. If metadata is stale or invalid, correctness wins over speed.

---

## 6. Mutation consistency

Whenever chats are created, updated, or deleted, the list-visible metadata must remain consistent.

That means the system must keep list metadata in sync after:

- creating a new chat
- appending to an existing chat
- editing a chat
- changing chat-visible metadata
- deleting a chat

If metadata cannot be updated incrementally, the system must have a reliable rebuild path.

There must be no state where:

- the chat exists but is absent from the list
- the chat was deleted but still appears
- the ordering reflects stale timestamps

---

## Data model requirements

The list view should operate on chat metadata, not full chat bodies.

Minimum metadata needed per chat:

- chat ID
- created timestamp
- optional updated timestamp if used
- message count
- first user prompt or other preview text
- optional profile label if relevant
- optional token count if displayed
- optional cost if displayed

If an index is used, it must be derived from the real chat corpus and treated as rebuildable.

---

## Cache/index requirements

If the implementation uses a metadata index, it must follow these rules:

1. the index is not the source of truth
2. the index must be rebuildable from real chats
3. missing index must be recoverable
4. malformed index must be recoverable
5. stale but syntactically valid index must be detectable
6. index updates must happen on normal mutation flows or be safely repaired before listing

### Stale index definition

An index should be treated as stale if it can no longer be trusted to represent the real corpus.

Examples:

- it contains fewer chats than exist on disk
- it contains chats that no longer exist
- ordering-relevant metadata is outdated
- it is older than known chat mutations

The implementation may decide how to detect staleness, but silent partial results are not acceptable.

### Allowed stale-index responses

Exactly one of these must happen:

1. automatically rebuild and continue
2. refuse to continue with a loud, explicit repair error

This is acceptable.

These are not acceptable:

- quietly showing only part of the corpus
- quietly showing stale ordering
- quietly reporting the wrong number of pages

---

## Failure-handling requirements

### 1. Missing metadata cache

If the metadata cache/index does not exist:

- rebuild it, or
- return a clear repair error

### 2. Malformed metadata cache

If the cache/index cannot be decoded:

- rebuild it, or
- return a clear repair error

### 3. Stale metadata cache

If the cache/index is valid JSON but incomplete or outdated:

- rebuild it, or
- return a clear repair error

### 4. Corrupted full chat files

The system should still be able to render list startup from metadata if non-selected full chat files are corrupted, as long as metadata is valid.

Only when the user selects a corrupted chat should full-chat loading matter.

### 5. Empty corpus

If no chats exist:

- render an explicit empty state
- do not show ambiguous page/count output

---

## Test strategy

This upgrade must be signed off with three layers of tests:

1. unit tests
2. integration tests
3. end-to-end tests

All three are required.

---

## Unit test requirements

Unit tests should cover isolated rules and contracts.

### A. sort behavior

Test:

- descending creation order
- deterministic tie-breaking
- zero or missing timestamps if supported
- unsorted input producing correct ordered output

### B. page math

Test:

- empty result set
- exactly one page
- multiple pages
- last page calculations

These tests must distinguish:

- internal page indexes
- displayed page numbers

### C. prompt rendering

Test exact page prompt output for:

- zero items
- one page
- multiple pages
- active search mode

### D. search behavior

Test:

- useful queries match the right rows
- random noise does not match broad swaths
- result ordering is deterministic
- tied matches use a documented order

### E. metadata validation

If a metadata index exists, test:

- missing index
- malformed index
- incomplete index
- outdated index

---

## Integration test requirements

Integration tests should exercise the full list workflow without needing a real terminal session.

They should run against isolated temporary chat corpora and scripted inputs.

### A. first-page correctness on multi-page data

Create enough chats for several pages.

Use intentionally shuffled timestamps.

Assert:

- first page contains the newest chats
- rows appear in correct order
- page indicator is correct

### B. all chats represented

Create a large on-disk corpus.

Assert:

- metadata row count matches corpus count
- page count matches expected value

### C. stale-cache detection

Create a valid-looking but incomplete metadata cache.

Assert either:

- automatic repair occurs and full corpus becomes visible, or
- startup fails loudly

Silent partial results must fail the test.

### D. missing-cache recovery

Create real chats with no cache.

Assert:

- startup succeeds or fails loudly according to contract
- if startup succeeds, the rendered first page is correct

### E. malformed-cache recovery

Create real chats with malformed cache.

Assert the same contract as missing cache.

### F. metadata-only startup

Build valid metadata for many chats.

Corrupt non-selected full chats.

Assert:

- first page still renders
- selecting one valid row still works

### G. lifecycle consistency

Drive the real create, update, and delete flows.

Assert after each mutation:

- list output is updated
- counts are updated
- sorting remains correct

This is critical. Tests must not patch metadata manually unless testing the metadata mechanism itself.

---

## End-to-end test requirements

End-to-end tests must validate behavior at app level with isolated config and cache paths.

### E2E-1: cold start with large corpus

Build a large chat corpus, for example around 3000 chats.

With no cache present:

- start the list flow in a scripted non-interactive harness
- verify first page correctness
- verify page count correctness
- verify cache creation if applicable

### E2E-2: warm start with large corpus

With cache already present:

- verify the same visible result as cold start
- verify correctness does not regress in the optimized path

### E2E-3: stale valid cache on large corpus

Build a large corpus and an incomplete but valid cache.

Assert:

- the app does not silently show only the cached subset
- the app rebuilds or fails loudly

### E2E-4: create then list

Create chats through real app flows.

Assert they appear correctly in the list without test-only repair steps.

### E2E-5: delete then list

Delete chats through real app flows.

Assert they disappear correctly from the list.

### E2E-6: select from list

Use scripted input to select a row.

Assert:

- the chosen chat loads
- list startup itself did not require loading all chats

---

## Non-interactive harness requirements

Because `chat list` is interactive, tests must use a controlled harness.

The harness must:

- provide scripted user input
- capture output deterministically
- always terminate
- not depend on a real TTY

Recommended reusable helper shape:

```go
func runChatListScript(t *testing.T, corpus Fixture, inputs ...string) (stdout string, err error)
```

The exact API can vary, but the harness must make it easy to test:

- page 1 rendering
- page prompts
- search
- selection
- quit behavior

Without this harness, list behavior will remain under-tested.

---

## Why earlier test approaches fail

The previous style of testing was insufficient for a feature like this because the common failure pattern is not in a helper function. It is in the mismatch between:

- real corpus state
- cached metadata state
- mutation flows
- rendered output

Typical inadequate patterns include:

- testing only 2-3 chats
- testing sorting without testing rendered page 1
- testing missing cache but not stale valid cache
- manually repairing metadata in tests after mutations
- not asserting actual prompt text
- not testing app-level behavior on large corpora

This upgrade must avoid all of those traps.

---

## Implementation constraints implied by this spec

This document is implementation-agnostic, but it does impose some hard constraints.

### 1. A rebuild path must exist

If metadata is used, it must be possible to rebuild it from source-of-truth chat files.

### 2. Runtime mutation flows must not drift indefinitely

If metadata is updated incrementally, create/update/delete flows must keep it accurate.

### 3. User-facing paging must be explicit

The displayed page indicator must be understandable to a normal user without knowing internal indexing rules.

### 4. Ordering must be stable

Repeated runs against unchanged data must show the same ordering.

### 5. Large-corpus correctness must be proven

A design that only works on tiny fixtures is not acceptable.

---

## Sign-off checklist

The feature is not complete until all items below are true.

### Product behavior

- [ ] first page is correct
- [ ] default ordering is newest-first
- [ ] page indicator is human-readable
- [ ] all chats are discoverable
- [ ] search is deterministic
- [ ] empty state is explicit

### Cache/index behavior

- [ ] missing cache is handled
- [ ] malformed cache is handled
- [ ] stale valid cache is handled
- [ ] cache cannot silently hide chats
- [ ] metadata can be rebuilt from source of truth

### Lifecycle behavior

- [ ] create is reflected in list
- [ ] update is reflected in list
- [ ] delete is reflected in list
- [ ] ordering remains correct after mutations

### Test coverage

- [ ] unit tests cover sort, page math, prompt rendering, search, and validation rules
- [ ] integration tests cover first-page correctness, full-corpus visibility, stale-cache behavior, and lifecycle consistency
- [ ] end-to-end tests cover large corpora, cold start, warm start, stale cache, create/list, delete/list, and selection

### Scale

- [ ] behavior is proven on a realistic large dataset
- [ ] optimization paths preserve correctness

---

## Minimum acceptance criteria

Do not sign off this upgrade until:

1. the visible first page is proven correct on multi-page data
2. the page indicator is explicit and human-friendly
3. stale metadata cannot silently produce partial results
4. create/update/delete flows are proven to keep the list accurate
5. a large-corpus end-to-end test passes
6. tests validate user-visible output, not just internal helpers

---

## Short summary

This upgrade must be treated as a full product surface, not a small UI refactor.

A correct implementation must ensure:

- correct first-page ordering
- complete corpus visibility
- human-readable page prompts
- reliable cache/index behavior
- mutation consistency
- large-corpus correctness
- strong unit, integration, and end-to-end coverage

If those are not all defined and tested, the feature is not ready for sign-off.