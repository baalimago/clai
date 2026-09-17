# Phase 0 — Measurement gate

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Make the repository able to produce the evidence every later phase cites: a
working CPU profiler, a deterministic generated JSONL corpus with the fixtures
every later invariant needs, a shared counting filesystem, and a recorded
discovery baseline.

## Specification

### Profiler repair

`main` starts a CPU profile under `DEBUG_CPU` and defers `pprof.StopCPUProfile`,
then calls `os.Exit`, which runs no deferred function. The profile written has
always been empty. Split the profiled body out of `main` so the deferred stop
runs before the process exits:

```go
func main() {
	ancli.SetupSlog()
	os.Exit(runProfiled(os.Args[1:]))
}

// runProfiled wraps run with CPU profiling when DEBUG_CPU is set. It must
// return rather than exit, so the deferred StopCPUProfile actually runs.
func runProfiled(args []string) int { ... }
```

| Rule                                                                 | Mechanism                                                  |
| -------------------------------------------------------------------- | ---------------------------------------------------------- |
| A profiling failure never prevents the command from running          | Every error path falls through to `run(args)`              |
| The profile is stopped and the file closed before the return value reaches `os.Exit` | `defer` inside `runProfiled`, which returns normally |
| The output file name is a named constant, not a literal              | `cpuProfileFileName`, whose value is `cpu-profile-file-name` |
| `run` keeps its current signature and behaviour                      | `runProfiled` only wraps it                                 |

### Generated corpus

A new package `internal/vendors/jsonltest` provides fixtures for every later
phase, in the style of `net/http/httptest`: exported helpers, no production
imports. Each fatal-ing helper wraps an error-returning core, because a helper
that only calls `tb.Fatal` has no failure a test can observe (D21):

```go
type Shape int // ShapeClaude, ShapePi

type CorpusOptions struct {
	Shape           Shape // README: jsonltest.CorpusOptions.Shape
	Sessions        int   // README: jsonltest.CorpusOptions.Sessions
	LinesPerSession int   // README: jsonltest.CorpusOptions.LinesPerSession
	Seed            int64 // README: jsonltest.CorpusOptions.Seed
}

// WriteCorpus fails the test on error; WriteCorpusErr is what error rows test.
func WriteCorpus(tb testing.TB, root string, opts CorpusOptions) Corpus
func WriteCorpusErr(root string, opts CorpusOptions) (Corpus, error)

type Corpus struct {
	Root     string
	Files    []string      // walk order; fixed-width names make it a lexical sort too
	Sessions []SessionFact
}

// SessionFact is the oracle: tests assert discovery against it, never against
// a number written into a phase file.
type SessionFact struct {
	Path          string
	SessionID     string
	Cwd           string
	Created       time.Time
	Model         string
	FirstUserText string
	Messages      int // exact, counted as that shape's schema counts
}
```

The corpus must contain, in every shape, at least one file of each awkward
kind. These are not decoration: each one is the only thing standing between a
later phase and a silent regression.

| Fixture kind                                                          | The invariant it guards                                                                 |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| A session longer than the per-session line default                     | The line cap truncates counts today, and deleting it fixes them                            |
| A message body quoting a role marker verbatim                          | The byte-prefilter trap: a naive substring count over-counts it                             |
| A sidechain file, and sidechain lines inside an otherwise normal file   | `LineRoleSkip` excludes them from counts and from metadata                                  |
| A tool-result message (pi shape)                                       | pi counts tool results, Claude does not; the counting rule is per schema                    |
| A file whose first user line carries only tool-result content          | The preview skips it and the count does not — the two rules diverge, and both must be right |
| A file with no session identity                                        | Discovery drops it, and the index caches that fact                                          |
| An empty file, and a file whose last line has no newline               | Scanner edge cases that must not error                                                      |
| A file whose identity line is not the first line                       | Keeps "the first line that establishes identity" honest                                     |
| A file carrying two distinct session identities                        | The guard for D23: first identity wins, in both discovery and lookup                        |

`WriteCorpusErr` is deterministic for a given seed: two calls produce
byte-identical files. Timestamps derive from the seed, never from `time.Now`,
so golden comparisons are stable. Later phases use the default seed rather than
choosing their own, so every phase asserts against the same fixture content.

### Counting filesystem

`jsonltest.CountingFS(root string) (fs.FS, *FSCounts)` returns a filesystem
that records opens and stats per path. It **implements `fs.StatFS`**: `fs.Stat`
falls back to opening the file on a filesystem that does not, which would make
the never-opened invariant fail against correct production code. One shared
helper means every later phase asserts "never opened" the same way.

### Assumption gate

D17 assumes a session file carries one identity. Measurement over the
maintainer's real corpus found none that carries more, and D23 records the
figure. Phase 0 re-runs that check as a repeatable command and records the
result, so the assumption rests on evidence rather than on symmetry with pi:

```
for f in $(find ~/.claude/projects -name '*.jsonl' -not -path '*/subagents/*'); do
  grep -o '"sessionId":"[^"]*"' "$f" | sort -u | wc -l
done | sort -u
```

A result other than a single line reading one refutes D17, and the phase stops
for a decision rather than proceeding.

### Baseline measurement

Add `BenchmarkSourceReaderDiscover` to `internal/vendors/anthropic`, driven by a
`jsonltest` corpus. Benchmarks do not run under the repository's test gate, so
this adds no wall-clock there, and it gives every later phase a repeatable
command rather than a remembered number:

```
go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime <benchmark-invocations>x
```

Record the figure in Implementation notes, together with the repository gate's
wall-clock band. Those two numbers are what `benchmark-regression-band` is
applied to for the rest of the worklog. No phase may quote the README's
measured baseline as an acceptance threshold.

### Files

| File                                             | Change                                                                       |
| -------------------------------------------------- | ------------------------------------------------------------------------------ |
| `main.go`                                         | `runProfiled` split out of `main`; `cpuProfileFileName` replaces the literal   |
| `main_profile_test.go`                            | New: the profiler tests                                                        |
| `internal/vendors/jsonltest/corpus.go`            | New: `CorpusOptions`, `WriteCorpus`, `WriteCorpusErr`, `Corpus`, `SessionFact` |
| `internal/vendors/jsonltest/countingfs.go`        | New: `CountingFS`, `FSCounts`; implements `fs.StatFS`                          |
| `internal/vendors/jsonltest/corpus_test.go`       | New: the corpus and counting-filesystem tests                                  |
| `internal/vendors/anthropic/bench_test.go`        | New: `BenchmarkSourceReaderDiscover`                                           |

## Integration contract

| Trigger                                            | Collaborators                 | Observable result                                  | Required side effects                            | Prohibited side effects                                   |
| ---------------------------------------------------- | ------------------------------- | ---------------------------------------------------- | -------------------------------------------------- | ----------------------------------------------------------- |
| `DEBUG_CPU` set, command run to completion         | real `run`, temp working dir   | Exit code equals the unprofiled exit code           | A gzip-decodable, non-empty profile file exists   | No change to command output; no network; no config write    |
| `DEBUG_CPU` set, profile file cannot be created    | working dir that is a file     | Command still runs and returns its normal exit code | A notice on stderr                                | No panic, no early exit                                     |
| `DEBUG_CPU` unset                                  | real `run`                     | No profile file is created                          | None                                              | No profiling overhead                                       |
| A corpus is written twice with the default seed    | two temp roots                 | Byte-identical files and identical facts            | None                                              | No dependence on wall-clock time                            |
| A counting filesystem serves a stat                | `jsonltest.CountingFS`         | The stat count rises, the open count does not       | None                                              | No file opened to answer a stat                             |

## Acceptance criteria

| Outcome                                                                   | Test or command                                                           |
| --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| A profiled run writes a non-empty, gzip-decodable profile                  | `TestRunProfiled_writesParseableProfile`                                    |
| A profiled run returns the same exit code as an unprofiled one             | `TestRunProfiled_exitCodeUnchanged`                                         |
| No profile file appears when the flag is unset                             | `TestRunProfiled_disabledWritesNothing`                                     |
| The corpus is byte-deterministic for a seed                                | `TestWriteCorpus_deterministicForSeed`                                      |
| The returned facts match the written bytes                                 | `TestWriteCorpus_factsMatchWrittenFiles`                                    |
| Every fixture kind in the table above is present in both shapes            | `TestWriteCorpus_containsRequiredFixtureKinds`                              |
| The counting filesystem answers a stat without an open                     | `TestCountingFS_statDoesNotOpen`                                            |
| The counting filesystem records per-path opens                             | `TestCountingFS_recordsOpensPerPath`                                        |
| The one-identity assumption still holds on the maintainer's corpus         | The assumption-gate command above, result recorded below                    |
| A discovery baseline exists and is reproducible                            | The benchmark command above, figure recorded below                          |
| The repository gate passes unedited, and its wall-clock band is recorded   | `go test ./... -race -cover -count=3 -timeout=30s`                           |

## Error coverage

| Failure                                                    | Expected outcome                                                    | Test                                          |
| ------------------------------------------------------------ | --------------------------------------------------------------------- | ----------------------------------------------- |
| The profile file cannot be created                         | Notice on stderr; the command still runs; exit code unchanged        | `TestRunProfiled_createFailureStillRuns`       |
| `pprof.StartCPUProfile` fails because profiling is active  | Notice on stderr; the command still runs; no partial file is left    | `TestRunProfiled_startFailureStillRuns`        |
| The corpus root's parent is a file, not a directory        | `WriteCorpusErr` returns an error naming the path; nothing is written | `TestWriteCorpusErr_rootNotADirectory`         |
| The corpus root cannot be written (parent is a file)       | `WriteCorpusErr` returns an error; no partial corpus is left behind  | `TestWriteCorpusErr_unwritableRootFails`       |
| A zero session count is requested                          | An empty corpus and empty facts; no panic                            | `TestWriteCorpusErr_zeroSessionsIsEmpty`       |
| A counting filesystem is asked for a missing path          | The error is returned unchanged and the miss is still counted        | `TestCountingFS_missingPathCounted`            |

## Implementation notes

**Session:** `2026-09-16`, clai (worklog-work, phase `0`).

### Deviations from the specification

**`Shape` needed a zero value.** The parameters table says `CorpusOptions.Shape`
has no default, which is only enforceable if the zero value is not a shape.
`ShapeUnset` is the zero value and `WriteCorpusErr` rejects it. Covered by an
added test, `TestWriteCorpusErr_invalidShape`.

**`SessionFact` gained a `Kind` field.** The acceptance row "every fixture kind
is present in both shapes" has no oracle otherwise: kinds are not derivable
from a path or a session id. `Kind` also lets a later phase select the one
fixture an invariant is about instead of rediscovering it.

**Every written file yields exactly one fact.** The specification's `Corpus`
implies facts only for sessions, but two required fixture kinds — the file with
no identity and the empty file — produce no session, and both are precisely
what later phases must assert about. `Files` and `Sessions` are therefore
index-aligned, and a fact with an empty `SessionID` is the oracle for "a
reader must drop this file".

**The specification contradicts itself about `Sessions`.** The README gives
`CorpusOptions.Sessions` a default of `64`, while this phase's error table
requires a zero session count to produce an empty corpus. Both cannot hold
through zero-substitution. Resolved as: `Sessions` is taken literally, so zero
means empty; `LinesPerSession` and `Seed` are zero-substituted to their
defaults, so later phases get the default seed by leaving the field alone;
`DefaultSessions`, `DefaultLinesPerSession` and `DefaultSeed` are exported
constants a caller names explicitly. `MinSessions` was added because a corpus
smaller than the required-kind count silently omits kinds.

**A working directory cannot be a file.** The integration contract provokes the
profile-create failure with "a working dir that is a file", which no process
can chdir into. `TestRunProfiled_createFailureStillRuns` instead places a
directory where `cpuProfileFileName` would go, which is the same `os.Create`
failure without the impossible precondition.

**Two error rows describe the same failure.** `TestWriteCorpusErr_rootNotADirectory`
and `TestWriteCorpusErr_unwritableRootFails` both read "parent is a file". They
were split so each covers a distinct path: the root itself is an existing file,
and the root's parent is a file. `os.Stat` under a file parent returns `ENOTDIR`
rather than `ENOENT`, so "nothing was written" is asserted as "the stat fails"
plus "the parent file is unmodified".

**A start failure leaves a file unless it is removed.** `os.Create` succeeds
before `pprof.StartCPUProfile` fails, so the "no partial file is left" outcome
needed an explicit `os.Remove`; it is not a property of the failure.

**Sidechains have no pi analogue.** `KindSidechainFile` and `KindSidechainLines`
are generated in both shapes so every kind can be indexed uniformly, but only
the Claude schema acts on the marker; the pi files carry it and count the lines
anyway. The kind-property assertions are scoped per shape for that reason.

### Verification

Assumption gate (D17, D23), run verbatim from this directory:

```
for f in $(find ~/.claude/projects -name '*.jsonl' -not -path '*/subagents/*'); do
  grep -o '"sessionId":"[^"]*"' "$f" | sort -u | wc -l
done | sort -u
```

Output was a single line reading `1`, over `365` files — the corpus grew by two
files since the README measurement, and the assumption still holds. D17 stands;
the phase proceeds.

Discovery baseline, the command later phases rerun:

```
go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x
```

Three consecutive runs on an `Intel Core Ultra 7 155H`, `NumCPU=22`, warm page
cache, against the default generated corpus (`64` files, `4191484` bytes):

| Run | ns/op      | B/op       | allocs/op |
| --- | ---------- | ---------- | --------- |
| `1` | `87752366` | `33044278` | `623968`  |
| `2` | `89515806` | `33044104` | `623968`  |
| `3` | `87772601` | `33043612` | `623967`  |

Reference figure: `89515806` ns/op, the slowest of three. Applying
`benchmark-regression-band`, a later phase's figure on this machine must stay
at or below `107418967` ns/op.

Repository gate, run unedited:

```
go test ./... -race -cover -count=3 -timeout=30s
```

Three runs at host load `2.1` to `3.3`: `29.3` s (cold build of the new
packages), `24.0` s, `24.3` s. The same gate with this phase's files removed ran
`25.1` s at load `2.1`, so the new tests add no measurable wall-clock; the band
to re-assert is `24` s to `25` s at load below `4`, and the README's `21` s
figure is a different host state, not a target.

Other gates, all clean: `gofumpt -l .`, `go vet ./...`, `staticcheck ./...`,
`go fix ./...`.

Coverage: `internal/vendors/jsonltest` at `93.9%` of statements;
`runProfiled` and `startCPUProfile` at `100%` each.

`dupl -t 80 .` reports one new clone group, `jsonltest.textContent` against
`vendors.TextBlocksContent`. It is deliberate and commented in place:
`jsonltest` imports no production package, so the packages it fixtures can
depend on it without an import cycle. CLAUDE.md points at a "Duplication
policy below" that the file does not contain, so this is judged rather than
ruled.

### Acceptance evidence

Every row below was proved by the run named, all under
`go test ./... -race -cover -count=3 -timeout=30s` unless stated otherwise.

| Criterion                              | Proof                                                                              |
| -------------------------------------- | ---------------------------------------------------------------------------------- |
| Non-empty, gzip-decodable profile      | `TestRunProfiled_writesParseableProfile` — opens the file, `gzip.NewReader`, asserts a non-empty body |
| Exit code unchanged                    | `TestRunProfiled_exitCodeUnchanged` — `version` and an unknown command, profiled and not; stdout compared too |
| Nothing written when the flag is unset | `TestRunProfiled_disabledWritesNothing` — the working directory is empty afterwards |
| Byte-determinism for a seed            | `TestWriteCorpus_deterministicForSeed` — two roots compared byte for byte, facts compared, and a different seed must differ |
| Facts match the written bytes          | `TestWriteCorpus_factsMatchWrittenFiles` — the facts are re-derived from the files by a second decode loop |
| Every fixture kind, both shapes        | `TestWriteCorpus_containsRequiredFixtureKinds` — presence plus the property each kind exists for |
| A stat opens nothing                   | `TestCountingFS_statDoesNotOpen` — stat count rises, total opens stay at zero |
| Per-path open counts                   | `TestCountingFS_recordsOpensPerPath`                                               |
| Profile-create failure                 | `TestRunProfiled_createFailureStillRuns`                                           |
| Profile-start failure                  | `TestRunProfiled_startFailureStillRuns` — also asserts no partial file             |
| Corpus root is a file                  | `TestWriteCorpusErr_rootNotADirectory` — the error names the path, the file is untouched |
| Corpus root under a file parent        | `TestWriteCorpusErr_unwritableRootFails`                                           |
| Zero sessions                          | `TestWriteCorpusErr_zeroSessionsIsEmpty` — empty corpus, empty root directory      |
| Missing path on the counting FS        | `TestCountingFS_missingPathCounted` — the not-exist error is returned unchanged and counted |
| One-identity assumption                | The assumption-gate command, above                                                 |
| A reproducible discovery baseline      | The benchmark command, above                                                       |
| The repository gate, unedited          | The gate command, above                                                            |

### Observations for later phases

**The fixtures agree with both real readers.** A temporary cross-check (written,
run, then deleted — it belongs to phase `1`) discovered the fixture-scale corpus
with `anthropic.SourceReader` and with `pi.SourceReader` and compared every row
against its `SessionFact`: session id, cwd, created, model, message count, full
first user text and the truncated preview all matched, and exactly the three
identity-less files were dropped. The oracle is therefore not merely
self-consistent.

**The line cap costs more than the README estimated, on this corpus.** At default
options the corpus holds `15699` messages; today's discovery reports `12159`,
and every one of the `61` discoverable rows undercounts. That is the field
phase `3` makes exact.

**Later phases cannot read this file.** The reading contract gives a phase the
README and its own phase file only, so the baseline figures above were promoted
to a README subsection under Budgets.

## Review findings

None.
