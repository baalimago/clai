# Model aliases and fallback

A profile that is shared between machines cannot name one model: the machines
differ in vendor keys, in account credit and in which models the vendor still
serves. **Model fallback** solves this. A configuration or profile names a verb
such as `strong`, `fast` or `cheap`, and each machine resolves that verb to its
own ordered list of concrete models. The run tries the models in order, and the
first one that answers supplies the output.

The feature is four small seams, each with one concern:

| Seam | Package | Concern |
| --- | --- | --- |
| Data | `internal/modelalias` | a verb resolves to an ordered model list |
| Mechanism | `internal/retry` | walk an ordered attempt list, stop at the answer |
| Vocabulary | `pkg/claierr` | which error means "this model could not answer" |
| Composition | `internal/modelfallback` | compose one run per model, guard the replay, report the notice |

A mode wires fallback with a single call to `modelfallback.New`, so the
mechanism has no knowledge of text, photo or audio.

## Data: the alias table

`modelalias.Table` is `map[string][]string`: a verb to the ordered models a run
tries in its place. Each mode's config carries its own table under
`model-aliases`, because a chat model cannot talk to an image vendor:

```json
{
  "model": "strong",
  "model-aliases": {
    "strong": ["claude-sonnet-5-5", "gpt-5.2", "gemini-3.6-flash"]
  }
}
```

`Table.Resolve` returns the ladder for a name the table holds, and the name
itself as a one-model ladder for a name it does not. That second rule is what
keeps an unconfigured table invisible: every model that is not a verb behaves
exactly as it did before fallback existed.

Resolution is strict, because a shortened ladder hides which machine setting is
wrong:

| Problem | Error |
| --- | --- |
| the configured model name is empty | `ErrEmptyModel` |
| the verb lists no models | `*InvalidAliasError` with `NoCandidates` |
| a candidate is empty or all space | `*InvalidAliasError` with `BlankCandidate` |
| a candidate is the verb itself | `*InvalidAliasError` with `SelfCandidate` |
| a candidate is another verb | `*InvalidAliasError` with `AliasCandidate` |

An alias inside an alias is refused on purpose. The ladder the user reads must
be the ladder the machine runs, so a candidate is always a concrete model.

The shipped ladders in `text.Default`, `photo.DEFAULT` and `audio.Default` are a
starting point the user owns from the first write. They name one model per
vendor or per route, so a machine with any one key still answers. The config
migration seeds them once and never rewrites an existing table (see
[config.md](./config.md)).

### Remote model alias lists

A profile's `"model"` field, a mode's `"model"` setting or the `-cm` / `-pm` / `-am`
flags may also name a remote model list (`cchub:<handle>/<alias>[@<version>]`,
`gh:<owner>/<repo>[/<path>][@<ref>]` defaulting to `models.json`, or `https://...`).
The remote document is a JSON array of model names:

```json
["claude-opus-4-7", "gpt-5.4", "gemini-3.1-pro-preview"]
```

`remote.ResolveModelList` (`internal/remote/models.go`) fetches, validates,
deduplicates in first-seen order, caches in `<cache>/remote/` (honoring
`CLAI_CLOUD=off`) and verifies an optional `#sha256=<hex>` suffix. Because model
names are inert strings matched against local vendor configurations, no
interactive trust prompt or `trust.json` entry is required. Local aliases are
published to the Hub with `clai cloud publish alias <name> [mode]`. Each mode
config (`textConfig.json`, `photoConfig.json`, `audioConfig.json`) has its own
table, so the same name can mean different ladders: `clai cloud publish alias
strong photo` publishes the photo one. Without the mode, an alias in several
mode configs is asked for in a selector (modes sorted by name, so `0` is
`photo` and `1` is `text` when both define it). `clai cloud publish` and
`clai cloud publish alias` ask for each missing level in turn: the kind, the
alias, then the mode. The output names the mode's flag (`-chat-model`,
`-photo-model` or `-audio-model`).

## Mechanism: the retry walk

`retry.Do` takes an ordered list of `retry.Attempt[T]`, a `retry.Policy` and a
`retry.Notice`, and returns the first answer. It holds no clai vocabulary and
prints nothing, so it is tested with plain functions and plain errors.

The policy answers one question: may the next attempt succeed where this one
failed? The walk ends on four conditions: an answer, a failure the policy
rejects, a failure wrapped in `retry.Final`, and an exhausted list. A
cancelled context also ends it, and the returned error joins the context error
with the failures already collected.

A chain that fails completely returns `*retry.ChainError`, which reports every
attempt in run order:

```text
claude-sonnet-5-5: model not found; gpt-5.2: likely insufficient credits
```

Each entry carries the provider's own facts (status, provider code, message),
so the final error says everything the notice holds back.

`ChainError.Unwrap` returns every cause, so `errors.Is` and `errors.As` see
through the chain and a caller's own error handling keeps working.

A chain of one attempt returns that attempt's value and error unchanged. This
is the backwards-compatibility rule that lets an unaliased model keep its
exact error text.

## Vocabulary: which failures are worth a second model

`claierr.ModelUnavailable` is the one place that states the meaning. It is true
for a retired or unknown model, a rejected key, an empty account, a throttled
account and a failing provider (`ErrModelNotFound`, `ErrAuthFailed`,
`ErrLikelyInsufficientCredits`, `ErrRateLimited`, `ErrProviderUnavailable`).

It is false for an answer about the request itself or about the network under
every model: a transport failure, an unexpected payload, an over-long context
and a content-policy refusal. Another model would fail the same way, so the
failure is reported to the caller unchanged.

## Composition: one run per model

`modelfallback.New(ctx, table, model, build, notice)` resolves `model` through
`table` and returns the `models.Querier` to run. The mode supplies `build`: a
function that composes its own complete run for one model.

The retry table for one attempt:

| Failure | Next model tried? | Reason |
| --- | --- | --- |
| the run cannot be composed (missing key, unknown vendor, refused option) | yes, always | the failure is about this model alone |
| the run failed with a `claierr.ModelUnavailable` error and produced nothing | yes | another model may answer |
| the run failed after it printed output or caused a side effect | no, the failure is final | a replay would duplicate the reply, the file or the tool call |
| the run failed with any other error | no | the vocabulary says the request, not the model, is the problem |
| the context was cancelled | no | the walk stops and reports the prior failures |

One candidate is composed immediately and returned unwrapped, so a run whose
model is not a verb keeps the querier type, the bell policy and the error text
it had before fallback existed. Several candidates return a chain that composes
each model only when it is that model's turn.

### The replay guard

A run that already produced something must never be replayed. The chain asks
the composed querier, through an optional method:

```go
Produced() bool
```

A querier that does not implement the method is treated as having produced, so
the default is the safe one. The implementations are:

| Querier | `Produced` is true when |
| --- | --- |
| text `*Querier[C]` | text streamed to the user, or it ran a tool |
| text `*loopRunner` | the second iteration began, otherwise the inner querier's answer |
| photo `*query` | it saved at least one image |
| audio `*TranscribeQuerier` | never asked: it has no method, so it is treated as having produced |

The text querier records production from the session it ran, never from its
finalize latch: finalizing a failed run is bookkeeping, not output, so a run
that died before its first token hands over to the next model as it should.

An attempt that fails after producing wraps its error in `retry.Final`, which
ends the chain whatever the policy says.

### The notice

Every advance is reported on stderr and nothing else changes, so a script that
reads stdout sees the same bytes as before. The line is clai's shared look
(`internal/look`), written through `look.For(stderr)` so it is themed in a
terminal and plain in a pipe:

```text
▸ model gpt-5.2 unavailable · likely insufficient credits · trying gemini-3.6-flash
```

The reason is one phrase, never the provider's prose: the `claierr` meaning when
the failure carries one (including unknown-model routing errors, which unwrap to
`claierr.ErrModelNotFound`), else the innermost cause (so a setup layer's
wrapping is dropped). The full text stays in the final error, which reports
every model the chain tried. The reason is folded onto one line, so a multi-line
cause cannot break the layout.

A notice is best effort: a failed write is dropped, because a diagnostic must
never change the outcome of the run. A plain model and a ladder whose first
model answers print nothing.

## The completion bell

The bell asks the querier that finished whether it suppresses the bell. A chain
is not the run that answered, so `internal/command.go` walks the chain down
through its `Answer()` method before it reads the policy. Without that walk, a
run that fell back would answer with the chain's zero policy instead of the
policy of the model that ran.

## Per mode

| Mode | Composed per attempt | Fallback in practice |
| --- | --- | --- |
| text (`clai q`, `clai -p <profile>`) | vendor querier, reply-only, summarizer, summary join timeout, image turn, loop runner and run tally | compose and run, whenever the failure means the model could not answer |
| photo (`clai p`) | the image generator, over the request, output and presenter collected once | compose; also at run time for a typed vendor failure that saved no image |
| audio (`clai a t`) | the transcriber route for that model | compose, which is where a missing vendor key appears |

Text is the interesting case: the query command composes the whole decoration
chain per attempt, so the model tried second is configured, tooled and tallied
exactly like the model tried first. Two things belong to the run and not to an
attempt: the summarizer, which is built once and attached to each attempt
because its label index is per run, and the subagent tally scope, which wraps
the resolved chain so that closing it cannot hide the replay guard of the
querier an attempt composed.

Audio keeps the transient input, which is the temp file a `-` run reads, out of
the attempts. The chain cannot remove it: a candidate that fails to compose must
leave the file for the next one. `setupTranscribeQuerier` wraps the resolved
chain in a querier that removes the file once, after the run ends, whatever the
outcome.

### What is not wired, and why

- **`clai video`** has one vendor (`sora-2`) and the vendor reports failures
  without the typed vocabulary, so a video ladder would have nothing to walk
  between. The seam is ready if a second video vendor lands.
- **The `generate_image` tool** and the **`audio_transcribe` tool** are tool
  bridges that take one concrete model from their own config. A tool call is
  inside a text run, so it inherits the text run's fallback, not its own.
- **`clai chat summarize`** already has a two-step model ladder (`summary-model`
  then `model`), which is a different mechanism: it degrades to a weaker task,
  not to another vendor.

## Implementation map

| File | Role |
| --- | --- |
| `internal/modelalias/modelalias.go` | `Table`, `Resolve` and the typed alias errors |
| `internal/retry/retry.go` | `Attempt`, `Do`, `Policy`, `Notice`, `Final`, `ChainError` |
| `pkg/claierr/claierr.go` | `ModelUnavailable` and the sentinel vocabulary it reads |
| `internal/modelfallback/modelfallback.go` | `New`, the chain, the replay guard and `NoticeTo` |
| `internal/look/look.go` | `Style.Head`, which draws the notice |
| `internal/command.go` | `answeringQuerier`, which walks the chain for the bell |
| `internal/text/cmd.go` | `textSession` and `buildTextRun` |
| `internal/photo/cmd.go`, `internal/photo/query.go` | `forModel`, which composes one generator |
| `internal/audio/setup_transcribe.go` | `transcribeChain` and `cleanupQuerier` |
