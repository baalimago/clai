# Chat groups: entry-message clustering in `clai chat list`

This document defines how `clai chat list` clusters conversations that share the same entry (first user)
message into **collapsed groups**, and how selecting a group filters the list to its members. The mechanism
reuses the data-model pattern established by the `Source`/`SourceID` system — a single field, `GroupKey`,
that captures content identity — and requires zero changes to the table selection subsystem.

## Motivation

Users repeatedly issue the same initial prompt — a debugging query, a code-review instruction, an
investigation seed — producing multiple parallel conversations with identical entry messages. Today these
appear as indistinguishable rows in `clai chat list`, forcing the user to scan by date and prompt preview to
find related conversations. The (at the time of writing) upcoming **conversation forking** feature (`clai chat fork <id>`, described
in `continue-from-claudex.md`) will further multiply parallel conversations from a common root. Both
scenarios demand a grouping mechanism.

The grouping must:

- Collapse repeated entry messages into a single row so the list is scannable.
- Let the user expand a group to see (and select) individual member conversations.
- Reuse the existing table infrastructure — no rewrites to the table selection or pagination subsystem.
- Use a content-derived identity key that parallels `Source`/`SourceID` in structure but is orthogonal in
  meaning — `Source` tracks origin, the group key tracks content affinity.

## High-level flow

The chat list handler (`handleListCmd`) is refactored into a **loop** that manages view state: top-level
(collapsed groups) vs group-filtered (expanded members). Each iteration builds a view, calls
`SelectFromTable`, and dispatches the result.

```text
handleListCmd loop
  │
  ├─ groupKey = "" (top-level state)
  ├─ Read chat index + discover foreign conversations (as today)
  ├─ Dedup (as today)
  ├─ Compute group identity for each row from its first user message
  ├─ Build top-level view: collapse groups with ≥2 members
  ├─ Sort: most-recent-member first; GroupKey lexicographic as tiebreaker; ungrouped by date
  ├─ Call SelectFromTable
  │
  ├─ If user selected a group row (Kind == chatRowGroup):
  │     groupKey = selected.GroupKey
  │     re-enter loop → render group view
  │
  ├─ If user selected a conversation row and presses Enter:
  │     actOnChat(ctx, chat, groupKey)
  │     └─ [b] → returns to loop (with groupKey preserved if set)
  │
  ├─ If ErrBack (user pressed [b]):
  │     when groupKey != "" → clear groupKey, re-enter loop → top-level view
  │     when groupKey == "" → exit loop → exit list
  │
  ▼
Top-level view (groups collapsed):
    0 [group:3] | clai      | 2026-07-04 10:12:34 | gpt-5  | $1.35 | fix the auth bug in login handler
    1 [group:2] | clai      | 2026-07-03 22:15:00 | Diff.  | $3.05 | refactor database layer
    2           | clai      | 2026-07-03 19:00:00 | gpt-4o | $0.00 | add unit tests for handler

User selects index 0 (the [group:3] row)
  │
  ▼
Group view (filtered to group members):
    0 | clai     | 2026-07-04 10:12:34 | gpt-5  | $1.12 | fix the auth bug in login handler
    1 | clai     | 2026-07-04 09:45:01 | gpt-5  | $0.08 | fix the auth bug in login handler
    2 | clai     | 2026-07-04 08:30:22 | gpt-5  | $0.15 | fix the auth bug in login handler

    group: "fix the auth bug in login handler" ([b]ack to list, [/] filter, [q]uit, page 0/0):

User presses [b] → returns to top-level view (loop clears groupKey, re-renders).
User selects a conversation → normal chat-info/continue flow.
Note how the group accurately sums the conversations total costs.
Note how the if there are multiple different models used, it amounts to "Diff.", but if only one is used, it lists it
```

### Group expansion dispatch

Group rows use a distinct `chatRowKind` value — `chatRowGroup` — added to the existing enum:

```go
type chatRowKind uint8

const (
    chatRowNative  chatRowKind = iota // existing
    chatRowForeign                    // existing
    chatRowGroup                      // new
)
```

When `SelectFromTable` returns and the selected row has `Kind == chatRowGroup`, the handler loop sets
`groupKey` and re-enters instead of dispatching to `actOnChat`/`actOnForeignChat`. No changes to
`SelectFromTable` itself.

### Loop structure and state management

`handleListCmd` is refactored into a loop. Each iteration builds a view (top-level or group-filtered),
calls `SelectFromTable`, and dispatches the result. The loop state is a local `groupKey string` variable
(empty for top-level).

```go
func (cq *ChatHandler) handleListCmd(ctx context.Context) error {
    paginator, err := NewChatIndexPaginator(cq.convDir)
    if err != nil {
        return fmt.Errorf("failed to create chat index paginator: %w", err)
    }
    return cq.listChats(ctx, paginator, "" /* groupKey */)
}

func (cq *ChatHandler) listChats(ctx context.Context, paginator *ChatIndexPaginator, groupKey string) error {
    for {
        // ... build rows, filter by groupKey if set ...
        selectedNumbers, err := utils.SelectFromTable(...)
        if err != nil {
            if errors.Is(err, utils.ErrBack) {
                if groupKey != "" {
                    groupKey = ""        // return to top level
                    continue             // re-enter loop
                }
                return nil               // exit list
            }
            return err
        }
        sel := rows[selectedNumbers[0]]
        if sel.Kind == chatRowGroup {
            groupKey = sel.GroupKey
            continue                     // re-enter loop with group filter
        }
        // ... actOnChat / actOnForeignChat with groupKey ...
        // On [b] from actOnChat, return to loop (groupKey preserved)
    }
}
```

`actOnChat` and `actOnForeignChat` receive the current `groupKey` so that [b]ack returns to the correct
view:

```go
func (cq *ChatHandler) actOnChat(ctx context.Context, chat pub_models.Chat, groupKey string) error {
    // ... chat info, user choice ...
    case "B", "b":
        // Return nil — the caller (listChats loop) handles ErrBack semantics.
        // If groupKey != "", listChats re-renders the group view.
        return nil  // was: return cq.handleListCmd(ctx)
}
```

The `[b]` key transitions between three states via the outer loop:

| Context                             | `[b]` behavior       | Mechanism                                                    |
| ----------------------------------- | -------------------- | ------------------------------------------------------------ |
| Top-level view                      | Exit list            | `ErrBack` with `groupKey==""` → return nil                   |
| Group view                          | Return to top level  | `ErrBack` with `groupKey!=""` → clear groupKey, continue     |
| Chat info (entered from group view) | Return to group view | `actOnChat` returns nil → loop re-renders with same groupKey |

## Group identity

### GroupKey

Each conversation carries a **GroupKey** — the hex-encoded SHA-256 of the first user message's
**canonical text content**. The hash is computed as:

1. If `msg.Content` (the plain string field) is non-empty, hash `msg.Content` as raw UTF-8 bytes.
2. Otherwise, concatenate the `.Text` fields of `msg.ContentParts` in order and hash that
   concatenation.
3. If the resulting string is empty (image-only message, or no user message at all), the GroupKey
   is the empty string `""` — the conversation never participates in grouping.

The canonical byte sequence uses **raw UTF-8 bytes with no Unicode normalisation, no whitespace
trimming, no BOM stripping, and no trailing-newline stripping**. The hex encoding uses **lowercase**
hex digits. The full 64-character digest is used, not a truncated prefix, to avoid false collisions.

The GroupKey is stamped once when the conversation is first persisted and **never rewritten**.
Editing or deleting messages within a conversation does not change its GroupKey. The key reflects
the original entry prompt that created the conversation.

The GroupKey is mirrored into the chat index cache so that group membership can be determined
without opening conversation files — exactly like `Source`/`SourceID` and `OriginDir` are mirrored
today.

### Deliberate separation from chat ID hash

`HashIDFromPrompt` (in `chat.go`) also hashes the first user message to produce a chat ID. GroupKey
uses a different hash function (SHA-256 producing a 64-char hex digest vs the shorter chat ID) and a
more explicit canonicalization (Content vs ContentParts rules). These are deliberately separate: chat
IDs must remain stable for file naming; GroupKey is a content-affinity key. Do not derive one from
the other.

### Pre-existing conversations

Conversations saved before this feature have no GroupKey (empty field). **No migration is needed and
no GroupKey is computed on normal read.** The only path to GroupKey assignment for old conversations
is an index rebuild (`clai chat list` implicitly rebuilds when the cache is missing or deleted).
During normal operation, old conversations remain ungrouped. This is an explicit design choice —
GroupKey is a "stamped once on first persist" field, not a "computed on read" field.

During index rebuild, the GroupKey is computed from the current first user message, which may differ
from the original if messages were edited. This is acceptable because rebuild is an administrative
recovery operation, not the normal path.

### Relationship to Source/SourceID

`Source`/`SourceID` answer _"where did this conversation come from?"_ (external tool, fork, or native).
`GroupKey` answers _"which other conversations share the same originating prompt?"_. They are orthogonal:

- A non-forked clai chat has no Source and a GroupKey derived from its first user message.
- A cloned foreign chat has a Source (e.g. `"claude-code"`), a SourceID from the session, and a
  GroupKey derived from its first user message.
- A forked chat (future) has `Source = "clai"`, `SourceID = parent-chat-id`, and a GroupKey
  **inherited from the parent** — not recomputed from the fork's own first message, which may differ.

Both fields coexist without conflict. The pattern is parallel — a single opaque identity key —
but the semantics differ.

### Data model additions

#### `pub_models.Chat`

```go
type Chat struct {
    // ... existing fields ...
    // GroupKey is the hex-encoded SHA-256 of the first user message's canonical
    // text content (see §Group identity). Empty when no user message exists,
    // the first message is image-only, or the chat predates this feature.
    // Stamped once on first persist; never rewritten.
    GroupKey string `json:"group_key,omitempty"`
}
```

#### `chatIndexRow`

```go
type chatIndexRow struct {
    // ... existing fields ...
    GroupKey string `json:"group_key,omitempty"`
}
```

`chatIndexRowFromChat` copies `chat.GroupKey` into `chatIndexRow.GroupKey`, identical to how it
copies `OriginDir`.

#### `chatListRow`

```go
type chatListRow struct {
    // ... existing fields ...
    GroupKey string // set for all rows; group rows distinguish by Kind == chatRowGroup
    // GroupMemberCount is populated only for group rows (Kind == chatRowGroup).
    GroupMemberCount int
}
```

`buildChatListRows` copies `chatIndexRow.GroupKey` into `chatListRow.GroupKey`.

## Top-level view

When no group filter is active, the chat list shows a top-level view where groups with two or more
members are collapsed into a single summary row.

### Group rows

A group row displays:

- The index column shows both the numeric position and the group label: e.g. `"0 [group:3]"`. The
  numeric index is preserved so the user can select the row by its visible position. The `[group:N]`
  suffix provides visual distinction from conversation rows.
- The most recent member's timestamp as the row's `Created` date.
- The shared first user message as the prompt preview.
- Aggregate display fields (total messages, total tokens, total cost) summed across members.
  Foreign members contribute **zero** to token and cost aggregates — the displayed values are
  therefore **lower bounds** when the group contains foreign members.
- The most recent member's profile and model. If the most recent member is foreign (Profile = "N/A",
  Model possibly empty), fall back to the most recent **native** member's profile and model. If no
  native member exists, show "N/A" and empty model.
- The source column shows the most recent member's source. For mixed groups, this may be "clai",
  "claude-code", etc.

### Ungrouped rows

Conversations whose GroupKey is unique (only one conversation with that key, or GroupKey is empty)
appear as normal rows without group decoration. They are interspersed with group rows, sorted by
`Created` descending.

### Sort order

Rows are sorted by `Created` descending. For group rows, `Created` is the most recent member's
timestamp — so active groups float to the top. When two rows (group or ungrouped) have identical
timestamps, the tiebreaker is **GroupKey lexicographic order** (empty GroupKey sorts before
non-empty). Within a group view, member conversations are sorted by `Created` descending with the
same tiebreaker.

### Wide format

In wide format (>120 columns), group rows render aggregate columns:

```
%-6s| %-15s | %-20s| %-8v | %-15s | %-18s | %-8s | %-6s | %v
 0     [group:3]  clai      2006-01-02 15:04:05  12      my-profile  gpt-5             $0.35    15K     fix the auth bug...
```

The `[group:N]` label appears in the index column, identical to narrow format.

## Group view

When the user selects a group row, the list re-renders showing only the member conversations.
Conversations render with their normal indices (0, 1, …) and normal formatting — the index column
shows only the numeric position without group decoration.

### Prompt bar

The group view prompt bar includes a group indicator so the user knows they are in a filtered view:

```
group: "fix the auth bug in login handler" ([b]ack to list, [/] filter, [q]uit, page 0/0):
```

The prompt preview is truncated to fit the terminal width. The `[b]ack to list` label
disambiguates the `[b]` key from the top-level `[b]` (which exits).

### Pagination

Pagination works identically in group view. Groups large enough to span multiple pages use the same
`[n]ext`/`[p]rev` controls with the same page size as the top-level view.

### Filter state scoping

Filters (substring `/filter` and `[d]`ir predicate) are **scoped to the current view**. When
returning from group view to top level (via `[b]`), both filters are cleared. When entering a
group view from the top level, both filters are cleared.

Within the group view, the `/substring` filter narrows within the group's members. The `[d]`ir
toggle filters member rows as normal: native rows are subject to the dirscope predicate, foreign
rows are always shown. A group view may show zero rows after dirscope filtering (e.g., all native
members hidden and no foreign members exist). The prompt line indicates the filter is active.
Pressing `[d]` again or `[b]`ack restores the view.

## Back navigation from chat info

When the user selects a conversation within a group view and enters chat info, pressing `[b]`ack
from chat info must return to the **group view**, not the top-level list. This means the active
group filter must survive the round-trip through chat-info and back to the list handler.

The chat info display includes a group context indicator when entered from a group view:

```
=== Chat info ===
group: "fix the auth bug in login handler" ([b]ack to group)
file path: /home/user/.config/clai/conversations/abc123.json
...
```

The GroupKey itself is displayed in chat info (both native and foreign) when non-empty, enabling
debugging and test verification:

```
group key: a1b2c3d4e5f6...
```

The full expected journey:

```text
top-level → select [group:N] → group view → select conversation
  → chat info → [b]ack → group view → [b]ack → top level → [b]ack → exit
```

## Dirscope filter compatibility

The `[d]irscoped convs` toggle filter applies only to native conversation rows (as today). Group rows
are never hidden by dirscope filtering — this matches the existing rule for foreign rows. Group rows
have `Kind == chatRowGroup` and the dirscope predicate always returns `true` for non-native rows.

Within a group view, the `[d]`ir toggle filters member rows as normal: native rows are subject to
the dirscope predicate, foreign rows are always shown. A group view may show zero rows after
dirscope filtering (all native members hidden, no foreign members). The prompt line indicates the
dir filter is active. Pressing `[d]` again or `[b]`ack restores the view.

## Substring filter compatibility

The `/substring` filter works identically in both top-level and group views. In the top-level view,
it searches across group rows and ungrouped rows alike. In the group view, it narrows within the
group's members. Filter state is scoped to the current view — returning to the top level clears
the substring filter.

## Forking integration

When `clai chat fork <chatID>` is implemented (per `continue-from-claudex.md`):

- The forked chat inherits the parent's GroupKey — all descendants of the same root prompt share
  the same group, regardless of where in the lineage the fork occurred.
- `Source = "clai"` and `SourceID = <parent-chat-id>` are set independently.
- The fork's first user message may differ from the parent's, but the GroupKey is inherited, not
  recomputed — preserving the lineage link.
- The parent's GroupKey is never changed by forking.
- **Visual inconsistency note**: Two forks of the same parent could have different first user
  messages but the same GroupKey. The group row's prompt preview column shows the most recent
  member's first user message, which may differ from the original root prompt. The group identity
  reflects lineage, not the displayed text.

### Forking from a pre-existing chat with empty GroupKey

If the parent is a pre-existing chat with an empty GroupKey, the fork also inherits an empty
GroupKey. Both parent and fork appear as separate ungrouped rows — visually unrelated despite the
`Source="clai"`, `SourceID=parent-id` link. An index rebuild assigns GroupKeys to both (derived
from each chat's own first user message, which may differ), restoring the group relationship if
their first messages match.

## cloneForeignChat integration

`cloneForeignChat` calls `Save()`, which stamps GroupKey on first persist. No explicit GroupKey
computation is needed in the clone function itself — `Save()` handles it uniformly for all persist
paths. The cloned chat's GroupKey is derived from its first user message (which is the original
foreign conversation's first user message) using the same canonicalization rules as native chats.

## Edge cases and limitations

### Concurrent modification

Group membership is computed from the index at view-build time and is not refreshed between
selection cycles. Concurrent modifications by other clai processes may cause stale group views
(e.g., a group expanded to show N members but one was concurrently deleted). This is accepted —
clai is a single-user CLI tool.

### Empty states

- **Top-level with zero conversations**: Unchanged from today — an empty table with the standard
  prompt line (`found '0' conversations:`).
- **Group view with zero members**: If all group members were deleted between building the
  top-level view and selecting the group row, the group view shows zero rows. The prompt bar
  still shows the group indicator. Pressing `[b]` returns to the top level, where the group row
  no longer appears.
- **Group view with all members filtered**: Dirscope or substring filtering may result in zero
  visible rows. The prompt line indicates the active filter. Pressing the toggle key or `[b]`
  restores visibility.

### Prompt preview collision

Two conversations with different first user messages that truncate to the same prompt preview
(e.g., "fix the auth bug in login handler" vs "fix the auth bug in login handler please") will
appear as separate ungrouped rows with identical preview columns. This is a pre-existing display
limitation of width-constrained previews, not specific to grouping.

## Acceptance criteria

1. **GroupKey computation** — a conversation with a first user message containing text gets a
   GroupKey equal to the lowercase hex-encoded SHA-256 of the canonical text (see §Group identity).
   Conversations with no user message or an image-only first message have an empty GroupKey.

2. **Index round-trip** — a conversation saved with a GroupKey is read back from the index with
   the same GroupKey. Index caches from before this feature (without the field) decode without error
   and produce empty GroupKeys.

3. **GroupKey immutability** — editing or deleting messages within a conversation does not change
   its GroupKey. The key is stamped once on first persist. Verification: create a conversation,
   edit its first user message, rebuild the index, and verify the conversation appears in the same
   group it originally belonged to (the index rebuild may change the GroupKey, but the on-disk Chat
   retains the original stamp; grouping before rebuild uses the original stamp). Chat info display
   includes the GroupKey (or a short prefix) when non-empty so tests can inspect it.

4. **Top-level: groups collapsed** — when two or more conversations share the same non-empty
   GroupKey, `clai chat list` shows a single `[group:N]` row instead of N individual rows. The
   row's date reflects the most recent member. The index column shows both the numeric position
   and the group label (e.g. `"0 [group:3]"`). Groups appear only for non-empty GroupKeys with
   N ≥ 2.

5. **Top-level: ungrouped rows** — conversations with a unique GroupKey (including empty) appear
   as normal rows without group decoration.

6. **Group expansion** — selecting a `[group:N]` row re-renders the table showing only the group's
   member conversations. The rendered table contains exactly N rows, each with a numeric index
   0..N-1, and every row's first-user-message preview matches the group's shared prompt. The user
   can select any conversation and proceed to the normal chat-info/continue flow.

7. **Back from group view** — pressing `[b]`ack in the group view returns to the top-level
   (collapsed) view. Pressing `[b]`ack at the top level exits the list.

8. **Back from chat info returns to group view** — when the user entered chat info from within a
   group view, pressing `[b]`ack returns to the group view (not the top-level list). Verification:
   after entering chat info from a group view and pressing `[b]`, the rendered table shows the
   same N rows and the prompt line includes the group indicator (not the top-level summary with
   `[group:N]` decorations).

9. **Full navigation journey** — the complete round-trip works end-to-end:
   `top-level → select group → group view → select conversation → chat info → [b]ack → group view → [b]ack → top level → [b]ack → exit`.

10. **Foreign rows in groups** — foreign conversations participate in grouping identically to native
    conversations (same GroupKey derivation from first user message). A group can contain both native
    and foreign members. Foreign members contribute zero to token and cost aggregates.

11. **Dirscope filter preserves group rows** — the `[d]`ir toggle never hides `[group:N]` rows.
    Within a group view, the `[d]`ir toggle filters member rows as normal.

12. **Substring filter in group view** — applying the `/filter` in a group view narrows results
    within the group's members only. Filter state is cleared when navigating between views.

13. **Index rebuild** — rebuilding the chat index correctly computes and persists GroupKey for all
    conversations. (The rebuild re-reads current messages and may produce a different key than the
    original stamp — this is acceptable because rebuild is an administrative recovery operation,
    not the normal path.)

14. **No false grouping** — two conversations whose first user messages differ by any amount (one
    character, trailing whitespace, etc.) get different GroupKeys and appear as separate rows.

15. **Forking: GroupKey inheritance** — a forked conversation inherits its parent's GroupKey,
    regardless of what the fork's own first user message is.

16. **Chat info includes GroupKey** — when viewing chat info for a conversation with a non-empty
    GroupKey, the GroupKey (or a short prefix) is displayed so users and tests can inspect it.

17. **Chat info includes group context** — when entered from a group view, chat info displays a
    group context indicator (e.g. `group: "<prompt preview>" ([b]ack to group)`).

18. **cloneForeignChat GroupKey via Save()** — `cloneForeignChat` calls `Save()`, which stamps
    GroupKey on first persist. No explicit GroupKey computation is needed in the clone function
    itself — `Save()` handles it uniformly for all persist paths.

19. **Sort stability** — when two rows have identical timestamps, GroupKey lexicographic order
    serves as the tiebreaker (empty GroupKey sorts before non-empty).

20. **Group view pagination** — groups with many members paginate identically to the top-level
    view with the same `[n]ext`/`[p]rev` controls.

21. **Go test suite** — `go test ./... -race -cover -timeout=10s` passes. Staticcheck and
    gofumpt are clean.

## E2E test expectations

E2E tests use a temporary configuration directory with seeded conversation fixtures.

1. **Basic grouping** — three conversations with identical first user messages and one with a
   different message. `clai chat list` output shows one `[group:3]` row and one ungrouped row.
   The `[group:3]` row is positioned according to the most recent member's timestamp.

2. **No false grouping** — two conversations whose first messages differ by one character appear
   as separate ungrouped rows.

3. **Single-member suppression** — a GroupKey with only one conversation produces no group row;
   the conversation appears as a normal ungrouped row.

4. **Group expansion and selection** — select the `[group:3]` row, verify the view shows exactly
   3 conversations, each with numeric indices 0..2, select one by index, verify the correct chat
   is loaded.

5. **Back navigation** — from group view, press `[b]`ack, verify return to top-level view showing
   the group row. From top-level, press `[b]`ack, verify exit from list.

6. **Full journey** — top-level → select group → select conversation → chat info → `[b]`ack →
   back at group view → `[b]`ack → back at top level → `[b]`ack → exit.

7. **Substring filter in group view** — in the group view, apply `/filter`, verify filtering
   narrows within the group members.

8. **Dirscope filter preserves group rows** — enable `[d]`ir filter; verify `[group:N]` rows
   remain visible even when member conversations would be hidden.

9. **Foreign + native mixing** — one native and one foreign conversation with the same first user
   message appear together under one `[group:2]` row.

10. **Index rebuild preserves GroupKey** — delete the chat index cache, trigger a rebuild by
    running `clai chat list`, verify GroupKey is present on all rows with user messages and
    consistent with the conversation files.

11. **GroupKey persistence across restart** — create a conversation, quit clai, restart, run
    `clai chat list`, verify GroupKey is preserved and grouping still works.

12. **Image-only first message ungrouped** — a conversation whose first user message is image-only
    (Content empty, ContentParts all images) has an empty GroupKey and appears as an ungrouped row,
    never collapsing with other image-only conversations.

## Future: group filter actions in the prompt bar

A natural extension is adding group-specific toggle actions to the prompt bar (e.g. `[g1] fix auth...`
for each group). This would let users filter to a group without first selecting its row. Deferred
because the collapsed-rows mechanism already delivers the core value, and the action-bar approach
needs a strategy for large numbers of groups (hotkey exhaustion). The group-row concept is
forward-compatible with any action-bar extension.
