# Phase 3: ffmpeg split, parallel transcription, stitch

**Status:** Complete
[← README](./README.md)

## Goal

Transparently handle files over the 25 MB request cap by best-effort ffmpeg chunking, bounded-parallel transcription of chunks, and local timestamp-offset stitching.

## Specification

`internal/audio/split.go` (+ tests), orchestration layer above the phase-2 transcriber:

- Size gate: files ≤ 25 MB (constant, config-overridable later) transcribe as a single request — the split path must be provably skipped.
- Over the cap: locate `ffmpeg` **and** `ffprobe` on `PATH` through an **injected command-runner interface** (pattern: constructor takes a `runner` func/interface; production wires `exec.CommandContext`). Both are hard requirements for the split path — either missing yields the same actionable error; sub-cap files need neither.
- Total duration comes from `ffprobe` on the input file; `segment_time = duration / ceil(size / 20 MB)` (fixed-duration chunks targeting ~20 MB each). Split via `-f segment -segment_time <s> -c copy` into a temp dir under `os.MkdirTemp`.
- Chunk start offsets derive from the split plan (`i × segment_time`); optionally verified against `ffprobe` per-chunk durations, with a stderr warning on drift > 1 s.
- Chunks transcribe via a bounded worker pool: `parallelism` from config (default 3), results indexed by chunk, `Offset()` applied, concatenated in order. First error cancels the rest through the existing ctx-cancel machinery.
- stderr UX (via `ancli`): pre-flight notice `<file> is <size> (> 25 MB limit) → splitting via ffmpeg: N chunks × ~M min`; per-chunk completion lines; all on stderr only.
- Diarize + split ⇒ stderr warning about speaker-label drift across chunks (README trade-off).
- Temp chunk files removed on completion and on error/cancel (defer-based).

## Integration contract

| Scenario | Collaborators | Observable result | Required side effects | Prohibited side effects |
| -------- | ------------- | ----------------- | --------------------- | ----------------------- |
| 10 MB file | fake runner (must not be called), fake transcriber | single transcription call, unmodified timestamps | — | no ffmpeg/ffprobe invocation, no temp dir |
| 60 MB file, ffmpeg+ffprobe present | fake runner serving ffprobe duration and producing 3 chunk files, fake transcriber returning per-chunk segments | stitched `[]Segment` with offsets 0 / T / 2T, in order | stderr split notice; temp dir removed | chunk results out of order; anything on stdout |
| 60 MB file, parallelism 2 | fake transcriber recording concurrency | max 2 in-flight transcriptions observed | — | unbounded concurrency |
| chunk 2 of 3 fails | fake transcriber erroring on index 1 | error returned; ctx cancelled for remaining chunks | temp dir removed | partial transcript returned as success |
| diarize model + oversized file | fakes as above | stitched result | stderr drift warning | — |

## Acceptance criteria

- [x] All five contract rows proven with the fake runner/transcriber (no real ffmpeg in tests) — race-clean under `-race -count=3`:
  - 10 MB bypass: `TestSplitter_SubCapBypassesSplit`
  - 60 MB split/stitch (offsets 0/600 s/1200 s, in order, stderr notice, temp dir removed, stdout empty via pipe capture): `TestSplitter_OversizedSplitsAndStitches`
  - parallelism 2 bound: `TestSplitter_ParallelismBounded` (atomic max-in-flight ≤ 2)
  - chunk 2 of 3 fails: `TestSplitter_ChunkFailureCancelsSiblings` (first error propagated, siblings observe ctx cancel, temp dir removed)
  - diarize + split warning: `TestSplitter_DiarizeWarning` (+ `TestSplitter_NoDiarizeWarningForPlainModel` negative)
- [x] Offset math: a segment at chunk-local 00:00:05 in chunk 3 (600 s chunks) renders at 00:20:05 in final VTT. — `TestSplitter_OversizedSplitsAndStitches` asserts `00:20:05.000` in rendered VTT
- [x] Sub-cap files bypass splitting entirely (fake runner asserts zero calls). — `TestSplitter_SubCapBypassesSplit`
- [x] Coverage ≥ 80 %. — package `internal/audio` at 93.2 % with the splitter included

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| Oversized file, ffmpeg or ffprobe not on PATH | actionable error naming the missing binary and a manual split command; no request sent | `TestSplitter_MissingBinary` (both binaries, asserts `ffmpeg -i` hint + zero transcriber calls) |
| ffmpeg/ffprobe exits non-zero | error including stderr tail; temp dir cleaned | `TestSplitter_FfmpegFails` |
| ffmpeg produces zero chunks | explicit error (not empty-success) | `TestSplitter_ZeroChunks` |
| transcription error mid-pool | first error propagated, siblings cancelled, cleanup ran | `TestSplitter_ChunkFailureCancelsSiblings` |
| ctx cancelled during split | prompt abort, cleanup ran | `TestSplitter_CtxCancelledDuringSplit` (`errors.Is(err, context.Canceled)`, <2 s) |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `internal/audio/split.go`, split tests appended in `internal/audio/split_test.go`.

Deltas from spec:

- **Status output goes to an injected `io.Writer` (default `os.Stderr`), not through ancli print functions.** ancli's notice/warn/ok printers write to *stdout* (and under `SetupSlog` everything below error level routes to stdout via the slog handler), which would break the pipes-stay-clean invariant. The splitter uses `ancli.ColoredMessage` for formatting (gated on `ancli.UseColor`) but writes lines itself. The injected writer also makes stderr UX assertable in tests.
- **`NewSplitter(transcriber, runner)` constructor** with exported tuning fields (`Parallelism`, `MaxBytes`, `Model`, `StatusOut`); `ExecRunner` is the production `CommandRunner` (covered by `TestExecRunner` against real `sh`).
- **Drift verification warns once** (first chunk over 1 s drift) rather than per chunk, and skips silently if a chunk probe fails — verification is best-effort per spec. `TestSplitter_ChunkDriftWarning` proves the warning path.
- **Sparse files** (`f.Truncate(60<<20)`) provide oversized inputs in tests without writing real megabytes.
- Phase-1 package doc ("pure functions only") updated: the split orchestration now lives in `internal/audio` per this phase's spec; HTTP remains excluded.

Verification (all green):

- `go test ./internal/audio/ -race -cover -count=3 -timeout=30s` → ok, **93.2 %** coverage
- `make qa` → exit 0; dupl reports zero clones in `internal/audio`

## Review findings (review 1, 2026-08-26)

Verified good (traced through every branch, not from the notes):

- All five integration-contract rows and five error rows re-read in
  `split_test.go`; the fakes are honest (atomic max-in-flight for the
  parallelism bound, timing-bounded sibling-cancel proof, stdout captured via
  `os.Pipe` and asserted empty, temp-dir removal stat-asserted on success,
  chunk-failure, and ffmpeg-failure paths).
- Temp-dir invariant holds on every path: `defer os.RemoveAll(tempDir)` is
  installed immediately after `MkdirTemp` at `split.go:147`, before any branch
  can return.
- ctx-cancel machinery holds: `Run` gets the pool/parent ctx everywhere,
  workers blocked on the semaphore observe `poolCtx.Done()`, first error wins
  under `errMu`, and external cancellation with no chunk error is still
  surfaced via the `ctx.Err()` check at `split.go:267`.
- Offset math verified: `i × segmentTime` through `secondsToDuration` (ms
  rounding) matches the phase-1 fixture precision; in-order stitch guaranteed
  by index-addressed `results[i]`.

Findings:

- [ ] **R1-02 (Minor, logged — does not reopen)** — inputs needing ≥1000
  chunks stitch out of order silently. The pattern `chunk_%03d`
  (`split.go:176`) grows to 4 digits at chunk 1000 and `filepath.Glob`'s
  lexical sort (`split.go:190`) orders `chunk_1000` before `chunk_999`; both
  segment order and the `i × segmentTime` offsets then misalign. Requires a
  ~20 GB input at the 20 MB chunk target, so practically unreachable for the
  meeting-recording use case — but the failure would be silent. Cheap
  hardening if ever touched: widen the pattern (`%06d`) or sort numerically
  after globbing.
