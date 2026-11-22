# Known voices — a library of named voice clips that labels recurring speakers

## Goal

A diarized transcript names the people the user already knows instead of labelling them Speaker A,
Speaker B. clai keeps a library of short voice clips per person. After a recording is transcribed, a
naming request puts the selected people's clips before a clip of each transcript speaker, and a speaker
takes a library person's name by agreement (L5.1). The
library grows by hand (`clai audio voices add`, amended 2026-11-19) and on its own (`audio_remember_voice`), and
every clip earns a rank from how its namings fare.

What must stay true: F1 (vendor agnostic), F3 (unchanged without voices), F4 (naming never fails a
transcription), F5 (confirmed clips are kept), F8 (clips stay on the machine) and F10 (concurrent
writers lose nothing).

## Glossary

| Term           | Meaning                                                                                                              |
| -------------- | -------------------------------------------------------------------------------------------------------------------- |
| Voice          | one named person in the library, with one or more clips                                                              |
| Clip           | 3–10 s of one person speaking, stored as 16 kHz mono FLAC                                                            |
| Library        | every voice under `<clai-config>/voices/`, in the format phase 1 declares                                            |
| Event          | one appended line of a voice's event log, `voices/<slug>/events.jsonl`: a clip added, confirmed, overruled, removed, placed or naming someone |
| Confirmed      | a clip the user added or kept (F5)                                                                                   |
| Unconfirmed    | a clip `audio_remember_voice` added; it names speakers, and those names say so, until it is confirmed or removed     |
| Selection      | the people a run names, resolved by `Librarian.Select` with the precedence of F11                                    |
| Pick           | the clips of the selection that go into a naming request: the best-ranked of each person                             |
| Candidate      | a clip of one transcript speaker, cut from the recording by `SampleExtractor`'s rules                                |
| Naming request | the request phase 5 builds from picked clips and candidates and sends through `Transcriber` (L5.1, L5.3)              |
| Order          | the sequence of picked clips in a naming request; phase 5 names each recording in two (L5.1)                          |
| Agreement      | how a speaker takes a person's name: phase 5's local fact L5.1                                                        |
| Provenance     | the clip that named a segment and its status, as the segment's `voice: {clip, status}`; the speaker is the plain name (F6) |
| Overrule       | a name a clip gave that the user or the agent corrected; it counts against that clip                                 |
| Rank           | the order of a person's clips by `Ranker` (F7)                                                                       |
| Menu           | the voices CLI's three levels, people, one person's clips and one clip, entered by a path of arguments |
| Path           | the arguments after `voices list`: a person, a clip and an action with its arguments, each one optional |
| Inspection     | the measurement of a clip when it is added: length, speech share, clipping, loudness                                 |
| Naming report  | the typed outcome of naming beside the segments: who was named, by which clip, and why anyone was not                |

## Flows

### Enrol a clip by hand

```text
┌──────┐                     ┌────────────┐                  ┌───────────┐        ┌───────────┐
│ user │                     │ voices CLI │                  │ Inspector │        │ Librarian │
└───┬──┘                     └──────┬─────┘                  └─────┬─────┘        └─────┬─────┘
    │                               │                              │                    │
    │── voices add <wav> -name n ──▶│                              │                    │
    │                               │                              │                    │
    │                               │── Inspect(file, from, to) ──▶│                    │
    │                               │                              │                    │
    │                               │◀─────── Inspection ──────────│                    │
    │                               │                              │                    │
    │                               │─────── Enrol(name, inspection, confirmed) ───────▶│
    │                               │                              │                    │
    │                               │◀──────────────────── ClipRef ─────────────────────│
    │                               │                              │                    │
    │◀───── "clip <id> added" ──────│                              │                    │
    │                               │                              │                    │
```

For example, `clai audio voices add rec.wav -from 61.2 -to 69 -name Lorentz`, or in short names
`clai a v a rec.wav -from 61.2 -to 69 -name Lorentz` (amended 2026-11-19); a pre-sliced
clip needs neither `-from` nor `-to`. A clip the inspection refuses (L3.1–L3.3) is a `ClipQualityError`,
and nothing is enrolled.

### Browse the library

```text
┌──────┐                     ┌────────────┐         ┌───────────┐
│ user │                     │ voices CLI │         │ Librarian │
└───┬──┘                     └──────┬─────┘         └─────┬─────┘
    │                               │                     │
    │───── voices list [path] ─────▶│                     │
    │                               │                     │
    │                               │────── List() ──────▶│
    │                               │                     │
    │                               │◀─ []VoiceSummary ───│
    │                               │                     │
    │◀─ the level the path names ───│                     │
    │                               │                     │
```

The path is up to three arguments, a person, a clip and an action; the menu enters the level the path
names (The voices menu, by example).

### The agent transcribes with known voices

```text
┌───────┐               ┌──────────────────┐       ┌───────────┐       ┌───────┐                ┌─────────────┐
│ model │               │ audio_transcribe │       │ Librarian │       │ Namer │                │ Transcriber │
└───┬───┘               └─────────┬────────┘       └─────┬─────┘       └───┬───┘                └──────┬──────┘
    │                             │                      │                 │                           │
    │──── call(file, voices?) ───▶│                      │                 │                           │
    │                             │                      │                 │                           │
    │                             │── Select(people) ───▶│                 │                           │
    │                             │                      │                 │                           │
    │                             │◀──── Selection ──────│                 │                           │
    │                             │                      │                 │                           │
    │                             │──────────────────────── Transcribe(file) ─────────────────────────▶│
    │                             │                      │                 │                           │
    │                             │◀─────────────────────────── segments ──────────────────────────────│
    │                             │                      │                 │                           │
    │                             │───── Name(file, segments, picks) ─────▶│                           │
    │                             │                      │                 │                           │
    │                             │                      │                 │─── Transcribe(forward) ──▶│
    │                             │                      │                 │                           │
    │                             │                      │                 │◀─────── segments ─────────│
    │                             │                      │                 │                           │
    │                             │                      │                 │── Transcribe(reversed) ──▶│
    │                             │                      │                 │                           │
    │                             │                      │                 │◀─────── segments ─────────│
    │                             │                      │                 │                           │
    │                             │◀────────────── Naming ─────────────────│                           │
    │                             │                      │                 │                           │
    │                             │── Tally(outcomes) ──▶│                 │                           │
    │                             │                      │                 │                           │
    │◀─ named segments, report ───│                      │                 │                           │
    │                             │                      │                 │                           │
```

`voices` is optional; `Select` applies F11. An empty selection skips everything after `Transcribe(file)`
and returns today's output (F3). `Transcribe(file)` is today's route: one request, a split, or
calibration. A failed naming leaves the speakers as transcribed and says why in the report (F4).

### The user transcribes a file with known voices

```text
┌──────┐                    ┌───────────────────┐      ┌───────────┐       ┌───────┐  ┌─────────────┐
│ user │                    │ TranscribeQuerier │      │ Librarian │       │ Namer │  │ Transcriber │
└───┬──┘                    └─────────┬─────────┘      └─────┬─────┘       └───┬───┘  └──────┬──────┘
    │                                 │                      │                 │             │
    │── audio transcribe -voices f ──▶│                      │                 │             │
    │                                 │                      │                 │             │
    │                                 │── Select(people) ───▶│                 │             │
    │                                 │                      │                 │             │
    │                                 │◀──── Selection ──────│                 │             │
    │                                 │                      │                 │             │
    │                                 │───────────────── Transcribe(file) ──────────────────▶│
    │                                 │                      │                 │             │
    │                                 │◀──────────────────── segments ───────────────────────│
    │                                 │                      │                 │             │
    │                                 │───── Name(file, segments, picks) ─────▶│             │
    │                                 │                      │                 │             │
    │                                 │◀────────────── Naming ─────────────────│             │
    │                                 │                      │                 │             │
    │                                 │── Tally(outcomes) ──▶│                 │             │
    │                                 │                      │                 │             │
    │◀───────── transcript ───────────│                      │                 │             │
    │                                 │                      │                 │             │
```

For example, `clai audio transcribe -voices 'Lorentz,Christoph' -af json meeting.wav`: as everywhere in
`clai audio transcribe`, flags come before the file. `Name` sends its
two naming requests to the `Transcriber` as in the agent's flow.

The file is the command's one argument (`-` reads stdin), whatever its name: `clai audio transcribe
voices` transcribes a file named `voices`, and `transcribe` has no sub-verb. With no file, the command
prints its help and the error on stderr and exits 1, as `clai audio` with no verb and `clai chat` with
no subcommand do (phase 9, `Wire.7`). (amended 2026-11-19)

### The agent learns a voice

```text
┌───────┐                              ┌──────────────────────┐               ┌───────────┐         ┌───────────┐
│ model │                              │ audio_remember_voice │               │ Inspector │         │ Librarian │
└───┬───┘                              └───────────┬──────────┘               └─────┬─────┘         └─────┬─────┘
    │                                              │                                │                     │
    │─────── call(file, start, end, name) ────────▶│                                │                     │
    │                                              │                                │                     │
    │                                              │── Inspect(file, start, end) ──▶│                     │
    │                                              │                                │                     │
    │                                              │◀──────── Inspection ───────────│                     │
    │                                              │                                │                     │
    │                                              │─────── Enrol(name, inspection, unconfirmed) ────────▶│
    │                                              │                                │                     │
    │                                              │◀───────────────────── ClipRef ───────────────────────│
    │                                              │                                │                     │
    │                                              │──────────── Review(overrules, overrule) ────────────▶│
    │                                              │                                │                     │
    │◀─ "<name>: clip <id> added (unconfirmed)" ───│                                │                     │
    │                                              │                                │                     │
```

`overrules` is optional: the agent passes the clip a wrong library name came from (the segment's
`voice.clip`), and only then is `Review(…, overrule)` called. The new clip stays unconfirmed until kept.
The result line is frozen: another worklog reads it to learn which clips a run added. When the overrule
fails after the clip was added, the call still succeeds, and a second line says why the overrule failed.

### The user keeps a learned clip

```text
┌──────┐                         ┌────────────┐            ┌───────────┐
│ user │                         │ voices CLI │            │ Librarian │
└───┬──┘                         └──────┬─────┘            └─────┬─────┘
    │                                   │                        │
    │── list <person> <clip> confirm ──▶│                        │
    │                                   │                        │
    │                                   │── Review(ref, keep) ──▶│
    │                                   │                        │
    │◀────────── "confirmed" ───────────│                        │
    │                                   │                        │
```

### The user corrects a name

```text
┌──────┐                          ┌────────────┐                ┌───────────┐
│ user │                          │ voices CLI │                │ Librarian │
└───┬──┘                          └──────┬─────┘                └─────┬─────┘
    │                                    │                            │
    │── list <person> <clip> overrule ──▶│                            │
    │                                    │                            │
    │                                    │── Review(ref, overrule) ──▶│
    │                                    │                            │
    │◀────────── "overruled" ────────────│                            │
    │                                    │                            │
```

### The user deletes a clip

```text
┌──────┐                        ┌────────────┐               ┌───────────┐
│ user │                        │ voices CLI │               │ Librarian │
└───┬──┘                        └──────┬─────┘               └─────┬─────┘
    │                                  │                           │
    │── list <person> <clip> delete ──▶│                           │
    │                                  │                           │
    │                                  │── Remove(person, clip) ──▶│
    │                                  │                           │
    │◀────────── "deleted" ────────────│                           │
    │                                  │                           │
```

### The user trims a clip

```text
┌──────┐                          ┌────────────┐                   ┌───────────┐        ┌───────────┐
│ user │                          │ voices CLI │                   │ Inspector │        │ Librarian │
└───┬──┘                          └──────┬─────┘                   └─────┬─────┘        └─────┬─────┘
    │                                    │                               │                    │
    │── list <person> <clip> trim a b ──▶│                               │                    │
    │                                    │                               │                    │
    │                                    │── Inspect(clip file, a, b) ──▶│                    │
    │                                    │                               │                    │
    │                                    │◀──────── Inspection ──────────│                    │
    │                                    │                               │                    │
    │                                    │──────── Enrol(person, inspection, status) ────────▶│
    │                                    │                               │                    │
    │                                    │◀──────────────────── ClipRef ──────────────────────│
    │                                    │                               │                    │
    │                                    │──────────── Remove(person, old clip) ─────────────▶│
    │                                    │                               │                    │
    │◀─────── "trimmed as <id>" ─────────│                               │                    │
    │                                    │                               │                    │
```

The trimmed clip keeps its status and starts with no namings; the old clip is removed, since a clip file is
never rewritten (F10). `Enrol` is told which clip the new one replaces, so a trim never prunes another clip
and is not refused at the cap (L4.2).

### The user moves a clip to another person

```text
┌──────┐                              ┌────────────┐             ┌───────────┐       ┌───────────┐
│ user │                              │ voices CLI │             │ Inspector │       │ Librarian │
└───┬──┘                              └──────┬─────┘             └─────┬─────┘       └─────┬─────┘
    │                                        │                         │                   │
    │── list <person> <clip> move <other> ──▶│                         │                   │
    │                                        │                         │                   │
    │                                        │── Inspect(clip file) ──▶│                   │
    │                                        │                         │                   │
    │                                        │◀───── Inspection ───────│                   │
    │                                        │                         │                   │
    │                                        │───── Enrol(other, inspection, status) ─────▶│
    │                                        │                         │                   │
    │                                        │◀───────────────── ClipRef ──────────────────│
    │                                        │                         │                   │
    │                                        │─────────── Remove(person, clip) ───────────▶│
    │                                        │                         │                   │
    │◀────────────── "moved" ────────────────│                         │                   │
    │                                        │                         │                   │
```

### The user plays a clip

```text
┌──────┐                      ┌────────────┐                  ┌────────┐
│ user │                      │ voices CLI │                  │ Runner │
└───┬──┘                      └──────┬─────┘                  └────┬───┘
    │                                │                             │
    │── list <person> <clip> play ──▶│                             │
    │                                │                             │
    │                                │── Run(player, clip file) ──▶│
    │                                │                             │
    │                                │◀────────── done ────────────│
    │                                │                             │
    │◀───── back at the clip ────────│                             │
    │                                │                             │
```

The player is the first of `play`, `ffplay -nodisp -autoexit` and the OS opener (`xdg-open`, `open`) that
the machine has; with none, the action says so and names the three. On a terminal, Enter stops the clip,
as it stops a recording; a clip that ends by itself leaves the next line typed to the menu. Without a
terminal the clip plays to its end.

## The voices menu, by example

This is the behaviour the voices CLI (phase 7) and the menu (phase 6) must have. The look (columns,
wording, colours) is the implementer's; what each input does is not.

`clai audio voices list [person [clip [action [args…]]]] [-json]` is a menu of three levels:
the people, one person's clips, and one clip. The arguments are a path into it.

`voices` is a verb of `audio`, beside `transcribe`, `calibrate` and `help`, and its short name is `v`.
Its verbs `add` and `list` have the short names `a` and `l`. A short name is the verb in every respect:
`clai a v l lorentz 1a7d confirm` is `clai audio voices list lorentz 1a7d confirm` (example 10).
(amended 2026-11-19: the voices CLI was `clai audio transcribe voices`, with no short names.)

1. Each argument picks its level, as `clai config publish [kind [name [mode]]]` does; the menu opens at
   the deepest level the path names. A level the path leaves out is asked in that level's table.
2. A person matches by name or slug, case-insensitive. A clip matches by a unique prefix of its id, or by
   its index in that person's table. A whole number below the person's clip count is always the index.
3. An argument that matches nothing says so in one line and opens the level above it, as if the argument
   had not been given.
4. An action given as an argument runs at once, prints one line and exits; nothing is asked. An action
   picked in the menu asks before it deletes, moves or trims, then returns to the level it changed.
5. In a table, a number picks a row and goes one level down; `[b]ack` goes one level up; `[q]uit` leaves.
6. The people table shows, per person: clips, unconfirmed clips, namings, wrong namings, last named. The
   clips table shows a person's clips best first (L4.1 sets how many are picked): status, length, namings, wrong
   namings, last named, who added it. The clip view shows those and the clip's path as
   `$(clai confdir)/voices/<slug>/<id>.flac`, whole, so it can be copied and played.
7. A clip's actions are play, confirm (only while unconfirmed), overrule, trim `<from> <to>`,
   move `<person>` and delete.
8. Columns fit the terminal's width; the least important drop first. A path is never cut. On a terminal,
   each level replaces the one before it on the screen, so the menu shows one level at a time instead of
   piling levels up the way `chat list`'s table does today.
9. Without a terminal, there is no menu: the level the path names is printed once and the command exits;
   an action given as an argument still runs. `-json` prints that level as JSON, always an array of people
   in `List`'s shape: everyone, the named person alone, or that person with the named clip alone.
10. Exit status: 0 when the menu was left or an action ran; 1 when an action failed, or, without a
    terminal, when an argument matched nothing; 2 on a usage error. Ctrl-C at a menu question leaves
    exactly as `q` does: exit 0, nothing more is printed, the question is not waited for, and nothing
    typed after it is acted on. This is the standard for every clai menu.

### 1. Browse from the top

```text
$ clai audio voices list
Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named
0     | Christoph | 1     | 1           | 0     | 0     | never
1     | Lorentz   | 3     | 1           | 17    | 3     | 2026-11-25
2     | Syrus     | 2     | 0           | 9     | 0     | 2026-11-22
[u]nconfirmed only · [q]uit · or pick a person: 1

Lorentz · 3 clips, best first
Index | Clip         | Status      | Length | Named | Wrong | Last named | Added by
0     | 9f3c2a1b0d4e | confirmed   | 8.4 s  | 14    | 1     | 2026-11-25 | you
1     | e81d5530c6a9 | confirmed   | 6.1 s  | 3     | 2     | 2026-11-21 | the agent
2     | 1a7d0f23c9b8 | unconfirmed | 9.7 s  | 0     | 0     | never      | the agent
[b]ack · [q]uit · or pick a clip: 2

Lorentz · clip 1a7d0f23c9b8 · unconfirmed
  length  9.7 s, learned 2026-11-24 by the agent
  named   nobody yet
  rank    3 of 3, not picked for naming (2 per person)
  path    $(clai confdir)/voices/lorentz/1a7d0f23c9b8.flac
[p]lay · [c]onfirm · [o]verrule · [t]rim · [m]ove to another person · [d]elete · [b]ack · [q]uit: p
▶ playing 9.7 s (ffplay) · [Enter] stops
```

### 2. Straight to a person

```text
$ clai audio voices list lorentz
Lorentz · 3 clips, best first
Index | Clip         | Status      | Length | …
```

`Lorentz`, `LORENTZ` and the slug `lorentz` all match.

### 3. Straight to a clip

```text
$ clai audio voices list lorentz 1a7d
Lorentz · clip 1a7d0f23c9b8 · unconfirmed
  …
[p]lay · [c]onfirm · …

$ clai audio voices list lorentz 2
Lorentz · clip 1a7d0f23c9b8 · unconfirmed
  …
```

### 4. An action, with no menu

```text
$ clai audio voices list lorentz 1a7d confirm
Lorentz/1a7d0f23c9b8: confirmed

$ clai audio voices list lorentz e81d overrule
Lorentz/e81d5530c6a9: overruled (3 of 3 namings wrong)

$ clai audio voices list christoph 4be0 move syrus
Christoph/4be07c19a2f1 moved to Syrus as 4be07c19a2f1 (unconfirmed)

$ clai audio voices list lorentz e81d trim 0.8 5.9
Lorentz/e81d5530c6a9 trimmed to 5.1 s as 7c20b1e94a03; the old clip is removed

$ clai audio voices list lorentz 1a7d delete
Lorentz/1a7d0f23c9b8: deleted

$ clai audio voices list lorentz 9f3c play
▶ playing 8.4 s (ffplay) · [Enter] stops
```

### 5. An action picked in the menu asks first

```text
$ clai audio voices list lorentz 1a7d
Lorentz · clip 1a7d0f23c9b8 · unconfirmed
  …
[p]lay · [c]onfirm · [o]verrule · [t]rim · [m]ove to another person · [d]elete · [b]ack · [q]uit: d
Delete Lorentz/1a7d0f23c9b8 (9.7 s, unconfirmed)? [y/N]: y
Lorentz/1a7d0f23c9b8: deleted

Lorentz · 2 clips, best first
Index | Clip         | Status    | …
```

### 6. A name that matches nothing opens the level above

```text
$ clai audio voices list ada
no voice named "ada"
Index | Person    | Clips | …
[u]nconfirmed only · [q]uit · or pick a person:

$ clai audio voices list lorentz ffff
Lorentz has no clip ffff
Lorentz · 3 clips, best first
…

$ clai audio voices list lorentz 1a7d sing
no action "sing"; the actions are play, confirm, overrule, trim, move, delete
Lorentz · clip 1a7d0f23c9b8 · unconfirmed
…
```

### 7. An action that fails

```text
$ clai audio voices list lorentz e81d trim 2.0 3.5
clip too short: 1.5 s, a clip is 3–10 s
$ echo $?
1
```

### 8. Without a terminal, and for scripts

```text
$ clai audio voices list | cat
Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named
0     | Christoph | 1     | 1           | 0     | 0     | never
…

$ clai audio voices list ada < /dev/null
no voice named "ada"
$ echo $?
1

$ clai audio voices list -json            # every person, their clips, each clip's path
$ clai audio voices list lorentz -json    # Lorentz's clips only
```

### 9. Adding clips

```text
$ clai audio voices add ~/clips/christoph.wav
Christoph: clip 4be07c19a2f1 added (confirmed)

$ clai audio voices add meeting.wav -from 61.2 -to 69 -name Syrus
Syrus: clip 3d81f60a2b97 added (confirmed)

$ clai audio voices add meeting.wav -from 61.2 -to 69
-from and -to cut a clip out of a recording, so the person must be named: add -name <person>
$ echo $?
2
```

A pre-sliced clip is named after its file, `christoph.wav` becoming Christoph. An empty library's people
table says `no voices yet; add one with clai audio voices add <wav> -name <name>` (amended
2026-11-19), and `-json` prints `[]`.

### 10. Short names (amended 2026-11-19)

```text
$ clai a v a rec.wav -from 61.2 -to 69 -name Lorentz
Lorentz: clip 5e0c41d9a7b2 added (confirmed)

$ clai a v l lorentz 1a7d confirm
Lorentz/1a7d0f23c9b8: confirmed

$ clai a v l -json                 # as clai audio voices list -json

$ clai a t voices                  # a file named voices: it is transcribed
```

`v`, `a` and `l` are the verbs `voices`, `add` and `list`; nothing else about a command changes with the
name it is written by. Under `transcribe` the word `voices` is a file like any other.

## Status board

**Line:** waiting on human: phase 9

| #   | Node                          | Parent | Class       | Interface                                                                                                     | Status      |
| --- | ----------------------------- | ------ | ----------- | ------------------------------------------------------------------------------------------------------------- | ----------- |
| 1   | Library format                | root   | contract    | data contract `store`: `<clai-config>/voices/<slug>/voice.json`, `<id>.flac`, `events.jsonl`, their record schemas (`internal/audio/voices/schema/`) and their Go mirror (`internal/audio/voices/store.go`) | Complete |
| 2   | Clip ranker                   | root   | contract    | `internal/audio/voices:Ranker` (`internal/audio/voices/rank.go`)                                              | Complete |
| 3   | Clip inspector                | root   | contract    | `internal/audio/voices:Inspector` (`internal/audio/voices/inspect.go`); its test double `voicestest.Runner` (`internal/audio/voices/voicestest/runner.go`), test support for phases 3, 7–9 | Complete |
| 4   | Voice librarian               | root   | contract    | `internal/audio/voices:Librarian`, embedding `Enroller` (`Enrol`), `Lister` (`List`), `Selector` (`Select`), `Tallier` (`Tally`), `Reviewer` (`Review`), `Remover` (`Remove`) (`internal/audio/voices/librarian.go`) | Complete |
| 5   | Speaker namer                 | root   | contract    | `internal/audio:Namer` (`internal/audio/namer.go`)                                                            | Complete |
| 6   | Menu                          | root   | contract    | `internal/menu:Navigator`: levels of tables entered at the depth a path of arguments names (`internal/menu/menu.go`) | Complete |
| 7   | voices CLI                    | root   | contract    | CLI `clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]` and `l|list [person [clip [action [args…]]]] [-json]` (The voices menu, by example); command tree `internal/audio:VoicesCommand` under `audio` (`internal/audio/voices_cmd.go`) | Complete |
| 8   | audio_remember_voice tool     | root   | contract    | tool schema `audio_remember_voice` (`pkg/tools/audio_tool_remember_voice.go`)                 | Complete |
| 9   | Known voices in transcription | root   | composition | root: `audio_transcribe` `voices` argument, `-voices` on `query`/`chat` and on `audio transcribe`, segment `voice` field; with a non-empty selection the json output is `{"segments": […], "naming": {…}}`; `audio transcribe` takes only a file and without one prints its help | In Progress |

### Imports

| Member                                                                                          | Kind                         | Used by phases |
| ----------------------------------------------------------------------------------------------- | ---------------------------- | -------------- |
| `internal/audio:Transcriber`, `Segment` (`generic.Segment`)                                     | repository interface, types  | 5, 9           |
| `internal/audio:SampleExtractor`                                                                | repository calibration code  | 5              |
| `internal/audio:Budgets`, `ResolveBudgets` (`max-request-bytes`, `max-request-seconds`)      | repository request budgets   | 5, 9           |
| `internal/audio:CommandRunner`, `ExecRunner` (satisfies `voices:Runner`, F9)                    | repository seam (ffmpeg)     | 5, 9           |
| `internal/audio:Configurations`, `TranscribeConfig`                                             | repository config types      | 9              |
| `internal/audio:TranscribeQuerier`, `ToolOverrides`, `SetTranscribeOverrides`                   | repository transcribe route  | 9              |
| `internal:MediaToolFlags`, `MediaToolSettings` (`-am`, `-af`; `internal/flags.go`)              | repository run flags         | 9              |
| `internal/audio:Flags`, `ApplyFlagOverrides` (`clai audio transcribe`'s flags; `cmd.go`)        | repository command flags     | 9              |
| `pkg/tools:AudioTranscribeTool`, `AudioTranscribeEngine`; `internal/tools:Registry` (`builtins`, `registerLocalTools`, `NewRegistry`) | repository tool registration; phase 9 changes the engine's signature to take `AudioTranscribeCall` | 8, 9 |
| `internal/audio:MockTranscriber` (the `test`/`test-diarize` route; `CLAI_MOCK_TRANSCRIBE_DELAY`) | repository test transcriber | 9 |
| `pkg/text/models:LLMTool`, `Input`, `Specification`, `InputSchema`, `ParameterObject`           | repository tool types        | 8              |
| `internal/runenv:Text`, `With`                                                                  | repository run streams       | 8              |
| `internal:Command.Subs`, `Help` (the help `Wire.7` prints; amended 2026-11-19); `go_away_boilerplate/pkg/cmd:Subcommander` (cmd-dispatch.md; a key is `name\|short`) | repository CLI framework     | 7, 9           |
| `go_away_boilerplate/pkg/table:Table`, `TableAction`, `ErrBack`, `ErrUserInitiatedExit`, `ReadUserInputFrom`; `utils.SessionDimensions` (`dimensions.Dimensions`), `utils.TerminalRows` | interactive table, terminal size and rows | 6 |
| `internal/runenv:Env` (`Stdin`, `StdinIsTerminal`, `IsTerminal`, `JSON`, `WriteJSONDocument`, `TablePrompter`); `internal:ExitCoder`, `ExitStatus`; `internal/cli` `-json` scan and misplaced-flag hint (`architecture/output.md`) | run streams, terminal state, exit status | 7, 9 |
| `internal/runenv:Warnf`, `SubsystemAudio`                                                       | the run's warnings (`warn [audio] …` on stderr); phase 9 reports the naming warnings of Output.5 through it, phase 7 the leftover file of add.6 | 7, 9 |
| `internal/cli` composition's signal handling (`compose.go`: the run's context, `firstSignal`, the interrupted branch of the dispatch); `internal/runtimeboundary:SignalSource`, `DispatchMonitor`; `internal/interrupt:SignalError` | repository signal handling; phase 9 changes it for one case, a first SIGINT at a menu question (Wire.6) | 9 |
| `internal/utils:GetClaiConfigDir`                                                               | repository helper            | 4, 8, 9        |
| `internal/testenv:Main`, `FakeBins`; `e2e/e2etest:Harness`, `WriteExecutable`                   | repository test isolation; `WriteExecutable` places phase 9's ffmpeg stub (L5.4) | 1–9 |

## Scope

Out of scope, and no phase builds it:

- The Penelope app's part: attendee checkboxes, keeping the library in the browser, Keep and correct, the
  hub profile. That is the `penelope-known-voices` worklog in the clai-in-wasmer-sh-poc repository, which
  imports this one.
- Provider-native speaker references (OpenAI `known_speaker_names[]`/`known_speaker_references[]`) and any
  provider speaker library. Naming is the same mechanism on every transcriber.
- Pre-seeding the calibration registry with library voices, or any change to how a recording is split,
  calibrated or stitched. Naming runs after transcription, on whatever segments it returned.
- Speaker embeddings or any model other than the configured transcriber.
- Recording clips. A clip is cut from a file that already exists.
- Syncing the library through `clai cloud`; its path allowlist stays as it is.
- Consent. The user asks the people they enrol; no phase records or enforces it.
- Measurement tooling. The go/no-go measurement is a dated note cited under Root facts; the agent runs it
  with the maintainer's keys and recordings (Q8, Q16), phase 9 is not `Complete` until the note exists,
  and no measurement code is committed.

## Root facts

**F1 — Vendor agnostic.** Naming reaches the transcriber only through `audio:Transcriber.Transcribe`
and reads only a segment's start, end and speaker label. No member of this worklog names a vendor or a
model. A transcriber that returns no speaker labels has no names to give: the naming report says
`not-diarized` and the transcript comes back as transcribed. Binds `Namer.Name`, the root.

**F3 — Unchanged without voices.** `Librarian.Select` resolves an empty selection when no voice is
named and the library holds no confirmed clip, or has no `voices/` directory, or cannot be read. With an
empty selection, `audio_transcribe` and `clai audio transcribe` make exactly the requests they make today
and return byte-identical output: no naming request is sent, no naming report is added, and nothing
under `voices/` is written. Reading the library to resolve the selection is the only access. Binds
`Selector.Select`, the root.

**F4 — Naming never fails a transcription.** With a non-empty selection, a naming request that fails, a
naming that agrees on nothing, or a library that cannot be read leaves every speaker as transcribed and
says why in the naming report. Only the transcription's own failures fail the call. F3 decides first: a
library unreadable with no voice named is an empty selection, not a report. ffmpeg is a dependency of
naming people, not a naming failure: when people are named (`-voices`, or the tool's `voices` argument)
and ffmpeg does not resolve, the transcription stops before any request with an error saying `-voices`
needs ffmpeg. With nobody named, a missing ffmpeg stops nothing: an empty selection needs no ffmpeg
(F3), and under the default selection of F11 it is a failed naming request like any other. Binds
`Namer.Name`, `Selector.Select`, the root.

**F5 — Confirmed clips are kept.** Only the menu's delete, and the old clip of a trim or a move, remove a
confirmed clip. Pruning past a person's
stored-clip cap (L4.2) removes the lowest-ranked unconfirmed clip, and refuses a new clip when a person's
clips are all confirmed. A clip `audio_remember_voice` adds is pruned like any other; the tool has no
remove or replace of its own. Binds `Enroller.Enrol`,
`Remover.Remove`, the voices CLI's delete, trim and move, the `audio_remember_voice` tool.

**F6 — Provenance.** Every segment a library clip named carries that clip's reference and status. Every
naming whose two requests were both answered appends, for each picked clip, whether it was placed and whom
it named. A confirm and an overrule
append to the clip they name. Binds `Namer.Name`, `Tallier.Tally`, `Reviewer.Review`, the segment
`voice` field.

**F7 — One ranking, in code.** Which clips are picked and which are pruned is decided by `Ranker.Rank`
alone, over each clip's events. Its constants are code, not configuration, so the heuristic can change
without changing an interface. Binds `Ranker.Rank`, `Selector.Select`, `Enroller.Enrol`.

**F8 — Clips stay on the machine.** A clip's audio leaves the machine only inside a naming request to
the configured transcriber. No log line, error or tool result carries clip audio. Binds `Librarian`,
`Namer.Name`, the voices CLI, the `audio_remember_voice` tool.

**F9 — One package direction.** `internal/audio` imports `internal/audio/voices`; `voices` never
imports `internal/audio`. `voices` declares the ffmpeg runner it needs (`voices:Runner`), which
`audio:ExecRunner` satisfies. A member outside `internal/audio` (the `audio_remember_voice` tool in
`pkg/tools`) gets its `Inspector`, built by the root over its runner, and its `Librarian` injected by the
root through `pkg/tools:AudioRememberVoiceDeps`, as `AudioTranscribeEngine` is today.
Binds the `store` package (phase 1), `Inspector` and its test double `voicestest` (phase 3), the voices CLI
(phase 7), the `audio_remember_voice` tool and `AudioRememberVoiceDeps` (phase 8), and the root's wiring
(phase 9).

**F10 — Concurrent writers lose nothing.** Two clai processes, or a clai process and a copy that reads
the files back, may write one library at once. Every fact is one appended event line; a clip file is
written once, by a temporary file named `.<id>.flac.tmp` beside it and a rename, and never rewritten;
names starting with `.` are never part of the library, and an event line counts only once it ends in a
newline; a clip's id is the first 12 hex
digits of its FLAC's sha256, so two writers never allocate one id; `voice.json` holds only the person's
name and is written by an exclusive create, so a second writer finds it and keeps it. A reader folds the
event lines, so the union of two copies' lines is their merge.
Binds the `store` format, `Librarian`.

**F11 — Selection precedence.** The people a run names are the tool call's `voices` argument when the
model gives one, else the run's `-voices` flag, else every person with a confirmed clip, up to
max-voices, in rank order of their best clip. A named person the library does not hold is left out
and listed in the naming report as `unknown-voice`; it never fails the call (F4). A name never stops a
run; ffmpeg missing while people are named does, as F4 says. Binds `Selector.Select`, the root.

| Parameter                              | Default                                              | Decided by                                                  |
| -------------------------------------- | ---------------------------------------------------- | ----------------------------------------------------------- |
| max-voices                             | 12, `audioConfig.json` `transcribe.max-voices`; 0 turns the default selection off, so only people named are named; negative is a load error | maintainer, 2026-11-18 (Q4); 0 as the opt-out, orchestrator 2026-11-18 |
| clip length                            | 3–10 s after slicing                                 | `architecture/audio.md` sample rules (`sample.go`)          |
| go/no-go                               | ≥ 94 % of enrolled speech named right, ≤ 2 % of other speech named | maintainer, 2026-11-18 (Q8); measurement note, pending |

## Phase-local facts

Moved into the phase files' Local facts, where each is defined and nowhere else: L3.1–L3.3 (and L3.4) in
`phase-3-clip-inspector.md`, L4.1–L4.3 in `phase-4-voice-librarian.md`, L5.1–L5.4 in
`phase-5-speaker-namer.md`, L6.1–L6.3 in `phase-6-menu.md`, L7.1–L7.4 in `phase-7-voices-cli.md`, L8.1 in
`phase-8-audio-remember-voice-tool.md`, L9.1 in `phase-9-known-voices-in-transcription.md`.

## Validation policy

### Gates

```sh
make qa
go vet ./...
go run github.com/mibk/dupl@latest -t 80 .
```

`make qa` runs staticcheck, gofumpt, `go fix`, and `go test ./... -race -count=3 -cover -timeout=30s`
with `-p` at the physical core count. Goldens are regenerated with
`go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens`.

### Human checks

None is a gate. The first needs listening, the second a real terminal; the root's need a provider key and
real recordings, which the maintainer provides and the agent runs (Q8), counting against the named
transcripts in the notebook (Q11).

| Node | Command                                                                                                                          | Certified by                                                       |
| ---- | -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| 3    | `clai audio voices add <recording>.wav -from <s> -to <s> -name <person>` (amended 2026-11-19), then play `voices/<slug>/<clip>.flac` | the clip is that person alone, audible, and not clipped            |
| 7    | `clai audio voices list` (amended 2026-11-19) on a real terminal: browse to a clip, play it, go back, quit                       | one level shows at a time, the last level stays after quitting, and nothing printed before the command is erased |
| root | `clai audio transcribe -am gpt-4o-transcribe-diarize -voices <people> -af json <second recording>.wav`, with clips enrolled from the first | the go/no-go rule holds on this recording                 |
| root | the same with `-am 'or:elevenlabs/scribe-v2+diarize'`                                                                             | the go/no-go rule holds on this recording                          |

## Findings

| ID  | Severity | Node | Owner | Status | Summary |
| --- | -------- | ---- | ----- | ------ | ------- |
| V1-01 | blocker | root | plan | Closed 1550dec+wip | check 1 · status board, phases 1–7 have no phase file · write phases 1–7 after the README sign-off |
| V1-02 | major | root | plan | Closed 1550dec+wip | check 3 · Imports + status board, phases 2, 4, 5, 7 · break the `internal/audio` ↔ `internal/audio/voices` import cycle (phase 2 imports `audio:CommandRunner`; phases 4, 5, 7 in `internal/audio` must import `voices`), and record the package direction in Imports |
| V1-03 | major | 4 | plan | Closed 1550dec+wip | check 9 · Imports row `RequestAssembler`, `AssembleRequest`, `SampleClip` · the seam cuts every sample from one `SourcePath` and always appends a core, so no provider puts `voices/` clips into a core-less naming request; have phase 4 own its request builder over `CommandRunner`, or assign the seam's extension to a phase |
| V1-04 | major | 7 | plan | Closed 1550dec+wip | check 9 · Glossary Selection, F3, max-voices · name the member that resolves the default selection, import `internal/audio:Configurations` for `transcribe.voices`, and restate F3 (and its precedence over F4) so an empty default selection is knowable without the read F3 forbids |
| V1-05 | major | 2 | fix | Closed 1550dec+wip | check 9 · parameter "inspection floors" · loudness floor and clipped-run length have no value and "measurement note, pending" is no source; set the values from the note before sign-off, or give phase 2 a Human required section |
| V1-06 | major | 4 | fix | Closed 1550dec+wip | check 5 · F2, "dominant share", "silence between clips" · each binds only phase 4; move them to phase 4's Local facts and leave no restatement at the root |
| V1-07 | major | 3 | fix | Closed 1550dec+wip | check 5 · F5, "clips per person per naming request", "stored clips per person" · each binds only phase 3; move them to phase 3's Local facts (or list the phase 5/6 members F5 binds), and have Goal and Glossary cite rather than restate F5 |
| V1-08 | major | 2 | fix | Closed 1550dec+wip | check 5 · parameter "inspection floors" · binds only `Inspector.Inspect`; move it to phase 2's Local facts |
| V1-09 | major | root | fix | Closed 1550dec+wip | check 8 · Flows · triggers without a diagram: `clai audio transcribe <file> -voices …` (the `TranscribeQuerier` route both root Human checks use) and `voices list`; add one flow each |
| V1-10 | minor | 3 | plan | Closed 1550dec+wip | check 7 · status board row 3 · `Library` is a noun holding six-plus verbs in one interface, and "files under `voices/`" is a second interface of no listed kind; name it `-er`, embed small interfaces, and drop or give a kind to the layout |
| V3-01 | major | root | fix | Closed 1550dec+wip | check 4 · Root facts F9 "Binds phases 1–9" · no clause of phases 2, 4, 5 or 6 cites F9; narrow its Binds line to the members that carry it (store `Package.1`, `Inspect.10`, voicestest `Package.1`, `voices.4`, the tool's `CallWithContext.4`/`.15`/`AudioRememberVoiceDeps.1`, root `Wire.2`/`Wire.4`) |
| V3-02 | major | root | fix | Closed 1550dec+wip | check 5 · README menu rule 6 and the `list` example, phase 7 (`TestVoicesAdd_Refusals`, `TestVoicesList_TrimAtTheCap`, `TestVoicesList_Move`, Error management "Six clips"), phase 8 (AC 3, `TestRememberVoice_EnrolRefusals`, `TestRememberVoice_PrunedLikeAnyOther`) · restate L4.1 (2) and L4.2 (6); cite the ids instead |
| V3-03 | major | 9 | fix | Closed 1550dec+wip | check 4 · phase 9 `Flag.3` and `TestFlags_Voices` Must not · "no other package-level value is added" is not observable through the interface; end the clause at the consumed members and drop the Must-not half |
| V3-04 | minor | 3 | plan | Closed 1550dec+wip | check 7 · phase 3 Interface, `voicestest.Runner` · a second surface with its own clauses in a phase that implements `Inspector`; give it its own phase, or record it as `Inspector`'s test support |
| V3-50 | major | 9 | fix | Closed 1550dec+wip | check 9 · phase 9 Test cases preamble, `FakeBins` stubs that "write the file they are asked for and answer a measurement" · `FakeBins` writes `exit 0` only and no consumed member gives a PATH stand-in for the Inspector's or Namer's ffmpeg; run the enrol/naming scenarios over `voicestest`, and give the naming e2e a phase 5 fact on its ffmpeg output argument |
| A01 | quality andon | root | fix | Closed 1550dec+wip | own: `fmt.Errorf` with `%w` in `internal/audio/voices/store.go:264` and `internal/menu/navigate.go:257`, the repository wraps through `internal/diag.Errorf` · `make qa` (wave 1 gate) · `pkg/diagnostic` `TestLocatorInventory` · 1550dec+wip |
| A02 | quality andon | root | fix | Closed 1550dec+wip | own: `internal/audio/voices` tests cost 18.2 s alone under `-race -count=3 -cover` (7.3 s without `-cover`); the inspector's sample-by-sample tests and the event reader's per-field `json.Unmarshal` dominate; `internal/audio` at 26.4 s · `make qa` (wave 4 gate) ×2: `panic: test timed out after 30s` in `internal/audio/voices` (testenv: busy, CPU-bound) · `TestLibrarian_ConcurrentWriters` running at the deadline · 1550dec+wip |
| R1-01 | major | 4 | fix | Closed 1550dec+wip | Enrol.2, Enrol.5, F10 · `library.go:64-66, 95-99, 514-530` · an existing `voice.json` that is not a record (empty, truncated, BOM, another slug) makes every `Enrol` return a ref and nil while storing nothing, skipping the cap; read it back and fail with `*LibraryWriteError`; remove the file when the fallback's write fails |
| R1-02 | major | 7 | fix | Closed 1550dec+wip | voices.3, menu rule 10 · `voices_cmd.go:50`, `voices_cmd_tree.go:105-122`, `voices_cmd_levels.go` · a child program's `*exec.ExitError` reaches `recordExit`, so a failed player or ffmpeg sets clai's exit status (7, 3, or 2 that reads as usage); pin a failed add or action to 1 at the verb boundary; test with `ExitCode()` 2 and 7 |
| R1-03 | minor | 4 | fix | Closed 1550dec+wip | Enrol.3, Enrol.7, Enrol.8 · `library.go:500-539` · the exclusive-create fallback (what a filesystem without links uses) has 0 % coverage; add an injectable link failure and run the rows through it |
| R1-04 | minor | 4 | fix | Closed 1550dec+wip | Enrol.7 · `library.go:477-494` · two writers of the same clip share `.<id>.flac.tmp`, so one can truncate the file the other renamed in; create the temp with `O_EXCL` (exists = another writer) or link it into place |
| R1-05 | minor | 4 | fix | Closed 1550dec+wip | Error management "a deletion fails" · `librarian_test.go` · no test makes a deletion fail (`library.go:97, 102, 374, 487-491` uncovered); add the rows (a `0555` directory works without root). Decided 2026-11-19 (Enrol.9 amended): the last row is `TestEnrol_PruneLeavesAFile`, a prune whose file cannot be deleted returns the new ref with a `*LibraryWriteError` naming the leftover file (`library.go:121`, as the code does) · touches `internal/audio/voices/librarian_test.go`, `export_test.go` |
| R1-06 | minor | 3, 4 | fix | Closed 1550dec+wip | house rule, typed errors · `inspector.go:181-187`, `library.go:83, 291, 328, 366` · `MkdirTemp`/`RemoveAll` and `librarianEncode` failures leave as untyped errors; wrap in `*SourceError` (or a declared phase 3 type) and `*LibraryWriteError` |
| R1-07 | minor | 6 | fix | Closed 1550dec+wip | Navigate.24, .25 · `navigate.go:324-330` · a picked action that read answers and then failed has its `Result.Answers` dropped, so the next erase is short and levels pile up; count them before the error branch |
| R1-08 | minor | 6 | fix | Closed 1550dec+wip | Navigate.5 · `navigate.go:146-147` · an argument action's `Result.Line` is written raw (two lines for `"a\nb"`); use `oneLine` |
| R1-09 | minor | 1 | fix | Closed 1550dec+wip | review rule · `store_test.go:203` · `TestStore_ClipID` proves `ClipIDOf.1` without naming it; name it |
| R1-10 | minor | 7 | fix | Closed 1550dec+wip | overrule.1, README example 4 · `voices_cmd_levels.go:329` · a clip that never named anyone reads "overruled (3 of 0 namings wrong)" and "named 0 times, 3 wrong"; word it so it reads sensibly |
| R1-11 | minor | 9 | fix | Closed 1550dec+wip | Wire.5, the CLI block's "as today" · `internal/audio/cmd.go:139` · a stray `-json` (or `-from`, `-to`, `-name`) before the file on `audio transcribe` now reads "flag provided but not defined here" instead of "failed to parse flagset: flag provided but not defined". Decided 2026-11-19: the new wording is accepted and Wire.5 and the CLI block are amended, so no body change is left; pin the wording with the four new rows of `Test_e2e_voices_flags_anywhere` (test only) · touches `e2e/interactive/audio_voices_e2e_test.go` |
| R1-12 | major | 6, 7, 9 | fix | Closed 1550dec+wip | Navigate.30, Navigate.31, Navigate.23, voices.6, Wire.6, menu rule 10 (amended 2026-11-19) · `internal/menu/navigate.go:239-265` (`ask` checks ctx only before `table.ReadUserInputFrom`), `internal/cli/compose.go:370-410` · Ctrl-C at a menu question does not leave: a probe then typed `y` + Enter at a delete question and the clip was deleted, and the run reports the interrupt and exits 130. Make a cancelled ctx at a question leave as `q` (`OutcomeLeft`, nil, the read abandoned, nothing acted on afterwards); add `menu.Asking`/`WithAsking`; have the composition take a first SIGINT while `Waiting` as a silent leave (exit 0, no shutdown warning, no interruption error), every other signal as today; say so in `architecture/output.md` · tests `TestNavigate_CancelLeavesAsQuit`, `TestNavigate_AskingOnlyAtAQuestion`, `TestNavigate_Failures` (its cancel row is replaced), `TestVoicesList_InterruptLeavesAsQuit`, `TestRun_InterruptAtAMenuQuestion` · touches `internal/menu/menu.go`, `navigate.go` and tests; `internal/audio/voices_cmd_*_test.go` (and `voices_cmd_tree.go` only if the session needs it); `internal/cli/compose.go`, `interrupt_test.go`; `architecture/output.md` · shares `internal/menu` with R1-15 |
| R1-13 | major | 4 | fix | Closed 1550dec+wip | Librarian.3, Enrol.2, Tally.1, Review.1, Review.3, Remove.1, L4.3 (amended 2026-11-19; widened the same day from `Enrol` to every event writer) · `internal/audio/voices/library.go:82, 151, 218, 246, 286, 341, 376` stamp every event with the raw `l.now()` · after a clock step back, within one clock time, or behind an event dated later than the clock, a call returns nil while the fold (`ReadLibrary.4`–`.6`) ignores its event: a re-enrolled clip still reads as removed, a removed clip stays held, a keep, an overrule or a tally does not count (phase 9's surprise "a fixture clock later than the wall clock hides the route's tallies" is this). Stamp every appended event at the later of the clock's time and one nanosecond after the same clip's latest event in the log, in the one step every member appends through (`librarianEncode`/`librarianAppend`), not in each member and not in phase 1's `EncodeEvent` · tests `TestEnrol_HeldWhateverTheClock`, `TestLibrarian_EventsTakeEffectWhateverTheClock` · touches `internal/audio/voices/library.go`, `librarian_test.go` · shares `library.go` with R1-05 |
| R1-14 | minor | 7, 8 | fix | Closed 1550dec+wip | add.6, move.4, CallWithContext.16, .8, .11, .12 (amended 2026-11-19), Enrol.9 · `internal/audio/voices_cmd_tree.go:131-136`, `voices_cmd_levels.go:437-442`, `pkg/tools/audio_tool_remember_voice_call.go:46-54` · each caller returns `Enrol`'s error and drops the ref, so a clip that was added while the pruned clip's file could not be deleted is reported as a failure (and a move leaves the clip with both people). On a non-zero ref beside a `*LibraryWriteError`: `add` prints the added line and warns through `runenv.Warnf` (`warn [audio] <message>` on stderr), exit 0; `move` goes on and ends its line `; warning: <message>`; the tool answers the frozen line, the overrule line if any, then a last line `warning: <message>`, and still overrules · tests `TestVoicesAdd_PruneLeavesAFile`, `TestVoicesList_MovePruneLeavesAFile`, `TestRememberVoice_PruneLeavesAFile` · touches `internal/audio/voices_cmd_tree.go`, `voices_cmd_levels.go` and their tests; `pkg/tools/audio_tool_remember_voice_call.go` and its tests · needs nothing of R1-05 (the callers' rows wrap the Librarian) |
| R1-15 | minor | 6 | fix | Closed 1550dec+wip | Navigate.21, Navigate.22 (amended 2026-11-19), house rule typed errors · `internal/menu/navigate.go:22-25, 92, 140, 258, 322` · a nil `Opener` or `Run` is the unexported `missingError`, found for `Run` only when the action is picked, and a read failure of `In` is an untyped `diag.Errorf` wrap. Export `InputError{Err}`; add `FaultOpener` and `FaultRun` and check them with the level (`check`), and a nil root before anything is written; remove `missingError` · tests `TestNavigate_MalformedLevel`, `TestNavigate_Failures` · touches `internal/menu/menu.go`, `navigate.go`, the level check and tests · shares `internal/menu` with R1-12 |
| R1-16 | minor | 5 | fix | Closed 1550dec+wip | Name.11 (amended 2026-11-19) · `internal/audio/namer_decide.go:134-150` (`namerSource`) takes the first pick that landed in both orders, else the first in the forward order, whatever its status · a name is reported `unconfirmed` while a confirmed clip supports it. Source is the first confirmed supporting pick (landed with the candidate's label in at least one order) in pick order, else the first supporting pick · test `TestName_SupportNotAmbiguity` cases 3–8 (case 3's expectation changes to the first clip) · touches `internal/audio/namer_decide.go`, `namer_test.go` (the file that holds that test) |
| R1-17 | major | 9 | fix | Closed 1550dec+wip | Output.5, Output.3, Run.5, Run.8, L9.1, F4 (amended 2026-11-19) · `internal/audio/audio.go:162-178` (`renderNaming`), `known_voices.go:44-60`, `querier.go:294`, the tool's engine in `internal/audio` · outside json a naming failure is invisible, and people named without ffmpeg are transcribed and then fail naming. Add `*VoicesNeedFFmpegError` and stop before any request when people are named and `ffmpeg` does not resolve (both routes); on `clai audio transcribe` report each case of L9.1 as its own `runenv.Warnf` warning (`audio`), stdout unchanged, nothing asked; update `architecture/audio.md`; regenerate the golden `voices-named` (stderr gains `warn [audio] no voice "Ada" in the library`) · tests `TestTranscribe_NamingWarnings`, `TestTranscribe_VoicesNeedFFmpeg`, `Test_e2e_AUDIO_voices_without_ffmpeg`, `TestDiagnosticOutputGoldens` · touches `internal/audio/known_voices.go`, `querier.go`, `audio.go`, the engine wiring and their tests; `e2e/` (a new test, `testdata/diagnostics/voices-named.golden`); `architecture/audio.md` |
| A03 | quality andon | root | fix | Closed 1550dec+wip | foreign: `internal/capture/process_test.go:44, 147` `TestKillProcess_EndsAProcessThatIgnoresTheInterrupt` reads the child's `ready` file between its create and its write (`strconv.Atoi: parsing ""`) · `make qa` (review 1, run 1) · that test, once in 2 runs and 0 in 40 alone · 1550dec+wip |
| V5-01 | major | 4 | fix | Closed 1550dec+wip | check 4 · phase 4 `Librarian.3`, last sentence ("The rule has one owner, the step through which every member of the Librarian appends; the phase 1 codec stamps nothing, and no member stamps on its own.") · where the stamping lives inside the Librarian is not caller-observable and no test row can prove it through the interface (as V3-03); end the clause at the observable rule ("…and no line already in the log is changed.") and leave the ownership to L4.3, which already states it, and to R1-13 |
| A04 | design andon | root | plan | Closed 1550dec+wip | maintainer decision 2026-11-19 · the voices CLI hangs under `audio transcribe`, whose one argument is a file, so `voices` is reserved in the file slot (the `./voices` rule; R1-11's hints sending `add`'s flags from transcribe), and `voices`, `add`, `list` have no short names while `t`, `c` and chat's `l` do; `clai audio transcribe` with no file prints one error line where `clai audio` and `clai chat` print their help · move the voices CLI to `audio v\|voices` with `a\|add` and `l\|list`; `audio t\|transcribe` takes only a file again (a file named `voices` is a file); `audio transcribe` with no file prints its help on stderr with the error, exit 1; amend the README and phases 7 and 9 to state it, then file the code work · 2026-11-19 plan: amended (README; phase 7 `voices.1`, `voices.2`, `list.15`, `VoicesKey`; phase 9 `Flag.5`, `Wire.4`–`Wire.6`, new `Wire.7`), code filed as R1-18..R1-21; stays Open until the amended interfaces 7 and 9 are re-validated and signed (major: `voices.1` and `Wire.4` are replaced, not extended) |
| R1-18 | major | 7, 9 | fix | Closed 1550dec+wip | A04 · `voices.1`, `voices.2`, `list.15`, `VoicesKey`, `Flag.5`, `Wire.4` (amended 2026-11-19) · `internal/audio/cmd.go:139` (`transcribe.Subs` holds the voices command), `:193-197` (audio's `c.Subs` lacks it), `:22-23, 50-51` (namespace help), `:120-121, 128` (transcribe `HelpText`), `:61-62` (`OpenVoices` comment), `:98` (audio `Desc`); `voices_cmd.go:10-11` (`VoicesKey = "voices"`); `voices_cmd_tree.go:25-29` (usage and synopsis), `:86` (`Name: VoicesKey`), `:89` (`Subs` `add`, `list`); `voices_cmd_levels.go:16` (empty-library line) · the voices CLI is reached as `clai audio transcribe voices`, has no short names, and reserves `voices` in transcribe's file slot. Hang it under `audio` beside `transcribe`: `VoicesKey = "voices\|v"` in `c.Subs`, the command's `Name` the literal `voices`, its `Subs` `add\|a` and `list\|l`; delete `transcribe.Subs`; every usage, synopsis, help and empty-library line names `clai audio voices …` (the synopsis and `audio -h` say `v`, `a`, `l`); transcribe's help drops the `voices` sub-verb and the `./voices` rule; `audio -h` drops both and lists `v\|voices` · tests: `TestVoices_SubVerb` becomes `TestVoices_Verb` (`voices_cmd_add_test.go:18-30`: short names, `transcribe voices` reaches the stand-in transcribe as a file); the fixture mounts the command beside the stand-in transcribe (`voices_cmd_support_test.go:106, 211`, `voices_cmd_list_test.go:47`); `voices_cmd_list_test.go:382` (the line); `TestTranscribe_EnrolThenNamed` (`known_voices_wiring_test.go:90-95`: `audio -h` names `-voices` and `voices`, `audio transcribe -h` names `-voices` and no `voices add`); `TestVoices_NoVerb`, `TestVoicesList_EmptyLibrary` rows as amended · touches `internal/audio/cmd.go`, `voices_cmd.go`, `voices_cmd_tree.go`, `voices_cmd_levels.go`, `voices_cmd_add_test.go`, `voices_cmd_list_test.go`, `voices_cmd_support_test.go`, `known_voices_wiring_test.go` · one package, one run; R1-20's tests fail until it is done, so run 18, 19, 20 together |
| R1-19 | major | 9 | fix | Closed 1550dec+wip | A04 · new `Wire.7` (2026-11-19), AC 17 · `internal/audio/setup_transcribe.go:141` (`resolveAudioInput`), reached from `transcribe.OnSetup` at `cmd.go:148` · `clai audio transcribe` with no file prints only `error: missing audio file argument, usage: clai audio transcribe <file>`. Print the help `clai audio transcribe -h` prints (`Command.Help`: the help text and the flags) on stderr with that error, exit 1, stdout empty, nothing transcribed and no request, with flags and no file too (`-voices Lorentz`, `-am test`); `-` is a file and is unchanged · tests: new `Test_e2e_AUDIO_transcribe_without_file` (`e2e/interactive/audio_voices_e2e_test.go`), new `TestAudioCommand_TranscribeWithoutFile` beside `cmd_run_test.go:31`'s `missing audio verb` row · touches `internal/audio/cmd.go` (or `setup_transcribe.go`), `cmd_run_test.go`, `e2e/interactive/audio_voices_e2e_test.go` · shares `cmd.go` with R1-18 and the e2e file with R1-20 |
| R1-20 | major | 9 | fix | Closed 1550dec+wip | A04 · `Wire.4`, `Wire.5`, `Wire.6`, `Flag.5` (amended 2026-11-19), AC 11, AC 16 · tests outside `internal/audio` that run the old path: `e2e/interactive/audio_voices_e2e_test.go:195-211` (`Test_e2e_AUDIO_voices_dispatch`: `./voices`, `audio transcribe voices list`, `-voices Lorentz voices list`, the synopsis strings), `:229-231, 249-250` (`Test_e2e_voices_flags_anywhere`), `:272-291` (the stray-flag rows: `'audio transcribe voices add'`, `flag provided but not defined here: `); `internal/cli/menu_interrupt_test.go:167-168, 202, 210` (`TestRun_InterruptAtAMenuQuestion`) · rewrite to the rows as amended: `audio voices list`, `a v l`, `audio v list`, `a v a …`; `audio transcribe -am test voices` transcribes the file named `voices` (no `./`); the `-voices Lorentz voices list` case is deleted (it is now a transcription of the file `voices`); `audio voices` prints the synopsis with `clai audio voices add <wav>` and `clai audio voices list [person`, exit 2; the stray flags read `failed to parse flagset: flag provided but not defined: <flag>` (the line of before this phase; a scratch build of the move at 1550dec+wip prints it) with the hint naming `'audio voices add'`; the interrupt rows run `audio voices list` · no body change · touches `e2e/interactive/audio_voices_e2e_test.go`, `internal/cli/menu_interrupt_test.go` · needs R1-18 |
| R1-21 | minor | 9 | fix | Closed 1550dec+wip | A04 · phase 9 Interface ("`architecture/audio.md`, `architecture/output.md` … are edited in this phase to say the same") · `architecture/audio.md:55, 582-584, 658-659, 674`; `architecture/cmd-dispatch.md:149, 228-236`; `architecture/output.md:68-70` · the three documents describe `audio transcribe voices`, the `./voices` rule and a `-voices` before `voices` left unread. State the tree `audio` → `transcribe\|t`, `voices\|v` (→ `add\|a`, `list\|l`), `calibrate\|c`, `help\|h`; `transcribe` takes a file only and with none prints its help with the error (Wire.7); the `-json` exception of output.md is after `clai audio voices list`; the stray-flag hint names `audio voices add` · documents only · touches `architecture/audio.md`, `architecture/cmd-dispatch.md`, `architecture/output.md` |
| R3-01 | minor | 7, 9 | plan | Closed 1550dec+wip | A04 · `voices.2`, phase 7 synopsis (amended 2026-11-19) · `internal/audio/voices_cmd_tree.go:27-32` · the synopsis that `clai audio voices`, `clai a v` and `clai a v x` print writes its usage lines with the long names (`clai audio voices add <wav> …`) and adds a row `v, a, l the short names of voices, add and list: clai a v l is clai audio voices list`, while clai's other helps write a short name inline beside its verb (`t\|transcribe <file>`, `v\|voices` in `clai audio -h`; `c\|continue`, `l\|list` in `clai chat`; `add\|a`, `list\|l` in the dispatcher's list under `clai a v -h`). The code follows the contract as signed: `voices.2` and the rows of `TestVoices_NoVerb` and `Test_e2e_AUDIO_voices_dispatch` pin `clai audio voices add …`, and phase 7's synopsis keeps the row (line 20) beside inline usage lines (17-18) · amend phase 7's synopsis (drop the row), `voices.2` (`clai audio v\|voices a\|add <wav> …`, `clai audio v\|voices l\|list …`; say whether `add`'s and `list`'s usage lines follow), the `TestVoices_NoVerb` row and phase 9's `Test_e2e_AUDIO_voices_dispatch` row; then `voicesCmdSynopsis` and the substrings at `voices_cmd_add_test.go:75`, `e2e/interactive/audio_voices_e2e_test.go:229-230` |
| R3-02 | minor | 7 | fix | Closed 1550dec+wip | A04 · phase 7 Interface, `VoicesCommand`'s doc (amended 2026-11-19) · `internal/audio/voices_cmd.go:64` · the block reads `VoicesCommand returns the voices command with its verbs add, short a, and list, short l (voices.1).`; the code keeps `… with its verbs add and list.` · write the doc comment as the block states it · touches `internal/audio/voices_cmd.go` |
| R3-03 | minor | 7 | fix | Closed 1550dec+wip | A04 · `TestVoices_Verb` row (amended 2026-11-19), voices.1, AC 1 · `internal/audio/voices_cmd_support_test.go:99-113` · the row's Given is a stand-in `audio` level holding `VoicesCommand` under `VoicesKey` beside a stand-in `transcribe`; the fixture puts both at the top of the dispatched map, so voices.1 is shown at the root, not as a sub-level (phase 9's e2e shows the real descent) · hang the stand-in `transcribe` and `VoicesKey` as `Subs` of a stand-in `audio` command in `commands()` and dispatch the suite's words under it · touches `internal/audio/voices_cmd_support_test.go` (and any row that builds its words without `start`) |
| R3-04 | minor | 9 | fix | Closed 1550dec+wip | A04, R1-21 · phase 9 Interface (`architecture/audio.md` says the same) · `architecture/audio.md:27` · the Entry Flow draws `→ audio → v\|voices → a\|add, l\|list` under `→ querier.Query(ctx)`, as a step of a transcription's query; the voices CLI is dispatched by `cmd.Run` beside `transcribe` and runs no querier · draw it under `→ cmd.Run(...)` at the level of `→ audio → transcribe subcommand` · documents only |
| R3-05 | minor | root | fix | Closed 1550dec+wip | maintainer 2026-11-19: the tests stay fast · `make qa` (`go test ./... -race -count=3 -cover -timeout=30s -p $(QA_PROCS)`) ran `internal/audio` in 24.9–27.3 s of its 30 s per-binary deadline in review 3 and the A04 fix runs · find the slowest tests of `internal/audio` (and of `internal/audio/voices`, `e2e/interactive`) with `go test -json` timings and cut their wall time without weakening what they prove: no skips, no change to the timeout, count, race or `-p` (AGENTS.md); real waits become injected clocks or events, repeated setup is shared, independent tests run in parallel · target: `internal/audio` well under half the deadline under `make qa` · touches test files of those packages and, only where a wait is not injectable, the seam that makes it so |

## Session journal

- 2026-11-18 · plan · README drafted from the reviewed design (notebook `codebases/imago/clai/2026-11-18T08-22-13Z_known-voices-library-investigation.md`) and maintainer answers Q1–Q11; sign-off waits for the measurement note
- 2026-11-18 · validate · V1 Not ready · inspection (no Interface blocks to type-check; grep/sed of Imports members in internal/audio, pkg/tools, internal/tools, internal/utils, internal/testenv, e2e/e2etest, go_away_boilerplate/pkg/cmd): every cited member exists; the assembler seam cannot carry `voices/` clips (V1-03) and phase 7 lacks an import for `transcribe.voices` (V1-04) · notes: 10
- 2026-11-18 · plan · README amended: Status board (phase 3 Library format, data contract; phase 4 Librarian of small interfaces; old phases 4–7 now 5–8), Imports (assembler and mapper dropped; config, flags, querier, engine added), Root facts (F9 package direction, F10 concurrent writers, F11 selection precedence, F3/F4 restated), Flows (one per trigger, two added), Phase-local facts section (V1-02, V1-03, V1-04, V1-10)
- 2026-11-18 · fix · V1-05, V1-06, V1-07, V1-08, V1-09 closed · gates not run: README text only, no code changed
- 2026-11-18 · validate · V2 Not ready · inspection (no Interface blocks to type-check; grep/sed of every Imports member in internal/audio, internal/audio/generic, internal, pkg/tools, internal/tools, internal/utils, internal/testenv, e2e/e2etest, go_away_boilerplate@v1.33.15/pkg/cmd, plus package import directions for F9): every cited member exists under the cited name; V1-01 open by design; V1-06 regressed (Goal and Glossary `Order` still restate L5.1 at the root); no new blocker or major · notes: 10
- 2026-11-18 · plan · README amended: Status board (Library format is phase 1, before the ranker that reads its events; Inspector phase 3, its facts L3.1–L3.3), Librarian methods named for their interfaces (`Enrol`, `Review`), F9 (runner injected into `pkg/tools`), F10 (clip ids by content hash, `voice.json` by exclusive create), F11 (unknown person in the naming report), Imports (`audio:Flags`, `ApplyFlagOverrides`, `MediaToolSettings`), from V2 notes 2–7
- 2026-11-18 · fix · V1-06 closed (Goal, Glossary `Order` and `Naming request` cite L5.1 instead of restating it) · gates not run: README text only, no code changed
- 2026-11-18 · plan · README amended: Glossary (`Event` names `events.jsonl`; `Provenance` is the segment's `voice` field), Status board rows 1, 6, 8 (file names, decimal seconds, `list -json`, the json output with naming), F10 (temporary files and complete lines), from penelope-known-voices V2 (V1-02 regressed, notes 7, 10, 11)
- 2026-11-18 · plan · README amended: Phase-local facts L3.1–L3.3 decided by the maintainer (inspection floors accepted as proposed)
- 2026-11-18 · plan · README amended: Flows redrawn as sequence diagrams with short labels, examples and edge cases moved to prose (maintainer: too dense)
- 2026-11-18 · plan · README amended: Flows, List the library shows `voices list` output (the table and `-json`, ranked clips, the empty library)
- 2026-11-18 · plan · README amended: the voices CLI is `voices add` and the menu `voices list [person [clip [action [args…]]]]` (rm, confirm and overrule are its actions); new phase 6 Menu, so the voices CLI is 7, the tool 8, the root 9; section "The voices menu, by example" holds its behaviour; flows for keep, correct, delete, trim, move and play; F5 follows (maintainer: denormalize by person, macro path as in `config publish`, UX over look)
- 2026-11-18 · maintainer · README signed
- 2026-11-18 · maintainer · decisions for execution: build everything now and measure in parallel, the root's go-live waits on the measurement note (Q16); interfaces count as signed once worklog-validate gives Ready (Q17, pre-authorized); phases on disjoint files run as parallel agents in waves along the dependency graph, agents run only their own packages' tests, and the orchestrator owns the status board, the journal and one full gate run per wave, departing from the one-agent-per-worklog rule (Q18)
- 2026-11-18 · plan · README amended: F5 (a clip `audio_remember_voice` adds is pruned like any other, as Q10' decided; the tool has no remove of its own), Status board row 1 (the Go mirror `store.go`), Flow "The user trims a clip" (`Enrol` is told the clip it replaces), from the phase 1–4 planner's report
- 2026-11-18 · plan · phase 8 written (audio_remember_voice tool)
- 2026-11-18 · plan · README amended: F9 (the tool gets an Inspector and a Librarian through `AudioRememberVoiceDeps`), Imports (tool types, run streams, registry members), Flow "The agent learns a voice" (the frozen result line; an overrule that fails after the add is a second line, not a failure), from the phase 8 planner's report
- 2026-11-18 · plan · phases 6 and 7 written (menu, voices CLI)
- 2026-11-18 · plan · README amended: Imports (table members, run streams and exit status), Status board row 7 (`VoicesCommand`), menu rule 2 (a whole number is the index), rule 8 (each level replaces the one before on a terminal: the maintainer called today's piling-up table awful and wants this menu as the new standard), rule 9 (`-json` is always an array of people), from the phase 6–7 planner's report
- 2026-11-18 · plan · phases 5 and 9 written (speaker namer, root)
- 2026-11-18 · plan · README amended: Phase-local facts now point at the phase files that define them; flags before the file in the user flow, its example and the root human checks (`clai audio transcribe` ignores flags after the file, checked 2026-11-18); max-voices 0 turns the default selection off (phase 4's `Select` already selects nobody under 1); Imports (MockTranscriber; the engine takes `AudioTranscribeCall`), from the phase 5 and 9 planner's report
- 2026-11-18 · plan · README amended: Imports (the `internal/cli` `-json` scan phase 9 touches); phase 4's `ClipOutcome.Landed` and phase 1's `placed` row say "at least one of the two orders", from the phase 5 and 9 planner's report
- 2026-11-18 · plan · README amended: Imports (`audio:Budgets`, `ResolveBudgets` for the naming request's budget), from the phase 5 and 9 planner's revision
- 2026-11-18 · validate · V3 (checks 1–7) Not ready · stub assembly of every Interface block in a copy at 1550dec, `GOPROXY=off go build ./...`: exit 0; `go vet ./...`: exit 0 after adapting the four test call sites of the engine's old signature; `go list` import direction: F9 holds; both schemas valid JSON; inspection of every Consumes member: present · notes: 14
- 2026-11-18 · validate · V3 (checks 8–10) Not ready · inspection of every phase against the README's decisions and the code (`internal/audio`, `internal/testenv`, `internal/cli`, `internal/runenv`, `internal/utils`, `pkg/tools`, go_away_boilerplate `pkg/table`): flows pass, humans pass; V3-50 (the e2e rows' ffmpeg stand-in has no provider) · notes: 13, three marked DECISION
- 2026-11-18 · plan · README amended: F9's Binds line names the members that carry it (V3-01), F6 says "a naming whose two requests were both answered" (as `Name.12` decided), menu rule 6 cites L4.1 (V3-02), example 4's overrule count, Status board row 3 names `voicestest` as test support (V3-04), Imports (`Transcriber` for 5 and 9, `GetClaiConfigDir` for 4, 8 and 9, `TerminalRows`), Human checks (the menu on a real terminal; the agent runs the root's with the maintainer's key, Q8, Q11)
- 2026-11-18 · maintainer · validation round 4 runs on the text changed since V3 ("one more round"); in the menu Enter stops a playing clip, as it stops a recording ("Why not stop it with simply enter?")
- 2026-11-18 · fix · V3-01, V3-02, V3-03, V3-04, V3-50 closed; D1 (phase 9's measurement follows Q8, Q11, Q16), D3 and the structural notes applied in phases 1, 3, 5, 7, 8, 9 and the README · gates not run: documents only
- 2026-11-18 · plan · README amended: examples 1 and 4 and Flow "The user plays a clip" (Enter stops the clip), Imports (`WriteExecutable`), Phase-local facts (L5.4)
- 2026-11-18 · validate · V4 Ready · stub assembly of every Interface block in a copy at 1550dec, `GOPROXY=off go build ./...`: exit 0, `go vet ./...`: exit 0 (four named test call sites adapted); inspection of the changed text: checks 1, 3, 4, 5, 9 pass; V3-01..V3-04, V3-50 resolved; D1–D3 upheld · notes: 10
- 2026-11-18 · fix · V4 notes 1–5, 7, 8 applied: phase 7 test preamble and rows (play answers written after the clip's level shows; play as an argument without a terminal; `Result.Answers` asserted), phase 7 Human required, phase 6 L6.3 (an action's line is echoed too), README Scope (measurement tooling), Phase-local facts pointer · gates not run: documents only
- 2026-11-18 · maintainer · interfaces 1–9 signed (pre-authorized on 2026-11-18 as Q17: the interfaces count as signed once worklog-validate gives Ready)
- 2026-11-18 · maintainer · baseline at 1550dec before wave 1: `make qa`: exit 0, 103 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 clone groups
- 2026-11-18 · work · root · A01 (wave 1 gate: `make qa` ×2 exit 2, `pkg/diagnostic` `TestLocatorInventory` names two `fmt.Errorf` `%w` wrappers in phases 1 and 6; the phases' scoped gates did not run that repository-wide inventory)
- 2026-11-18 · fix · A01 closed (`diag.Errorf` in `internal/audio/voices/store.go` and `internal/menu/navigate.go`) · `make qa` ×2: exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups, none new; `gofumpt -l internal/audio/voices internal/menu`: empty
- 2026-11-18 · work · phase 1 Complete
- 2026-11-18 · work · phase 3 Complete
- 2026-11-18 · work · phase 6 Complete
- 2026-11-18 · work · phase 2 Complete · wave gate: `make qa` ×2: exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-18 · work · phase 4 Complete · wave gate: `make qa` ×2: exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-18 · work · root · A02 (wave 4 gate: `make qa` ×2 exit 2, `internal/audio/voices` timed out at 30 s, CPU-bound; `internal/audio` 26.4 s; phases 5, 7, 8 wait for it)
- 2026-11-18 · fix · A02 closed (cheaper event decoding and per-sample loops in phases 1 and 3; cheaper test fixtures in phases 3 and 8; test names per package unchanged: voices 219, audio 441, pkg/tools 496) · `make qa` ×2: exit 0, 106 packages ok (`internal/audio/voices` 7.4 s and 8.8 s, `internal/audio` 18.5 s and 19.9 s, `pkg/tools` 13.5 s and 13.8 s); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-18 · work · phase 5 Complete
- 2026-11-18 · work · phase 7 Complete
- 2026-11-18 · work · phase 8 Complete
- 2026-11-18 · work · phase 9 waiting on human · built; `make qa` ×2 (second shuffled): exit 0, 106 packages ok (`internal/audio` 21.0 s and 21.3 s); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups; the eight existing goldens unchanged, four new; the measurement needs the maintainer's keys file and the two Daily sync recordings (Q8, Q11, Q16)
- 2026-11-18 · review · root round 1 Not clean (phases 1–9 read; phase 9 waits on the measurement) · reviewer B: `make qa` run 1 exit 2 (`internal/capture` flake, A03), run 2 (`make lint`; `go test ./... -race -count=3 -cover -timeout=30s -p 11 -shuffle=on`) exit 0, 106 ok; `go vet ./...` exit 0; dupl 49 groups; F3 compared on 39 commands against a 1550dec build: identical except R1-11; reviewer A: voices and menu suites ×2 ok · R1-01..R1-11 and A03 filed; phases 4 and 7 Reopened (review 1); six design questions put to the maintainer (Enrol.9 vs Remove.5, Enrol.2 stamping, the menu's two untyped errors, naming failures invisible outside json, Name.11's pick, Ctrl-C in the menu)
- 2026-11-18 · fix · R1-01..R1-04, R1-06..R1-10 and A03 closed (phase files 1, 3, 4, 6, 7, 9 Implementation notes; notebook `codebases/imago/clai/2026-11-18T19-19-31Z_capture-signal-witness-flakes-and-r1-fixes.md`); R1-05 done but for the Enrol prune whose file cannot be deleted, which waits on the maintainer's Enrol.9 vs Remove.5 answer (owner maintainer); R1-11 is no body change, back to plan (owner plan); phase 7 Complete, phase 4 stays Reopened on R1-05 · scoped `go test … -race -count=3 -cover -timeout=30s` ok ×2 (second shuffled); vet, staticcheck, gofumpt clean; dupl 49 groups
- 2026-11-19 · orchestrate · fixes verified in the tree (each closed finding's code present) · wave gate: `make qa`: exit 0, 106 packages ok (`internal/audio` 20.5 s, `internal/audio/voices` 10.9 s, `internal/capture` 14.5 s); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups · line waits on the measurement (phase 9)
- 2026-11-19 · maintainer · review-1 design question "Ctrl-C in the menu": Ctrl-C at a menu question leaves exactly as q does (exit 0, silent; nothing typed after it is acted on), and this is the standard for all menus; Navigate.22 to be amended (plan)
- 2026-11-19 · maintainer · naming failures are handled each on its own, never as one case; when a named person is not matched, the plain `clai audio transcribe` warns "failed to find a match for <name>, were they in the meeting?" and asks nothing (warning only; agent runs already ask the user to label and remember the voice)
- 2026-11-19 · orchestrate · stated to the maintainer, not objected: a named person the library lacks warns `no voice "<name>" in the library`; a failed naming request warns "naming failed: <reason>; speakers left as labels"; people named (`-voices` or the tool's `voices`) without ffmpeg stop before any request ("`-voices` needs ffmpeg", F4 amended: ffmpeg is a dependency, not a naming failure); Enrol stamps `added` after the clip's latest event so a nil error always means held (Enrol.2); a prune whose file cannot be deleted still adds the clip and the callers warn of the leftover file (Enrol.9 follows Remove.5, unblocks R1-05); the menu's read failure and nil Opener/Run get typed errors (Navigate.21); Name.11 prefers a confirmed supporting clip, then pick order; R1-11 accepts the new "flag provided but not defined here" wording (Wire.5 amended)
- 2026-11-19 · plan · README amended: Root facts F4 (ffmpeg is a dependency of naming people; nobody named and no ffmpeg stops nothing), The voices menu rule 10 (Ctrl-C leaves as `q`), Findings (R1-12..R1-17 filed for fix; R1-05 and R1-11 back to fix). Phase files amended for the seven decisions of 2026-11-19: 4 (`Enrol.2`, `Enrol.9`, L4.3), 5 (`Name.11`), 6 (`Navigate.21`–`.23`, new `.30`, `.31`, `InputError`, `FaultOpener`, `FaultRun`, `Asking`), 7 (new `voices.6`, `add.6`, `move.4`), 8 (`CallWithContext.8`, `.11`, `.12`, new `.16`, L8.1), 9 (`Output.3`, new `Output.5`, `Run.5`, new `Run.8`, `VoicesNeedFFmpegError`, `Wire.5`, new `Wire.6`, L9.1, Golden diff `voices-named`). Not done here, for the orchestrator: the Imports rows phase 9 now needs (`internal/runenv:Warnf`; the `internal/cli` composition's signal handling, `runtimeboundary:SignalSource`, `interrupt:SignalError`), re-validation and the maintainer's sign-off of the amended interfaces, and the status board · gates not run: documents only, no code changed
- 2026-11-19 · plan · README amended: Imports (two rows for phase 9: `internal/runenv:Warnf`, `SubsystemAudio`; the `internal/cli` composition's signal handling with `runtimeboundary:SignalSource`, `DispatchMonitor`, `interrupt:SignalError`), Findings (R1-13 widened, no new ID). Phase 4 amended, the orchestrator's call: the stamping rule of L4.3 binds every event writer, stated once in new `Librarian.3` and owned by the Librarian's one append step; `Enrol.2`, `Tally.1`, `Review.1`, `Review.3`, `Remove.1` cite it; new row `TestLibrarian_EventsTakeEffectWhateverTheClock`; phase 9 `Wire.1` cites it · gates not run: documents only, no code changed
- 2026-11-19 · orchestrate · phases 5, 6, 7, 8 Reopened (design 2026-11-19): their signed clauses were amended for the maintainer's answers (R1-12, R1-14, R1-15, R1-16); phase 4 stays Reopened (R1-05, R1-13), phase 9 In Progress (R1-11, R1-12, R1-17) · gates not run: board only
- 2026-11-19 · validate · V5 Not ready · amendments of 2026-11-19 (phases 4–9; README F4, menu rule 10, Imports, R1-05, R1-11..R1-17) at 1550dec+wip · stub assembly of the new Interface members (`menu.InputError`, `FaultOpener`, `FaultRun`, `Asking`, `WithAsking`; `audio.VoicesNeedFFmpegError`) with consumers of `Enrol.9`, `runenv.Warnf`, `CommandRunner.LookPath`, `firstSignal`, `SignalSource`, `interrupt.SignalError` in a scratch copy of the tree, `GOPROXY=off go build ./...`: exit 0, `go vet ./...`: exit 0; inspection of the changed clauses, their consumers, error tables, test rows, goldens and Binds lines against the journal's decisions and the code: checks 1–3, 5–10 pass, decisions stated faithfully; V1–V4 findings still resolved; V5-01 (check 4, `Librarian.3` states where the rule lives) · notes: 7
- 2026-11-19 · fix · V5-01 closed (phase 4 `Librarian.3` ends at "…and no line already in the log is changed."; the ownership sentence is in L4.3 alone, and R1-13 names the step) · gates not run: documents only, no code changed
- 2026-11-19 · plan · README amended: Phase-local facts pointer (L4.3, L9.1), Root facts F11 (cross-reference to F4's ffmpeg stop), Imports (`runenv:Warnf` also used by phase 7), Findings (R1-14 and R1-17 summaries follow). Phase files amended from V5's notes: 6 (`TestNavigate_CancelLeavesAsQuit` words the two cancels that are not at a question on their own), 7 (`add.6` warns through `runenv.Warnf` as `warn [audio] …`, one form with `Output.5`, the orchestrator's call; `move.4` prints nothing apart from its own line), 9 (`Flag.1` and `Run.1` point at Run.8; `TestRun_InterruptAtAMenuQuestion` writes its own blocking `play` script; L9.1 cases 2, 4, 5 no longer marked proposed) · gates not run: documents only, no code changed
- 2026-11-19 · validate · V6 Ready · the edits since V5 at 1550dec+wip (phase 4 `Librarian.3`, L4.3; phase 6 `TestNavigate_CancelLeavesAsQuit`; phase 7 `add.6`, `move.4`, Consumes, L7.4 and their rows; phase 9 `Flag.1`, `Run.1`, `TestRun_InterruptAtAMenuQuestion`, L9.1; README F11, Imports, Phase-local facts pointer, R1-14, R1-17) · inspection of each edit against its clauses, rows, error tables and the code (`runenv.Warnf`, `runenv.New`/`Text`, `internal/diag:NewReporter`; `go list -deps`: `internal/audio` already depends on `internal/diag`, no cycle); no Interface signature changed since V5's stub assembly (build and vet exit 0): V5-01 resolved, checks 1–10 pass on the changed text · notes: 2
- 2026-11-19 · orchestrate · L9.1 cases 2, 4, 5 told to the maintainer, not objected: `voice library unreadable: <reason>; speakers left as labels`, `the transcript has no speaker labels; nobody named`, `no speaker talks long enough to compare; nobody named`, each only when people were named
- 2026-11-19 · maintainer · interfaces 4–9 as amended on 2026-11-19 signed (pre-authorized on 2026-11-18 as Q17: the interfaces count as signed once worklog-validate gives Ready; V6 Ready)
- 2026-11-19 · fix · wave 1: R1-05, R1-13 (phase 4: `Librarian.3` stamping through one step, `Enrol.9` row) and R1-15 (phase 6: `InputError`, `FaultOpener`, `FaultRun`) closed; R1-12's menu part built (`Navigate.23`, `.30`, `.31`, `Asking`), its phase 7 and 9 parts open; phases 4 and 6 Complete · scoped gates per phase file ok ×2 (second shuffled) · wave gate: `make qa`: exit 0, 106 packages ok (`internal/audio/voices` 14.4 s, `internal/menu` 1.7 s 97.2 %); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-19 · fix · wave 2: R1-16 (phase 5: `Name.11` confirmed supporting clip first) and R1-14 (phase 7: `add.6` warns through `runenv.Warnf`, `move.4`; phase 8: `CallWithContext.8`, `.11`, `.12`, `.16`) closed; R1-12's phase 7 part built (`voices.6`, `TestVoicesList_InterruptLeavesAsQuit`), its phase 9 part (`Wire.6`) open; phases 5, 7, 8 Complete · scoped gates per phase file ok ×2 (second shuffled) · wave gate: `make qa`: exit 0, 106 packages ok (`internal/audio` 20.3 s, `pkg/tools` 13.5 s); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-19 · fix · wave 3: R1-17 (phase 9: `Output.5` warnings through `runenv.Warnf`, `Run.8` `VoicesNeedFFmpegError` before any request, L9.1) and R1-12 (its last part, `Wire.6`: one `menu.Asking` per run, a first SIGINT at a question leaves silently with exit 0) closed; golden `voices-named` gains `warn [audio] no voice "Ada" in the library`, the eight committed goldens unchanged · `make qa` ×2 (second `make lint` then `go test ./... -race -count=3 -cover -timeout=30s -p 11 -shuffle=on`): exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-19 · orchestrate · R1-11: the hints the dispatcher now gives for a stray flag before the file are accepted with the wording (a 1550dec build showed `-json`'s hint unchanged, `-from`/`-to` newly pointed at `audio transcribe voices add`, `-name` listing it as a sixth owner, exit 1 throughout); `Wire.5` amended to state them, rows to pin them, no dispatcher change
- 2026-11-19 · maintainer · R1-11: the new hints for a stray flag before the file on `audio transcribe` are accepted (`-json` as before; `-from`, `-to` point at `audio transcribe voices add`; `-name` lists it among its owners; exit 1); Wire.5, the CLI block, the test row and the Error management row amended to state them
- 2026-11-19 · fix · R1-11 closed (four rows in `Test_e2e_voices_flags_anywhere` pin the error line, the hint per Wire.5 and that nothing is transcribed; test only) · `make qa`: exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups
- 2026-11-19 · orchestrate · wave 3 gate (after R1-11): `make qa`: exit 0, 106 packages ok; `go vet ./...`: exit 0; `dupl -t 80`: 49 groups · every finding closed; phases 1–8 Complete, phase 9 built and waiting on the measurement; root review round 2 next
- 2026-11-19 · review · root round 2 Ready (phases 1–8 Complete; phase 9 built, In Progress on the measurement; line left `waiting on human: phase 9`) at 1550dec+wip · start and end, each: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`: exit 0, 106 packages ok (`internal/audio` 21.9 s and 20.6 s, `internal/audio/voices` 11.7 s and 12.1 s); `go vet ./...`: exit 0; `dupl -t 80`: 49 groups · R1-01..R1-17 and A03 verified resolved, none regressed; no new finding · F3 against a 1550dec build: 17 transcription and tool commands in six settings (no library, no ffmpeg, unreadable library, `voices` a file, unconfirmed clips only, `max-voices` 0), stdout, stderr, exit status and the config directory identical but for the random chat id · on the built binary with the real ffmpeg: every example of "The voices menu, by example", the menu on a pty (Ctrl-C at three kinds of question exits 0 with stderr empty and a later `y` deletes nothing; SIGTERM 143; Ctrl-C while a clip plays 130), scripted namings in four formats on both routes, the six warnings of L9.1 and the `-voices needs ffmpeg` stop · stress: the cancel, interrupt, stamping and warning tests 5–40 shuffled race runs ok · Review 2 notes in phase files 4–9 · open with the maintainer, not findings: the measurement, the human checks of nodes 3 and 7, the root human checks
- 2026-11-19 · maintainer · the voices CLI moves from `clai audio transcribe voices` to `clai audio voices`, short `v` (`clai a v`), with `add` short `a` and `list` short `l`; `clai audio transcribe` takes only a file again, as before known-voices, and with no file prints its help as `clai audio` and `clai chat` do · A04 filed, line stopped by A04
- 2026-11-19 · plan · phases 7 and 9 re-planned (A04, major: `voices.1` and `Wire.4` replaced) · README amended: Goal, Flows (the enrol example; a file named `voices` and no file, under "The user transcribes a file with known voices"), The voices menu, by example (the path `clai audio voices`, the short names `v`, `a`, `l`, new example 10, the empty-library line), Imports (`Command.Help`), Human checks 3 and 7, Findings (R1-18..R1-21 filed for fix). Phase 7: synopsis, `voices.1`, `voices.2`, `list.15`, `VoicesKey` (`voices|v`), AC 1, `TestVoices_Verb` (was `TestVoices_SubVerb`). Phase 9: CLI block, `Flag.5`, `Wire.4`, `Wire.5` (the stray-flag line is again the one of before this phase, `failed to parse flagset: flag provided but not defined: <flag>`, because `transcribe` has no verbs below it; the hints name `audio voices add`), `Wire.6`, new `Wire.7` (no file: help and error on stderr, exit 1), AC 11, 16, new 17, rows and Error management. Not done here, for the orchestrator: status board rows 7 and 9 (their Interface cells still name `clai audio transcribe voices`), the phases' statuses, re-validation and the maintainer's sign-off of interfaces 7 and 9, on which A04 closes · gates not run: documents only, no code changed
- 2026-11-19 · orchestrate · A04 replanned (R1-18..R1-21 filed, phases 7 and 9 amended); board rows 7 (Reopened) and 9 state the new tree; Penelope's code already follows (its journal) · re-validation of interfaces 7 and 9 next
- 2026-11-19 · validate · V7 Ready · the A04 amendments of phases 7 and 9 and the README (Goal, both Flows, The voices menu by example incl. example 10, Imports, Human checks 3 and 7; R1-18..R1-21, A04) · `grep -n "transcribe voices\|a t voices\|\./voices" *.md`: the only live hits are README example 10 (`a t voices`, a file) and phase 9 Wire.4 (a file named voices); the rest are amendment notes, closed findings and journal lines · stub assembly: inspection (VoicesKey, VoicesCommand, Command.Subs/Help exist in internal/command.go; Wire.7 consumes Help) · flows 8, provenance 9, humans 10, tests 4: every amended clause and AC 1, 11, 16, 17 has rows; no blocker or major · notes: 1 (Wire.7 "with or without flags" means flags transcribe defines; a stray -json/-from/-to/-name is Wire.5)
- 2026-11-19 · maintainer · interfaces 7 and 9 as amended for A04 signed (pre-authorized on 2026-11-18 as Q17: the interfaces count as signed once worklog-validate gives Ready; V7 Ready)
- 2026-11-19 · orchestrate · A04 closed (replanned, V7 Ready, signed); line running for R1-18..R1-21, one fixer run
- 2026-11-19 · fix · R1-18, R1-19, R1-20, R1-21 closed: the voices CLI is `clai audio v|voices` (`a|add`, `l|list`; `VoicesKey = "voices|v"`, the command's `Name` the literal `voices`), `transcribe` holds no command and takes a file only, with no file it prints its help and the error on stderr, exit 1 (`Wire.7`; new `TestAudioCommand_TranscribeWithoutFile`, `Test_e2e_AUDIO_transcribe_without_file`); `TestVoices_Verb`, the e2e dispatch, flag and interrupt rows on the new path; the stray-flag line is `failed to parse flagset: flag provided but not defined: <flag>` with the hint naming `'audio voices add'`; architecture `audio.md`, `cmd-dispatch.md`, `output.md` restated; the audio `Desc` left as is (help.golden unchanged, Golden diff) · `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`: exit 0, 106 packages ok (`internal/audio` 25.5 s, `internal/cli` 8.3 s, `e2e/interactive` 9.3 s), at start and end; `go vet ./...`: exit 0; `go run github.com/mibk/dupl@latest -t 80 .`: 49 groups
- 2026-11-19 · review · nodes 7, 9 (A04) round 3 Not ready (no blocker or major; four minor open: R3-01 plan, R3-02..R3-04 fix) at 1550dec+wip · start: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`: exit 0, 106 packages ok (`internal/audio` 27.3 s, `internal/audio/voices` 19.6 s, `internal/cli` 9.1 s, `e2e/interactive` 9.8 s); `go vet ./...`: exit 0; `go run github.com/mibk/dupl@latest -t 80 .`: 49 groups · end (dupl, vet, qa): 49 groups; exit 0; exit 0, 106 packages ok (`internal/audio` 26.6 s, `internal/audio/voices` 18.8 s); then `go test ./internal/audio/ ./internal/cli/ ./e2e/interactive/ -race -count=3 -timeout=30s -p 6 -shuffle=on`: ok · R1-18..R1-21 verified resolved, none of R1-01..R1-17 regressed · on a built binary (scratch `CLAI_CONFIG_DIR`, keys unset, `-am test`): `audio transcribe`, `a t -am test -voices Lorentz -af json`, `audio transcribe --` exit 1 with the `-h` text and the missing-file error on stderr, stdout empty, no config written, and as `output`/`error` records under `-json`; `a t -am test voices` transcribes the file `voices`; `a v`, `audio voices x` exit 2 with the synopsis; `a v l`, `a v l -json` on an empty library; stray `-from`/`-name` before the file name `'audio voices add'`; completion offers `v voices` and `a add l list` · Review 3 in phase files 7 and 9
- 2026-11-19 · maintainer · `clai -h` lists the voices library: the audio line reads `Transcribe and record audio: audio t|transcribe <file> ('-' reads stdin), audio v|voices, audio c|calibrate`; `help.golden` changes by that line, an exception to phase 9's Golden diff (review 3 note on `cmd.go:98`)
- 2026-11-19 · maintainer · "We need to keep the tests nice, fast and spiffy": R3-05 filed for the `internal/audio` test time (24.9–27.3 s of the 30 s deadline); the maintainer re-reviews the result himself after the implementation
- 2026-11-19 · fix · R3-01's plan part done in the fix run: phase 7's synopsis (the `v, a, l` row dropped), `voices.2` (synopsis and `add`/`list` usage lines `clai audio v|voices a|add <wav> …`, `clai audio v|voices l|list …`; the empty-library line keeps the long form), the `TestVoices_NoVerb` row and phase 9's `Test_e2e_AUDIO_voices_dispatch` row amended 2026-11-19. The orchestrator chose the form (as `t|transcribe` in `clai audio -h`, `l|list` in chat); the review's re-check stands for the re-validation of this wording
- 2026-11-19 · fix · R3-01, R3-02, R3-03, R3-04 closed (synopsis and usage lines inline; `VoicesCommand` doc as the block; the suite dispatches under a stand-in `audio`; `audio.md` Entry Flow draws the voices branch under `cmd.Run`); with the maintainer's audio `Desc` (`audio v|voices`), `help.golden` regenerated, only its `audio|a` line changed, phase 9 Golden diff and AC 10 amended for the exception · `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`: exit 0, 106 packages ok at start (`internal/audio` 22.3 s) and end (`internal/audio` 23.5 s); `go vet ./...`: exit 0; `go run github.com/mibk/dupl@latest -t 80 .`: 49 groups
- 2026-11-19 · orchestrate · R3-01..R3-04 closed by the fixer (`make qa`: exit 0, 106 packages ok; `go vet`: 0; `dupl`: 49 groups); phase 7 Complete on the board; R3-05 (test speed) in fix; the maintainer re-reviews after
- 2026-11-19 · fix · R3-05 closed (`internal/audio`: the record rows stop on the capture's first sound (`heardRecorder`) instead of 150 ms sleeps; `TestRecordText_Facts` runs the real recorder with a 300 ms duration stop after checking the request carries the file's 1 s; the stop-file rows poll every 10 ms through the new unexported `stopSources.poll` (zero keeps `stopFilePoll`); the end-of-input row and `TestRecordText_JSONProgress` wait for events, not 50 ms; rows that touch no process state run in parallel (`TestStopOnFile`, `TestEnterStop_EOFIsNotALine`, `TestCoordinatorLiveBoardReadsStatesSafely` without `plainStatus`, four calibration rows, two splitter rows); `voicestest` copies samples in one move on little-endian hosts, pinned by `TestVoicestest_PCMInOneMove`. The voices CLI suite stays serial: `internal.Command.Setup` writes `utils.ReadonlyConfig`, `NoCreateConfig` and `Live`, which -race flags when two runs dispatch at once) · start: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`: exit 0, 106 packages ok (`internal/audio` 27.0 s, `internal/audio/voices` 18.0 s, `e2e/interactive` 9.9 s); `go vet ./...`: exit 0; `go run github.com/mibk/dupl@latest -t 80 .`: 49 groups · end: `make qa` (keys unset): exit 0, 106 packages ok (`internal/audio` 16.1 s and 16.5 s with the host at load average 5–7 from outside the sandbox, 12.0 s in a quieter run before the last helper refactor; back to back under that load `go test ./... -race -count=3 -cover -timeout=30s -p 11` ran `internal/audio` in 22.2 s and 22.7 s on the tree before the fix, 14.4 s and 13.7 s on this one); `go vet ./...`: exit 0; dupl: 49 groups; `go test ./internal/audio ./internal/audio/voices ./internal/audio/voices/voicestest ./e2e/interactive -race -count=10 -shuffle=on -timeout=300s`: ok · `go test -race -count=1 -json` alone: `internal/audio` 7.3 → 4.2 s, `internal/audio/voices` 3.0 → 2.5 s, `e2e/interactive` 3.1 → 2.8 s · left as real work: three `board.TickInterval` in `TestCoordinatorLiveBoardReadsStatesSafely` (now overlapped), capture's 250 ms `cancelArrivalGrace` in `TestRecordText_RecorderError`, the 1 s silence timeout of `Test_e2e_meeting_transcriber_profile`, and the CPU of the voices CLI suite
- 2026-11-19 · orchestrate · R3-05 verified (`record.go` gains one unexported poll field defaulting to `stopFilePoll`; `go test ./internal/audio -race -count=1`: ok 4.4 s alone) · every finding closed (A04, R1-18..R1-21, R3-01..R3-05); line back to waiting on human: phase 9 (the measurement); the maintainer re-reviews the A04 and R3 changes himself
- 2026-11-23 · plan · phase 5 Name.15 amended (Lorentz: "do the final improvements", after the Retro in Penelope spent 77 s + 70 s on naming's two orders in series): both requests are built, then sent at once; a failed send cancels the other; Err prefers the forward order's failure; patch to a Complete phase, sign-off needed
- 2026-11-23 · maintainer · phase 5 Name.15 amendment signed (Lorentz: "Sign, build now")
- 2026-11-23 · review · Name.15 amendment reviewed Ready (8 minor, none blocking); `TestName_NeverFails` row brought in line with the amended clause (it is `TestNamer_FailedRequestLeavesTheTranscript`, `TestNamer_SendsBothOrdersAtOnce` and `TestNamer_AFailedOrderCancelsTheOther` in the code)
