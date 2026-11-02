# Phase 3 — Level policy

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Resolve diagnostic levels for a registry of stable subsystem names.

## Interface

`Level` is public; the resolver is internal (F10). Written to
`pkg/diagnostic/level.go`:

```go
package diagnostic

type Level uint8

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)
```

and to `internal/diag/levels.go`:

```go
package diag

import "github.com/baalimago/clai/pkg/diagnostic"

// LevelLayer maps a default or dotted subsystem selector to a level.
// The empty selector names the default. Layers are ordered from low to high
// precedence. Registry names and selector names are case-insensitive.
type LevelLayer map[string]diagnostic.Level

// LevelRequest is the owner's snapshot of the registry and the configuration.
// Legacy holds the value of each legacy control named in the README's Legacy
// controls table, keyed by its variable name; the owner reads the environment,
// the resolver never does.
type LevelRequest struct {
	Registry []string
	Legacy   map[string]string
	Layers   []LevelLayer
	LogLevel string
}

type ResolvedLevels map[string]diagnostic.Level

type LevelResolver interface {
	// Resolve returns a level for every registered name and, under the empty
	// selector, the default. The default is the Diagnostic level parameter
	// (README Parameters). LOG_LEVEL accepts one
	// default and dotted name=level overrides, with the level names and the
	// alias F7 defines. The resolver is internal to clai. (Resolve.1, F7, F10)
	//
	// Legacy entries form the lowest-precedence layer, mapped as the README's
	// Legacy controls table states and in its row order. Layers follow, then
	// LOG_LEVEL, each replacing the same selector from an earlier layer. The
	// longest complete dotted selector wins after the layers merge.
	// (Resolve.2, F7, F9)
	//
	// Malformed entries, duplicate selectors in LOG_LEVEL, invalid levels,
	// unknown selectors, and a Legacy key the table does not name return an
	// error that identifies the entry. (Resolve.3, F7)
	Resolve(LevelRequest) (ResolvedLevels, error)
}
```
Consumes: `Level` (this phase, public), the Legacy controls table (root), and
`go_away_boilerplate/pkg/misc:Truthy` (import).

## Acceptance criteria

| #   | Criterion                                                                 | Proves        | Proof                       |
| --- | ------------------------------------------------------------------------- | ------------- | --------------------------- |
| 1   | Defaults, aliases, and explicit subsystem overrides resolve consistently. | Resolve.1     | `TestLevelResolverContract` |
| 2   | Layer precedence and the longest complete dotted name select the level.   | Resolve.2     | `TestLevelResolverContract` |
| 3   | Invalid or unknown entries return an identifiable error.                  | Resolve.3     | `TestLevelResolverContract` |
| 4   | Every row of the Legacy controls table maps as stated, and nothing else.  | Resolve.2, F9 | `TestLegacyLevelMapping`    |
| 5   | Only `Level` and its constants are exported from `pkg/diagnostic` by this phase. | Resolve.1, F10 | `TestPublicAPISurface` (phase 1) |

## Test cases

| Test                        | Level          | Proves                          | Given                                                                              | Then                                                                | Must not                                                   |
| --------------------------- | -------------- | ------------------------------- | ---------------------------------------------------------------------------------- | ------------------------------------------------------------------- | ---------------------------------------------------------- |
| `TestLevelResolverContract` | contract suite | Resolve.1, Resolve.2, Resolve.3 | Defaults, nested selectors, case variants, invalid entries, and conflicting layers | Each registered name has the expected level or the entry is refused | Match a partial segment or silently accept an unknown name |
| `TestLegacyLevelMapping`    | contract suite | Resolve.2, Resolve.3, F9        | Each legacy control alone, in combination, with `LOG_LEVEL`, and an unlisted key   | The table's selector and precedence select the levels; the unlisted key is refused | Read the environment or map `DEBUG_CPU` or `DEBUG_OUTPUT_FILE` to a level |
| `TestPublicAPISurface`      | repeatable command | Criterion 5, F10                | The exported identifiers of `pkg/diagnostic` after this phase                       | Every identifier is in the F10 allowlist                            | Count `internal/diag`                                      |

## Error management

| Failure               | Expected outcome                                                                   | Test                        |
| --------------------- | ---------------------------------------------------------------------------------- | --------------------------- |
| Invalid configuration | Return an error that identifies the invalid entry; do not produce a usable policy. | `TestLevelResolverContract` |
| Unlisted legacy key   | Return an error that names the key; do not produce a usable policy.                | `TestLegacyLevelMapping`    |

## Golden diff

None.

## Removes

None.

## Local facts

The caller supplies the registry and the configuration snapshot. Resolution
does not read process-global configuration or change retry behavior.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-07: `LevelRequest.Legacy` added; Resolve.2 maps it per the root Legacy controls table. V1-08: Resolve.1 cites the Diagnostic level parameter. V1-03: criterion 4 and Resolve.2 cite F9. |
| replan    | 2c9c4ff+wip | —       | dry run: Resolve.1 cites F7 for level names and the `warning` alias instead of restating them. |
| replan    | 2c9c4ff+wip | —       | F10: `Level` stays in `pkg/diagnostic`; `LevelResolver`, `LevelRequest`, `LevelLayer`, `ResolvedLevels` move to `internal/diag`. |
| replan    | 2c9c4ff+wip | —       | V3-03: `TestPublicAPISurface` row; criteria renumbered. Resolve.1 puts the default under the empty selector of `ResolvedLevels`. |
| replan    | 2c9c4ff+wip | —       | round 4 note: Local facts no longer restate the README's truthiness rule. |

## Review findings

None.
