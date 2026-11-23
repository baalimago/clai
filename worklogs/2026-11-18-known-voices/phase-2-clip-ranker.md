# Phase 2 — Clip ranker

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Order clips from best to worst by how their namings fared, as the one decision behind which clips are
picked for a naming request and which are pruned (F7).

## Interface

Written to `internal/audio/voices/rank.go`.

```go
package voices

import "time"

// Ranker orders clips by their events. It is the only thing that decides
// which clips are picked and which are pruned. (F7)
type Ranker interface {
	// Rank returns the clips best first, as of now.
	//
	// - Rank.1 — The result holds every given clip exactly once and nothing
	//   else. Clips of several people may be ranked together; each keeps its
	//   Ref. No clip is an empty result, not an error.
	// - Rank.2 — The order depends only on the clips' histories and on now:
	//   the same clips in any input order give the same result, and nothing
	//   is read from a clock, a file, the environment or an earlier call.
	//   (F7)
	// - Rank.3 — The order is by score, the smoothed share of the clip's
	//   uses that went well. A naming that was never overruled counts for the
	//   clip; an overrule, and a placement that did not land, count against
	//   it. One more naming that was not overruled never lowers a clip; one
	//   more overrule, or one more unlanded placement, never raises it. A
	//   placement that landed and named nobody, and events before the clip's
	//   latest added event, change nothing. (F7, F6)
	// - Rank.4 — The share is smoothed, so a clip with few uses is not
	//   over-trusted and an untried clip starts low: a clip named once and
	//   never overruled ranks below a clip named twenty times and overruled
	//   once, and a clip never used ranks below a clip named three times and
	//   overruled twice, and above a clip whose namings were all overruled.
	//   (F7)
	// - Rank.5 — Recent uses weigh more than old ones: of two clips with the
	//   same number of namings and overrules, the one whose overrules are
	//   old and whose other namings are recent ranks first. (F7)
	// - Rank.6 — Clips with equal scores rank by the later added time first,
	//   then by slug and id, so the order is total.
	// - Rank.7 — The smoothing, the recency weight, any preference between
	//   clips beyond these clauses and every constant are the
	//   implementation's: there is nothing to configure, and no option, file
	//   or environment variable changes the order. (F7)
	Rank(clips []ClipHistory, now time.Time) []ClipHistory
}

// NewRanker returns the one Ranker.
//
// - NewRanker.1 — The Ranker holds no state: calls do not affect each other
//   and may run at once.
func NewRanker() Ranker
```

Consumes:

- `ClipHistory`, `ClipRef`, `Event`, `EventKind`, `Status` (phase 1)
- `internal/testenv:Main` (import)

## Acceptance criteria

| #   | Criterion                                                                                        | Proves                   | Proof                          |
| --- | ------------------------------------------------------------------------------------------------ | ------------------------ | ------------------------------ |
| 1   | Every clip comes back once, in an order that does not depend on the input order or on the clock  | `Rank.1`, `Rank.2`, `Rank.6`, F7 | `TestRank_TotalAndStable` |
| 2   | More kept namings never lower a clip; more overrules and more unlanded placements never raise it | `Rank.3`, F7, F6         | `TestRank_Monotonic`, `TestRank_UnlandedCountsAgainst` |
| 3   | A clip with few uses is not over-trusted, and an untried clip starts low                         | `Rank.4`, F7             | `TestRank_FewUsesNotTrusted`   |
| 4   | Recent uses outweigh old ones                                                                    | `Rank.5`, F7             | `TestRank_RecentWeighsMore`    |
| 5   | The README's example table (14 named 1 wrong, 3 named 2 wrong, never named) ranks in that order by score alone | `Rank.3`, `Rank.4` | `TestRank_MenuExample`     |
| 6   | Nothing configures the ranking, and concurrent calls agree                                       | `Rank.7`, `NewRanker.1`, F7 | `TestRank_NoConfiguration`, `TestRank_Concurrent` |

## Test cases

Every row builds `ClipHistory` values in memory and passes a fixed `now`; no file is read. Unless a row
says otherwise its clips share one status, so no row depends on how the heuristic treats status.

| Test                         | Level          | Proves                               | Given                                                                                                              | Then                                                                                      | Must not                                  |
| ---------------------------- | -------------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------- | ----------------------------------------- |
| `TestRank_TotalAndStable`    | contract suite | `Rank.1`, `Rank.2`, `Rank.6`, AC 1   | Eight clips of two people, three with identical histories but different added times and ids; a sample of permutations of the input; an empty input | One same order for every permutation, each clip once with its `Ref`; equal scores by later `Added`, then slug and id; empty in, empty out | Drop or repeat a clip, or change the input slice |
| `TestRank_Monotonic`         | contract suite | `Rank.3`, F7, AC 2                   | A base clip with 5 namings and 1 overrule; a twin with one more naming; a twin with one more overrule; a twin with 30 extra `placed` events that landed and a naming dated before its `added` | The first twin is not below the base, the second not above it, the third ties with it     | Count a landed placement, or events of an earlier life |
| `TestRank_UnlandedCountsAgainst` | contract suite | `Rank.3`, F6, F7, AC 2           | Two clips with 5 namings each, never overruled; one also has 10 `placed` events with `landed` false; then two never-named clips, one with 3 unlanded placements | The clip without unlanded placements is first, both times                                  | Ignore `landed`                           |
| `TestRank_FewUsesNotTrusted` | contract suite | `Rank.4`, F7, AC 3                   | Clip A: 1 naming, 0 overrules. Clip B: 20 namings, 1 overrule. Clip C: never used. Clip D: 5 namings, 5 overrules. Clip E: 3 namings, 2 overrules. All the same day | B before A; E before C; C before D                                                        | Rank by raw share (A's 100 % over B's 95 %) |
| `TestRank_RecentWeighsMore`  | contract suite | `Rank.5`, F7, AC 4                   | Two clips with 4 namings and 2 overrules each: one overruled 300 days ago and named this week, the other named 300 days ago and overruled this week | The clip with the old overrules is first                                                  | Ignore `now`, or read the wall clock      |
| `TestRank_MenuExample`       | contract suite | `Rank.3`, `Rank.4`, AC 5             | `9f3c2a1b0d4e` 14 namings 1 overrule, `e81d5530c6a9` 3 namings 2 overrules, `1a7d0f23c9b8` never named, events dated as in "The voices menu, by example"; once with the example's statuses and once with all three unconfirmed | That order both times                                                                     | Need a clip's status to get the order     |
| `TestRank_NoConfiguration`   | unit           | `Rank.7`, F7, AC 6                   | The fixture of `TestRank_MenuExample`, ranked with `CLAI_CONFIG_DIR` pointing at a directory holding an `audioConfig.json` with a `transcribe` block, and with no such directory | The same order both times; `NewRanker` takes no argument                                  | Open a file                               |
| `TestRank_Concurrent`        | unit           | `NewRanker.1`, AC 6                  | One `Ranker`, 16 goroutines ranking two different fixtures                                                         | Every goroutine gets its fixture's order; clean under `-race`                             | Share state between calls                 |

## Error management

None. `Rank` has no failure: every input, the empty one included, has an order.

## Golden diff

None.

## Removes

None.

## Local facts

None. The ranking's constants are code (F7), so they are named nowhere in the worklog.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                      | Result                |
| ----- | ----------- | ---------------------------- | --------------------- |
| start | 1550dec+wip | `go test ./internal/audio/voices/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck, `gofumpt -l` on `./internal/audio/voices/...`; `dupl -t 80 .` | 46 tests ×3 passed, 0 failed (voices 94.4 %); vet, staticcheck, gofumpt clean; 49 clone groups |
| end   | 1550dec+wip | the same                     | 54 tests ×3 passed, 0 failed (voices 94.8 %, `rank.go`/`ranker.go` 100 %); vet, staticcheck, gofumpt clean; 49 clone groups, none in phase 2 files |
| end   | 1550dec+wip | the same with `-shuffle=on`  | 54 tests ×3 passed, 0 failed (voices 94.8 %); vet, staticcheck, gofumpt clean; 49 clone groups, none in phase 2 files |

Andon: none.
Surprises:
- gofumpt re-indents the `NewRanker.1` doc-comment bullet; `rank.go` equals the Interface block (with `NewRanker`'s body) after gofumpt, checked by diff.

## Review findings

None.
