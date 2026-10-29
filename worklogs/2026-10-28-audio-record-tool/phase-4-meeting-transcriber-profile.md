# Phase 4 — meeting-transcriber profile

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

Ship one profile JSON that turns `audio_sources`, `audio_record` and
`audio_transcribe` into a meeting workflow a user runs with a single query,
and prove it end to end.

## Interface

Written to `examples/profiles/meeting-transcriber.json`.

```json
{
  "description": "Records a meeting on this machine, transcribes it with speaker labels, and files the notes where you tell it. Run it in a terminal: it inspects the audio inputs, then records until Enter, a silence timeout, or its maximum duration. Needs the audio_sources and audio_record tools (linux or macOS with ffmpeg); note tools come from your own MCP servers.",
  "model": "gpt-6.1-sol",
  "use_tools": true,
  "tools": [
    "audio_sources",
    "audio_record",
    "audio_transcribe",
    "date",
    "cat",
    "ls",
    "rg",
    "rows_between",
    "write_file",
    "mkdir",
    "mcp_slivingdoc*"
  ],
  "prompt": "<the workflow, per the clauses below>"
}
```

Clauses of the shipped file:

- profile.1 — Every entry of `tools` is a built-in registered by
  `internal/tools` or an MCP glob of the form `mcp_<server>*`; the file names
  no tool that the catalog does not provide. (F8, F10)
- profile.2 — The prompt tells the model to call `audio_sources` first and to
  use its device names when the user names a device, and its default markers
  otherwise. (F2, F11)
- profile.3 — The prompt tells the model to choose `sinks` and `mic` from the
  user's description: a room meeting sets `mic: true` and `sinks: "none"`, a
  call sets `mic: true` and `sinks: "default"`, a video or podcast sets
  `mic: false` and `sinks: "default"`. (F2, F10)
- profile.4 — The prompt tells the model to pass a short `name_suffix` taken
  from the user's context, to announce that Enter stops the recording, and to
  report the recording facts it received. (F6, F10)
- profile.5 — The prompt tells the model to transcribe the returned path with
  `audio_transcribe` and `output_format` `json`, to save the complete tool
  result byte-exact, and only then to render a readable transcript and the
  notes. (F10)
- profile.6 — The prompt contains no organization name, note directory, index
  rule or notebook service; the user's message supplies those, and any note
  tool comes from the MCP servers the user configures. (F10)
- profile.7 — The prompt tells the model to stop and say so when
  `audio_record` is not among its tools, naming `ffmpeg` and this platform as
  the requirement, rather than substituting a shell command. (F8, F10)
- profile.8 — The prompt tells the model to relay a backend error as it is and
  to stop, and to carry on with the microphone alone when `audio_sources`
  reports `system_audio: unavailable`. (F8, F11)
- profile.9 — The file passes `remote.Validate` and loads through
  `-profile-path` from the repository checkout. (F10)

Consumes: `tools:AudioSources` (phase 2); `tools:AudioRecord` (phase 3);
`pkg/tools:AudioTranscribe` (import); `internal/tools:Registry` (import);
`internal/remote:Validate` (import); `e2e/e2etest:Harness`,
`SetupMainTestConfigDir` (import); the phase 1 fixture
`internal/capture/testdata/fake_ffmpeg.sh` (recorded fixture).

## Acceptance criteria

| #   | Criterion                                                                                                                          | Proves                                                                | Proof                                       |
| --- | ---------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- | ------------------------------------------- |
| 1   | The shipped profile is valid JSON, passes `remote.Validate`, and names only tools the catalog provides                             | `profile.1`, `profile.9`, `F10`                                       | `Test_meeting_transcriber_profile_is_valid` |
| 2   | The prompt carries the inspect-first step, the source-selection mapping, the suffix, the Enter notice and the stop-on-absence rule | `profile.2`, `profile.3`, `profile.4`, `profile.7`, `F2`, `F8`, `F11` | `Test_meeting_transcriber_profile_prompt`   |
| 3   | The prompt requires a JSON transcription, a byte-exact raw save, then the rendered notes                                           | `profile.5`, `F10`                                                    | `Test_meeting_transcriber_profile_prompt`   |
| 4   | The profile contains no organization, directory or index rule                                                                      | `profile.6`, `F10`                                                    | `Test_meeting_transcriber_profile_prompt`   |
| 5   | The prompt says what to do when the backend is broken or system audio is missing                                                   | `profile.8`, `F8`, `F11`                                              | `Test_meeting_transcriber_profile_prompt`   |
| 6   | A mock-model run of the profile inspects, records and transcribes, in that order                                                   | `profile.9`, `F10`                                                    | `Test_e2e_meeting_transcriber_profile`      |
| 7   | The shipped example is documented where users look for profiles                                                                    | `F10`                                                                 | `Test_meeting_transcriber_documented`       |

## Test cases

| Test                                        | Level                | Proves                                                    | Given                                                                                                                                   | Then                                                                                                                                                                                                                                                 | Must not                                        |
| ------------------------------------------- | -------------------- | --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `Test_meeting_transcriber_profile_is_valid` | contract suite       | `profile.1`, `profile.9`, `F8`, `F10`, AC 1               | The file's bytes, `remote.Validate`, `internal/tools.Registry`                                                                          | Validation passes; every non-MCP tool name resolves in the registry; every MCP entry starts with `mcp_`                                                                                                                                              | Start a query                                   |
| `Test_meeting_transcriber_profile_prompt`   | contract suite       | `profile.2`–`profile.8`, `F2`, `F8`, `F10`, `F11`, AC 2–5 | The decoded prompt                                                                                                                      | Contains the three source selections, the `audio_sources` step, the `name_suffix` instruction, the Enter notice, the JSON transcription order, the missing-tool rule and the unavailable-system-audio rule, and no organization or directory literal | Encode a notebook path                          |
| `Test_e2e_meeting_transcriber_profile`      | integration scenario | `profile.9`, `F10`, AC 6                                  | Harness with phase 1's fixture on `PATH` and `-cm mock_test -prp <file> q "tool_audio_sources tool_audio_record tool_audio_transcribe"` | Status 0; the device list, the recording facts and the mock transcript all appear, in that order                                                                                                                                                     | Record from a real device, or reach the network |
| `Test_meeting_transcriber_documented`       | unit                 | `F10`, AC 7                                               | `examples/profiles/README.md` and `examples.md`                                                                                         | Both name `meeting-transcriber` and its required tools                                                                                                                                                                                               | Assert prose beyond the name                    |

## Error management

| Failure                                    | Expected outcome                                                                       | Test                                        |
| ------------------------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------- |
| The profile names a tool the catalog lacks | The validity test fails and names the entry                                            | `Test_meeting_transcriber_profile_is_valid` |
| The profile fails `remote.Validate`        | The validity test fails with the validator's problems                                  | `Test_meeting_transcriber_profile_is_valid` |
| A prompt clause is edited away             | The prompt test fails naming the missing clause                                        | `Test_meeting_transcriber_profile_prompt`   |
| `audio_record` is unavailable at run time  | The prompt's stop rule is the model's instruction; no test asserts a model's obedience | `Test_meeting_transcriber_profile_prompt`   |
| The backend is unusable                    | The prompt tells the model to relay the error verbatim and stop                        | `Test_meeting_transcriber_profile_prompt`   |
| The machine has no system audio            | The prompt tells the model to record the microphone alone                              | `Test_meeting_transcriber_profile_prompt`   |

## Golden diff

| Golden                                                                 | Change |
| ---------------------------------------------------------------------- | ------ |
| `examples/profiles/meeting-transcriber.json`                           | new    |
| `Test_e2e_meeting_transcriber_profile` observed call order and results | new    |
| `examples.md` capture-and-transcribe example                           | new    |

## Removes

None.

## Local facts

**L1 — Prompt shape.** The prompt names the six facts `audio_record` returns,
the `audio_sources` inspection step, the three source selections, the
`audio_transcribe` call with `output_format: "json"`, the order transcribe →
save raw → render → notes, the stop rule for a missing tool and the recovery
rules for a broken backend and for missing system audio. It asks the user's
message for the organization, the attendees, the context and the note
destination.

**L2 — Documentation.** This phase edits `examples.md` (one example and one
sentence), `examples/profiles/README.md` (a row for the new profile) and adds a
paragraph to `architecture/audio.md` that points at the shipped profile as the
worked example of the two tools.

**L3 — Test isolation.** The end-to-end test puts phase 1's fixture on `PATH`
with `t.Setenv`, sets `CLAI_MOCK_AUDIO_TRANSCRIBE_FILE` so no network call is
made, and points `capture.dir` at the test's own temporary directory through
the `audioConfig.json` of an `e2etest.SetupMainTestConfigDir` home. It calls
`tools.Init()` after the `PATH` swap inside `tools.WithTestRegistry`, so a
fresh registry observes the fixture and the run never depends on which test
initialised the process-global registry first. Its stdin is `/dev/null`, a
character device that `utils.IsTerminalWriter` reports as a terminal, so the
run also proves that end of input is not read as an Enter press (R3-01). The
mock model calls the tools named by `tool_*` tokens in the query, in token
order, so the token order is the asserted call order.

## Human required

Publishing the profile to the Hub, and running it against a real model on real
meeting audio, are human actions. The README's human-check table records the
second one; no phase performs either.

## Implementation notes

Implemented 2026-10-28 (worker: Claude Code, phase-4 session).

`examples/profiles/meeting-transcriber.json` ships the interface's model,
tool list and prompt. The prompt carries `profile.2`–`profile.8`: the
`audio_sources` step, the three role selections, the device-name rule, the
`name_suffix`, the Enter notice, the six result facts, the JSON transcription
order, the missing-tool rule, and the broken-backend and unavailable-system-
audio rules.

The contract tests are split by what they own:

- `internal/tools/meeting_profile_test.go` registers a catalog with an
  always-found executable lookup, so the test does not depend on which
  binaries this host has, then proves every non-MCP tool name resolves and
  every MCP entry is an `mcp_<server>*` glob.
- `internal/text/meeting_profile_test.go` pins every prompt clause and the
  inspect → record → transcribe-json → save-raw → render → notes order. It
  refuses a slash (a directory literal) and a notebook-service name in the
  prompt, which is how `profile.6` stays enforced.
- `e2e/profile_meeting_transcriber_e2e_test.go` runs the shipped file
  through `-prp` and the mock model and asserts the device list, the
  recording facts and the transcript, in that order. It also guards the two
  documentation places.

Two test-harness facts are not obvious and are why the test differs from
L3's sketch:

1. `internal/tools.Init` fills the process-global registry exactly once, and
   an earlier test in the `e2e` binary may already have done so without
   `ffmpeg`. Instead of skipping when the audio tools are absent, the test
   swaps in a fresh registry with `tools.WithTestRegistry`, puts the fixture
   on `PATH` and calls `tools.Init`; AC 6 then runs on every host with a
   shell and needs no skip.
2. `/dev/null` is a character device, so `utils.IsTerminalWriter(os.Stdin)`
   reports a terminal. The test points `os.Stdin` at `/dev/null` on purpose:
   the record tool must keep recording when stdin is already at EOF
   (R3-01), so the silence timeout ends the recording and the six facts
   appear. The recorded file stays in a test-owned directory.

`examples.md` gains one capture-and-transcribe example and
`examples/profiles/README.md` one row naming the three tools;
`architecture/audio.md` already pointed at the profile as the worked example
(phase 3 session).

### Test results

- `go test ./internal/tools -run Test_meeting_transcriber_profile_is_valid -race -count=1 -timeout=60s` — pass
- `go test ./internal/text -run 'Test_meeting_transcriber_profile_prompt|TestExampleProfiles' -race -count=1 -timeout=60s` — pass
- `go test ./e2e -run 'Test_e2e_meeting_transcriber_profile|Test_meeting_transcriber_documented' -race -count=3 -timeout=30s` — pass (4.5 s)
- `go test ./e2e ./e2e/tooling ./e2e/conversation -race -count=1 -timeout=60s` — pass
- `go test ./internal/tools ./internal/text ./internal/audio ./internal/capture ./pkg/tools -race -count=3 -timeout=30s` — pass
- `go test ./e2e -race -count=3 -timeout=180s` — pass (56.9 s); the gate's 30 s budget is not reachable for this package on this host, and the same timeout occurs with `-skip meeting_transcriber`
- `go test ./... -race -cover -count=3 -timeout=30s` — every other package passes; the touched packages report `internal/tools` 84.4 %, `internal/text` 87.6 %, `internal/audio` 88.9 % and `internal/capture` 81.7 % coverage. `e2e` times out at 30 s (it needs 56.9 s for `-count=3` even with `-skip meeting_transcriber`) and `internal/summary` fails `TestNewAgentSummarizer_loadsConfig`, which the phase-3 journal already records as reproducing at HEAD
- `go vet ./internal/tools ./internal/text ./e2e` — pass
- `staticcheck ./internal/tools ./internal/text ./e2e` — pass
- `gofumpt -l .` — no output; `go fix ./...` — no change; `dupl -t 80` — no clone involves the new files

No handover is needed; the phase is complete.

## Review findings

### Review 1, 2026-10-28 — root review

**Status: Complete — no finding in this phase.**

**Verified good.**

- **The shipped profile is valid and catalog-complete.**
  `remote.Validate` passes and every non-MCP tool name resolves in a registry
  whose executable lookup always succeeds, so the test does not depend on the
  host's binaries (`internal/tools/meeting_profile_test.go:14-56`).
- **Every prompt clause is pinned** (`internal/text/meeting_profile_test.go`):
  the `audio_sources` step, the three role selections, the device-name rule,
  `name_suffix`, the Enter notice, the six facts, the JSON → raw → render →
  notes order, the missing-tool rule, the broken-backend rule and the
  unavailable-system-audio rule. The prompt carries no `/`, no notebook
  service and no index rule.
- **The composed run works.** `Test_e2e_meeting_transcriber_profile` drives
  the shipped file through `-prp` and the mock model; the device list, the
  recording facts and the transcript appear in that order, and exactly one
  WAV lands in the configured directory. The test now runs on every host with
  `sh` because it swaps in a fresh registry instead of skipping
  (`e2e/profile_meeting_transcriber_e2e_test.go:20-100`).
- **The documentation guard** keeps the profile named, with its three tools,
  in `examples/profiles/README.md` and `examples.md`
  (`Test_meeting_transcriber_documented`).
- **The end-to-end package passed twice** with `-count=3 -timeout=180s`
  (59.8 s and 62.7 s), so the scenario is not order- or timing-flaky.

**Findings**

None.

**Notes.**

- F10's note-file claim was out of the e2e's reach: the mock model scripts
  inputs per tool and cannot feed a tool result into a later tool's input, so
  a scripted `write_file` could only prove that some file is written, not that
  the transcript is. F10 now claims what the run proves: the three tools are
  called in the profile's order and the mock transcript reaches stdout
  (R4-02).
- The e2e's `/dev/null` `os.Stdin` is what keeps the recording alive under
  `go test` now that end of input is not an Enter press (R3-01); before that
  fix the test had to avoid `/dev/null` with an empty regular file.
- The gate's `-timeout=30s` is unreachable for this package on this host with
  or without this worklog: `go test ./e2e -race -count=3 -timeout=30s` times
  out at the pre-worklog commit too (see the README journal).

### Review 2, 2026-10-28 — root review, holistic sweep

**Status: no finding.** The shipped profile, its prompt tests and the
profile-driven run are unchanged. `go test ./e2e -race -count=3
-timeout=180s` passes in 79.9 s with the recorder's new sound probe and stop
handshake, so the mock-model run still inspects, records and transcribes in
order and still leaves exactly one WAV.

### Review 3, 2026-10-28 — root review, holistic sweep

**Status: Complete — R3-01's composition proof and R3-03's documentation
fixed in the same session.**

**Findings**

- [x] **R3-01** (major, phase 3) — the e2e's stdin workaround hid a real
  defect: the record tool stopped at once whenever stdin was a non-terminal
  character device such as `/dev/null`. The test now points `os.Stdin` at
  `/dev/null` instead of an empty regular file, so the profile-driven run
  proves that the recording survives EOF and ends by the silence timeout,
  then produces the six facts and the transcript in order. The run passed
  with `-race -count=3`; before the phase-3 fix the same setup failed with
  `ffmpeg produced no readable wav`.
- [x] **R3-03** (minor, docs) — L3 still said the e2e "skips when the audio
  tools are still absent", which the shipped test has not done since its
  first implementation: it swaps in a fresh registry and calls `Init` under
  the fixture. L3 now says so, and names the `/dev/null` requirement.

**Notes.**

- The prompt's "Run it in a terminal" advice is now a preference, not a
  requirement: a non-interactive run records until the silence timeout or
  `max-seconds` and returns the facts.

### Review 4, 2026-10-28 — root review

**Status: Complete — R4-02 fixed in the fix session (2026-10-28).**

**Findings**

- [x] **R4-02** (minor, `F10`) — F10 said the mock-model run "writes the
  resulting note files", but `Test_e2e_meeting_transcriber_profile` asserts
  three tool results in order and exactly one WAV; the query scripts only
  `audio_sources`, `audio_record` and `audio_transcribe`, and the mock returns
  an empty input for `write_file` (`internal/vendors/mock.go:284`), so no note
  file can exist. Narrow F10 to what the e2e proves, or script `write_file` and
  assert the note. See the README row.

**Verified good.**

- **The profile is valid and resolvable.** `remote.Validate` accepts the file,
  the model, prompt and `use_tools` are set, and every non-MCP tool name
  resolves in a registry built with an always-found lookup
  (`Test_meeting_transcriber_profile_is_valid`).
- **The prompt carries its clauses.** `Test_meeting_transcriber_profile_prompt`
  pins `profile.2`–`profile.8`, the step order inspect → record → transcribe →
  raw save → render → notes, and the absence of a directory, index or notebook
  literal.
- **The composition runs.** `go test ./e2e -race -count=3 -timeout=180s` passes
  (126.8 s). The mock run exits 0 with `system_audio: available`,
  `recorded: <dir>` and `mock transcription` in that order, over `/dev/null`
  stdin, and leaves exactly one WAV (R3-01's composition proof).
- **The example is documented.**
  `Test_meeting_transcriber_documented` passes against `examples.md` and
  `examples/profiles/README.md`.

### Fix session, 2026-10-28 — R4-02 closed

F10 is narrowed to what the mock-model run proves: the three tools are called
in order and the mock transcript reaches stdout. The note-file claim is
replaced by the fact that note files come from the user's own MCP servers,
which the mock model cannot drive. The e2e and the prompt contract tests are
unchanged.
