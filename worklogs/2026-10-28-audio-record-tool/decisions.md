# Decision log — audio-record-tool

- 2026-10-28 — worker: ChatGPT — session: phase-1 attempt
  - Decision: Introduced `internal/capture` package with a minimal recorder implementation and a shell-based `fake_ffmpeg.sh` fixture to test process orchestration without requiring real audio hardware.
  - Rationale: The worklog explicitly calls out swapping `PATH` to a fixture script (README Phase 1 L6) so contract tests can exercise argv building, stderr parsing, and interrupt handling deterministically.

- 2026-10-28 — worker: ChatGPT — session: phase-1 completion
  - Decision: Unskipped and fixed the AC6 path (`TestRecord_FFmpegEnds`) so an ffmpeg process that exits on its own reports `StopError` and returns an error containing ffmpeg’s last stderr line.
  - Rationale: AC6 / F6 requires distinguishing "no audio" from "ffmpeg failed/ended early"; returning `ErrNoAudio` here hid the real failure mode.
  - Decision: In stop-driven exits (silence/max/enter/interrupt), explicitly reap the ffmpeg process (wait for `cmd.Wait`) to ensure stderr closes and tests don’t hang.
  - Rationale: Avoids leaking goroutines and ensures stderr parsing completes before we decide which error to return.

- 2026-10-28 — worker: ChatGPT — session: phase-2 implementation
  - Decision: Gate `audio_sources` registration on `ffmpeg` being present on PATH (tool is absent when `exec.LookPath("ffmpeg")` fails).
  - Rationale: README invariant F8 requires both audio tools to be registered exactly when `ffmpeg` resolves; this matches the existing tool registry’s executable-gating pattern.
  - Decision: Wire the tool engine via `internal/audio` package `init`, mirroring `audio_transcribe`’s mode-as-tool bridge.
  - Rationale: Keeps `pkg/tools` free of engine dependencies and avoids per-run wiring.

- 2026-10-28 — worker: Claude Code — session: phase-3 completion
  - Decision: Repaired `internal/capture` while completing phase 3 instead of layering the tool on the existing first cut.
  - Rationale: The start gate failed in phase 1: `cmd.Wait` ran twice for one process, `exec.CommandContext` received `context.Background()`, `lastStderr` was read without a happens-before edge, and `fileLooksLikeWAV` read the whole recording. F3, F4 and F5 cannot hold on that base.
  - Decision: The recorder owns the stop handshake: one `cmd.Wait` goroutine, stderr through `os.Pipe`, `Cmd.Cancel` sends `SIGINT`, `WaitDelay` is 2 s, and the `.part` file is renamed only after the process exits. The RIFF check reads 4 bytes.
  - Rationale: F4 requires finalization on every ending, including caller cancellation, without unbounded memory and without a lost file.
  - Decision: `silencedetect` events are read while ffmpeg runs; a `silence_start` at or below zero is the quiet start of the stream, a later `silence_start` proves sound was heard, and the scanner ignores events after a stop was requested.
  - Rationale: F3 arms the silence timer only after sound. The flush event on a silent stream would otherwise mark sound and hide `ErrNoAudio`.
  - Decision: `recordText` loads the merged `capture` section with `LoadConfigFromFileCollect`, not `LoadConfigFromFile`.
  - Rationale: F9 forbids stdout writes; the announce variant prints the config-upgrade message there.
  - Decision: Tool arguments of an integer field must decode to an integral `float64`; a fractional JSON number is refused by field name.
  - Rationale: The schema says integer, and a silent `int(1.5)` truncation hides a model mistake.

- 2026-10-28 — worker: Claude Code — session: phase-4 completion
  - Decision: The profile contract tests live where the concern lives: catalog resolution in `internal/tools` (with an injected always-found executable lookup, so the result does not depend on the host's binaries), prompt clauses in `internal/text` (next to the `Profile` type and the shared example validator), and the profile-driven run and documentation guard in `e2e`.
  - Rationale: The validity check must not become flaky when a host lacks `rg` or `cat`, and the prompt check must run without starting the CLI.
  - Decision: The e2e test swaps in a fresh registry with `tools.WithTestRegistry` after putting phase 1's fixture on `PATH`, then calls `tools.Init`, instead of skipping when an earlier test already initialised the registry without `ffmpeg` (phase 4 L3).
  - Rationale: The availability gate is proven by `TestRegisterLocalTools_AudioRecordGate`; AC 6 needs the composition to run on every host. A skip would make the acceptance criterion conditional on test order.
  - Decision: The e2e test points `os.Stdin` at an empty regular file for the run. (Superseded by R3-01: it points at `/dev/null` now, and `enterStop` ignores EOF.)
  - Rationale: `utils.IsTerminalWriter` treats any character device as a terminal, and `/dev/null` is one. Under `go test` the record tool therefore armed the Enter stop, read EOF and interrupted `ffmpeg` before the WAV existed. An empty regular file is not a terminal and is already at EOF, so the silence timeout ends the recording.
  - Decision: The ordering assertion reads stdout only.
  - Rationale: A piped stdout selects `rawDisplay`, which prints every tool result and the final reply to stdout in call order; the record tool's one status line goes to stderr as F9 requires.

- 2026-10-28 — worker: Claude Code — session: root review (R1)
  - Decision: `Record` now owns one interrupt per ffmpeg through `interruptOnce`, a CAS guard shared by `exec.Cmd.Cancel` and `requestStop`, instead of letting both send `SIGINT`.
  - Rationale: `os/exec`'s context watcher calls `Cancel` when the call context ends, and the recorder also interrupted the process itself. Real ffmpeg 8.0.1 exits before it finalizes the WAV on the second `SIGINT` ("Immediate exit requested"). Measured with the real binary behind the phase-1 fixture seam: 10 of 60 cancellations lost the file and the saved path. After the guard: 60 of 60 saved. R1-01.
  - Decision: `TestInterruptOnce` proves the guard against a real child process rather than a signal-counting Go helper.
  - Rationale: a Go helper coalesces signals that arrive microseconds apart (measured: 58 deliveries for 200 back-to-back signals), so it cannot witness the recorder's duplicate. The test sends the two signals itself 100 ms apart, which the child can count reliably, and it fails on the unguarded code ("child received 2 interrupts").
  - Decision: `TestSourcesText` drives `capture.New()` over the phase-1 fixture and the local `stubRecorder` is deleted.
  - Rationale: the phase-2 test row names an integration scenario over the real recorder; the stub proved only the renderer and left the recorder's error path to a sentinel. R1-03.
  - Decision: the phase-1 interface comment now says the shared gate is the `ffmpeg` lookup, and `Supported` answers the platform's half.
  - Rationale: no production path called `Supported`, while the comment and the Imports table claimed it gated both tools. F8 and the two registration-gate tests make the `ffmpeg` lookup the gate. R1-06.
  - Decision: `architecture/audio.md` describes the `.part` file as the WAV it is (`-f wav`) and states the one-interrupt rule.
  - Rationale: the document said raw `pcm_s16le`, which the pre-plan measurement used before the recorder wrote a container. R1-05.
  - Decision: the root review does not claim `done`; it sets `Line: waiting on human: root`.
  - Rationale: the nine README human checks need audio hardware and a listener. R1-07.

- 2026-10-28 — worker: Claude Code — session: root review (R2, holistic)
  - Decision: the capture process is started with `exec.Command`, without the caller's context, and the recorder is the only sender of `SIGINT`.
  - Rationale: a terminal SIGINT reaches every process of the foreground group, and `exec` gives the child no new group, so ffmpeg already receives one; `exec.CommandContext` sent a second when clai cancelled the run. Measured with real ffmpeg 8.0.1 over `pipewire-pulse`: 20 of 20 recordings lost with both signals, 0 of 20 with the terminal's alone. R2-01.
  - Decision: a caller cancellation waits `terminalSignalGrace` (1 s) for an interrupt that already arrived, then sends its own only if the process still runs; a process that ends as the cancellation arrives waits `cancelArrivalGrace` (250 ms) so it is reported as interrupted, not as an ffmpeg failure.
  - Rationale: ffmpeg needed 293–374 ms to finalize after the terminal's SIGINT over 20 runs, and clai's cancellation arrived before it exited in 20 of 20. A programmatic cancellation pays the second, which is the only one without a terminal signal; Enter, silence and max-seconds stops still signal at once. R2-01.
  - Decision: `interruptOnce` and `TestInterruptOnce` are removed; the property is now structural (one sender) and pinned by `TestStartFFmpeg_LeavesInterruptingToTheRecorder` and `TestRequestStop`.
  - Rationale: the guard shared two senders that no longer exist. A shell fixture cannot witness near-simultaneous SIGINTs, so the test pins the absence of `Cmd.Cancel` and the two branches of the one sender, and the real-ffmpeg measurement in the phase records the outcome. R2-01.
  - Decision: the finalized WAV's samples decide `ErrNoAudio` (`wavHasSound`), not the `silencedetect` events.
  - Rationale: `silencedetect` reports transitions only; a source that never pauses emits no event, so a video, podcast or noisy room was deleted as silent. The probe walks the RIFF chunks (ffmpeg writes `LIST INFO` before `data`), scans with a 32 KiB buffer and stops at the first sample above `10^(peak-dB/20) · 32768`. R2-02.
  - Decision: the fixture writes a real two-sample WAV (tone or zeros) and gains a `continuous` mode with no silence events.
  - Rationale: the old zero-length data chunk could not prove either side of the sample probe, and the continuous mode is the regression for R2-02.
  - Decision: `ErrUnsupported` names the platforms it needs.
  - Rationale: F8 asks an error to name the action. R2-03.

- 2026-10-28 — worker: clai (worklog worker) — session: root review (R3, holistic)
  - Decision: `enterStop` closes the stop channel only when `bufio.Reader.ReadString` returns a completed line; EOF and read errors leave the channel open.
  - Rationale: `/dev/null` is a character device, so `utils.IsTerminalWriter(os.Stdin)` reports a terminal for a scripted or harnessed run, and closing on EOF interrupted ffmpeg before it wrote the WAV. Measured through the shipped tool with stdin `/dev/null`: 0.44 s and `ffmpeg produced no readable wav: open …wav.part: no such file or directory`; after the fix the same command recorded 2 s and returned the six facts. The phase-4 e2e's empty-regular-file stdin had worked around exactly this user-facing case. R3-01.
  - Decision: phase 4's e2e now points `os.Stdin` at `/dev/null`, and phase 3's fast unit checks use the recorder stub; the recorder's contract tests stay with the fixture.
  - Rationale: the e2e becomes the composition proof for the EOF rule, and `internal/audio` stays well below the gate's 30 s budget (13.6 s at `-count=3`).
  - Decision: `TestRecord_SelectedSources` records two sources over the fixture (with an argument log) and `TestFilterForSources` pins the `amix` strings.
  - Rationale: F2's selection, F5's equal-weight mix and the `devices` order had no test; every existing request selected one mic. Coverage of `internal/capture` rose from 82.1 % to 85.4 %. R3-02.
  - Decision: `Request.MaxDuration` documents zero as disabling the duration stop, and `architecture/audio.md` says so and describes the completed-line stop rule.
  - Rationale: the code already behaved this way; a file with `capture.max-seconds: 0` otherwise looked like an unconditional hard stop. R3-03 also corrected phase 4 L3, which still claimed the e2e skips.

- 2026-10-28 — worker: clai (worklog worker) — session: root review (R4, round 4)
  - Decision: the line stops at `A01`; the root review does not claim `done`.
  - Rationale: `go test ./... -race -cover -count=3 -timeout=30s` failed in `internal/audio` on `TestRecordText_RecorderError/cancelled call` in one run on a loaded host, and the same run passes when the host is idle. A gate whose result changes between runs is a quality andon.
  - Decision: A01 records both halves, the code and the test.
  - Rationale: `Record` calls `sources` with the call context, so a cancellation during the two listing subprocesses returns `ffmpeg -sinks pulse failed: signal: killed: ` with an empty tail (proved with a discovery slowed to 300 ms); the test cancels on a fixed 100 ms timer, which is what makes the gate result depend on host load. Fixing only the test would leave the misleading error; fixing only the code would leave the timing hazard.
  - Decision: R4-01 records the phase-1 Interface drift instead of editing the block, and R4-02 records F10's unproven note-file claim instead of narrowing it.
  - Rationale: review records findings; [[worklog-fix]] makes the edits, so the next session sees what changed and why.

- 2026-10-28 — worker: clai (worklog fix session)
  - Decision: A01's code half lives in `capture`: `discoveryCancellation` returns the caller's cancellation when exec stopped a listing child, and `childFailure` appends stderr only when the child wrote some.
  - Rationale: the caller must be able to branch on the cancellation, and the empty `: ` tail was the visible symptom of formatting stderr unconditionally. Both platform halves changed, because darwin had the same defect.
  - Decision: both cancellation tests cancel on the `.part` file instead of a timer, and `TestRecord_CancelDuringDiscovery` uses the fixture's `slow` discovery mode.
  - Rationale: the `.part` file is the observable that discovery finished; a timer is what let the test cancel a call setup under host load. The slow mode makes the discovery-cancellation path deterministic on linux and darwin.
  - Decision: R4-01 and R4-02 are applied as the review rows asked; no interface or clause changed beyond matching the shipped text.
  - Rationale: the block now matches the shipped error message and the directory rule, and F10 claims only what the e2e proves.
  - Decision: the implementation follows the testenv refactor: `testenv.MainInHome` and `e2etest.BlankDebugAndVendorKeys` no longer exist, so the audio and capture binaries use `testenv.Main` and the profile e2e drops the blanking call.
  - Rationale: `testenv.Main` now isolates HOME and blanks vendor keys for every binary; the removed helpers were the only lint failures.
