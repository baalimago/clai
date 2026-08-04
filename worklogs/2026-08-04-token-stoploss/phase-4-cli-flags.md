# Phase 4 — CLI flags

**Status:** Not Started
**Back to:** [README](./README.md)

## Goal

Expose both run limits as CLI flags (`-max-tokens`, `-max-tool-calls`); the
handover instructions message is NOT a flag.

## Specification

### `internal/setup_flags.go`

`internal.Configurations` gains:

```go
MaxTokens        int  // -max-tokens value; meaningful only when MaxTokensSet
MaxTokensSet     bool // true when -mt/-max-tokens was explicitly passed
MaxToolCalls     int  // -max-tool-calls value; meaningful only when MaxToolCallsSet
MaxToolCallsSet  bool // true when -mtc/-max-tool-calls was explicitly passed
```

`parseFlags` gains (defaults 0 — 0 means "no limit" and is also the unset
sentinel; explicit-set is tracked with `fs.Visit`, following the
`-lb/-lookback`/`UseLookbackSet` precedent):

```go
maxTokensShort := fs.Int("mt", defaults.MaxTokens, "Set the max context tokens for this run. 0 = unlimited. Overrides stoploss.max-tokens in textConfig.json.")
maxTokensLong := fs.Int("max-tokens", defaults.MaxTokens, "Set the max context tokens for this run. 0 = unlimited. Overrides stoploss.max-tokens in textConfig.json.")
maxToolCallsShort := fs.Int("mtc", defaults.MaxToolCalls, "Set the max tool calls for this run. 0 = unlimited. Overrides max-tool-calls in textConfig.json.")
maxToolCallsLong := fs.Int("max-tool-calls", defaults.MaxToolCalls, "Set the max tool calls for this run. 0 = unlimited. Overrides max-tool-calls in textConfig.json.")
```

Explicit-set detection:

```go
fs.Visit(func(f *flag.Flag) {
	switch f.Name {
	case "mt", "max-tokens":
		MaxTokensSet = true
	case "mtc", "max-tool-calls":
		MaxToolCallsSet = true
	}
})
```

Both short and long spellings set the same logical value. If only one spelling
is supplied, it wins. If both are supplied with equal values, the input is
accepted. If both are supplied with different values, parsing returns the
same mutual-exclusion error used by the existing `ReturnNonDefault` pattern.
This applies to zero as well: `-mt=0 -max-tokens=0` is valid, while
`-mt=0 -max-tokens=5` is rejected.

Alias resolution is based on explicit visitation, not comparison with the
parser defaults. After `fs.Parse`, record four independent booleans from
`fs.Visit`: `mtSet`, `maxTokensSet`, `mtcSet`, and `maxToolCallsSet`. Resolve
each alias pair with this rule: if neither alias is set, use the default; if
exactly one is set, use that value; if both are set and equal, use that value;
otherwise return the mutual-exclusion error. This rule applies when a value
equals its default and when the value is zero. The resulting `MaxTokensSet`
and `MaxToolCallsSet` fields mean that at least one alias in the corresponding
pair was explicitly supplied.

`applyFlagOverridesForText` (text precedence: flags > profile > file > default):

```go
if flagSet.MaxTokensSet {
	if tConf.Stoploss == nil {
		tConf.Stoploss = &text.Stoploss{}
	}
	tConf.Stoploss.MaxTokens = flagSet.MaxTokens
}
if flagSet.MaxToolCallsSet {
	tConf.MaxToolCalls = &flagSet.MaxToolCalls
}
```

Notes:

- `-max-tokens=0` explicitly overrides a file's `stoploss.max-tokens` to
  unlimited (this is why `MaxTokensSet` exists).
- `-max-tool-calls=0` explicitly overrides a file's `max-tool-calls` to
  unlimited.
- The instructions message has NO flag; it comes from `textConfig.json`
  (`stoploss.max-tokens-handover-instructions`) or the agent API
  (`WithStoploss`). When `-max-tokens` is given without a configured message,
  the default `DefaultHandoverInstructions` applies.

### `main.go` usage

Add to the flags section:

```text
  -mt, -max-tokens int             Set the max context tokens for this run. 0 = unlimited (default is found in %v/textConfig.json)
  -mtc, -max-tool-calls int        Set the max tool calls for this run. 0 = unlimited (default is found in %v/textConfig.json)
```

## Integration contract

| Input / trigger                                              | Collaborators / fakes                      | Externally observable result                                                     | Required side effects | Prohibited side effects            |
| ------------------------------------------------------------ | ------------------------------------------ | -------------------------------------------------------------------------------- | --------------------- | ---------------------------------- |
| `clai -max-tokens=5000 q ...`                                | `parseFlags` → `applyFlagOverridesForText` | `text.Configurations.Stoploss.MaxTokens == 5000`; message left from file/default | none                  | instructions overwritten           |
| `clai -mt=0 q ...` with file `stoploss.max-tokens=100`       | same                                       | `Stoploss.MaxTokens == 0` (unlimited — flag overrides file)                      | none                  | file value surviving an explicit 0 |
| `clai -max-tool-calls=2 q ...` with file `max-tool-calls: 5` | same                                       | `Configurations.MaxToolCalls` points at 2                                        | none                  | file value surviving               |
| `clai -max-tool-calls=0 q ...` with file `max-tool-calls: 5` | same                                       | unlimited for this run                                                           | none                  | file value surviving               |
| No flags given                                               | same                                       | `MaxTokensSet == false`, `MaxToolCallsSet == false`; file config untouched       | none                  | nil-pointer deref on `Stoploss`    |

## Acceptance criteria

1. Flag parsing unit tests: short and long spellings parse; mutual exclusion
   handled per the model-flag pattern; explicit `0` is detected as set
   (`MaxTokensSet`/`MaxToolCallsSet` true).
2. Override-cascade tests (mirroring the existing `applyFlagOverridesForText`
   tests): flag beats file for both limits; explicit 0 disables a file limit;
   omitted flag leaves the file value.
3. Alias tests cover one alias, both equal aliases, and both conflicting
   aliases for each limit, including explicit zero.
4. `clai h` shows both flags.
5. `go test ./internal/ -timeout=60s` and the e2e suite green.

## Error coverage

| Failure condition                                        | Expected error / recovery / external outcome                          | Test              |
| -------------------------------------------------------- | --------------------------------------------------------------------- | ----------------- |
| `-max-tokens` and `-max-tokens=abc` (non-integer)        | `flag` parse error, propagated by `parseFlags`                        | flag parsing test |
| Both `-mt` and `-max-tokens` given with different values | Resolved per the existing `ReturnNonDefault` mutual-exclusion pattern | flag parsing test |

## Implementation notes

To be written by the executing agent.

## Review findings (review 3, 2026-08-04)

- [x] **R3-03 — Medium:** Make the new alias conflict path return an error from
      `parseFlags`, rather than route through `exitWithFlagError`, which calls
      `os.Exit` at `internal/setup_flags.go:306`. The acceptance criteria require
      unit-testable parsing, including conflicting aliases. Preserve the existing
      user-facing error at the top-level caller if needed, but keep parsing itself
      free of process termination.

## Review resolution (review 4, 2026-08-04)

- [x] **R3-03:** Alias conflicts return errors from `parseFlags`. The top-level
      `Setup`/CLI boundary may format the error and exit, but the parser itself
      remains unit-testable.

## Review findings (review 5, 2026-08-04)

- [x] **R5-02 — Medium:** Resolved in Review 6. The specification now requires
      independent `fs.Visit` tracking for each alias and defines the four-state
      resolver: neither, one, both equal, or both conflicting. Tests must cover
      explicit zero and explicit values equal to defaults.
