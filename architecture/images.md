# Images

Images are a kind of message content on every querier, in both directions. A user attaches an
image with a flag, a glob or stdin, and a tool can return one. A model makes one through clai's
`generate_image` tool, which runs on the same image generators as `clai photo`, on every vendor
whose model takes tools.
A chat model whose output is images (an OpenRouter image model) answers with an image directly. Image bytes live as files in a content-addressed
store; conversations hold only references, so their JSON stays small and readable, and base64
exists only while a request body is built or a response is read.

A text-only conversation, request and output is byte-for-byte what it was before images, and
every conversation saved before images still loads.

## Vocabulary

| Term | Meaning |
| --- | --- |
| Part | One piece of message content: `TextPart` or `ImagePart` (`pkg/text/models/parts.go`). A message holds either `Content` or `Parts`. |
| Asset ref | An image's file name in the asset store, `<sha256-hex>.<ext>`; the only image identity a conversation persists. |
| Handle | The first 6 hex digits of the image's SHA-256. The user and the model both see it. |
| Stored / pending | An `ImagePart` with an asset ref, or with its bytes in memory and no ref yet. A pending image becomes stored when a model step begins or the conversation is saved. |
| Origin | `user` (attached), `tool` (returned by a tool) or `generated` (made by a model or a generator). |
| Label | The text part `[image <handle>]` placed in front of every image sent to a model. |

## The CLI surfaces

```text
clai [flags] query|q <prompt>
  -img, -image <path|glob>            attach image(s); repeatable
  -g, -glob <glob>                    image matches attach as images, other files as text
  stdin                               wholly an image (raw or base64) → attached
  -t generate_image | view_image      the image tools; a pattern selects them as any tool
                                      ('*', '*_image'), from -t or a profile's tools
  -open                               open each generated image
  -pm, -photo-model <model>           the model behind generate_image for this run
clai photo [-img <path|glob>]... [-open] <prompt>
clai video [-img <path|glob>] <prompt>
clai re | dre                         an image prints as "[image <handle>] <asset path>"
clai chat list | continue | delete    previews show "[image <handle>]"; delete sweeps assets
```

Loop mode (`-l`) takes the same flags. Each generated image is shown once on stdout. A native image
(from an image-output chat model) and a `clai photo` image get a line of their own; on a colour
terminal:

```text
▸ [🐕 🎾 🎨] image 25c1ba · gpt-image-2.5-flare · 1536×1024 (3:2) · 1.5 MB · 41s → file:///…/25c1….png
```

The URL is percent-encoded, so terminals open it on ctrl-click, and it is also an OSC 8 link. The
rebus is the emoji the model passed to `generate_image`, asked for as three and shown when it is
one to three emoji. The details (`present.Details`) are the image model (for an image-output chat
model, the chat model), the size and its ratio (PNG, JPEG and GIF; `asset.Measure` reads the
header), the file size (SI units) and how long the generation took; each is left out when
unknown.

An image a tool generated (`generate_image`) is shown in that tool's block instead, as two rows
in place of `generated image … saved at …` and `[image <handle>]` (the model still receives
both), coloured and linked the same way:

```text
▸ generate_image  input_images=[]  prompt=A whimsical photorealistic food photograph …
  ▸ [☁️ 🛋️ 🍽️] image 32259a · gpt-image-2.5-flare · 1024×1024 (1:1) · 1.2 MB · 38s
  → file:///home/u/.config/.clai/conversations/assets/32259a….png
```

That block stays on screen: with the rolling activity window on, the window's rows so far stay
where they are, the block follows them, and later activity starts a new window below it. Its image
rows are never compacted or cut, so the terminal wraps a long URL, and each image's revised prompt
follows its own rows. With debug on, the image gets its own line instead. When stdout is not a terminal, or in raw
mode, an image's line is the bare absolute path, so
`clai photo a fox | xargs xdg-open` works. With structured output (`-rf`) no line is printed, so
stdout holds only the JSON. A viewer that fails to open is one notice on stderr and never fails the run.

## Attaching an image to a query

```text
clai -img cat.png q "what breed?"
clai query                                   internal/text/cmd.go, query_images.go
 |-> ImageCollector.Collect(-img, -g, stdin, args)        internal/imageinput
 |   <- pending ImageParts (origin user), prompt text, glob file messages
 |-> user message: Content, or Parts [images..., TextPart(prompt)]
 |-> TurnRunner.Run(session)                              internal/text/turn*.go
     |-> AssetStorer.Put(bytes, mime), per pending image  internal/asset
     |-> Normalizer.Normalize(chat, ImageCapabilities())  internal/imagenorm
     |   |-> ImageLoader.Load(ref), per image
     |   <- labelled chat, images relocated, or a typed refusal
     |-> StreamCompletions(normalized chat)               the vendor's encoder
     |-> Persister.Promote(chat)                          internal/chat/persister.go
         <- refs only on disk
```

The collector expands `~` and the glob, sniffs the MIME type from the bytes, refuses a non-image
with `asset.ErrNotImage` and an image over 20 MiB with `*asset.ImageTooLargeError` (an `-img` file
before reading it, a `-g` match or stdin once read), and fails a pattern that matches nothing with
`*imageinput.NoMatchError`. It never extracts
base64 from inside prompt text. Each vendor declares what its route accepts
(`models.ImageCapabilities`): images in user turns or none, and tool-result images natively or by
fallback, where they travel in a synthetic user message. A route that accepts no images refuses a
user image with `*claierr.ImageUnsupportedError`, naming the vendor and the model, before any HTTP
request. A tool or generated image reaches such a route as its `[image <handle>]` text, without
bytes, so a saved tool image never fails a later turn there. The details are in
[query.md](./query.md#image-input-collection) and [streaming.md](./streaming.md#image-content).

## Getting images back

```text
TurnRunner.Run(session)
 |-> Gen setup, when the tool selection matches generate_image: register the tool
 |-> generate_image.CallParts(prompt, rebus, input_images, size)
 |   |-> NewGeneratorFunc(Model, ConfigDir) → ImageProgress.Start → ImageGenerator.Generate → Stop
 |   |-> AssetStorer.Put → tool message Parts [TextPart, ImagePart(generated)]
 |   <- presented with the call's rebus when the tool returns
 |-> image-output chat model: ImageProgressEvent → ImageProgress.Start
                              ImageEvent → Stop → AssetStorer.Put → Presenter.Present → assistant ImagePart
```

The tool runs on `imagegen.ImageGenerator` backends for OpenAI image models, OpenRouter (`or:`
prefix) and Gemini, picked by `route.NewGenerator` from the model name (see
[photo.md](./photo.md#image-generators)). Its model is `-pm`; otherwise
`route.ImageModelFor` prefers the chat model's own vendor: beside an OpenAI, Gemini or OpenRouter
chat model it keeps `photoConfig.json`'s `model` when that is the same vendor's, and otherwise
uses that vendor's default (`gpt-image-2.5-flare`, `gemini-3.1-flash-image`,
`or:google/gemini-3.1-flash-lite-image`). Beside any other vendor it uses `photoConfig.json`'s
`model`. The vendors' built-in image tools (such as the Responses `image_generation` tool) are
never enabled, so every generated image goes through one tool, its arguments and its result.
The tool result names the image's path, so the model can use the file. OpenRouter models whose
catalog lists image output still answer with images directly.
`input_images` takes handles the model saw in labels, or local paths. A generated image is stored
and presented the moment it arrives, so a later failure in the turn never loses it.

While an image is generated, a terminal run that is not raw shows one live row in the look of the
audio board (`board.Status`, [board.md](./board.md)): `▸ generating image  ⠹ 12s`. The tool
shows it while its generator runs. An image-output chat model starts it on
`models.ImageProgressEvent`, which OpenRouter emits as the first event of a stream that asks for
images. The row is erased before
anything else is written, and the presented image line takes its place. After streamed text that
left its row open, the row starts on the next line. See
[tooling.md](./tooling.md#image-results) and [query.md](./query.md#images-in-a-turn).

## On disk

Assets live in `<clai-config>/conversations/assets/`, one file per distinct image, named by its
ref, so they open directly. A conversation's JSON holds
`{"type":"image","asset":"<ref>","mime":…,"origin":…}` parts. A conversation saved before images,
with `image_url` data URLs, loads with those images as pending parts and is rewritten with refs on
its next save, never before. `clai chat delete` sweeps every asset that no remaining
conversation file references, `globalScope.json` included. Every reader of a conversation the
user continues (`-re`, `-dre`, `chat continue`, replay) goes through `chat.Persister.Load`. See
[chat.md](./chat.md#conversation-persister-internalchatpersistergo).

## Text surfaces

A surface that shows only text renders an image as `Message.Describe()`, `[image <handle>]`: the
`chat list` preview, the `chat continue` preview, the lookback tools, the summarizer transcript,
and the `-re` context of `photo` and `video`. Replay and `dre` add the path:
`[image <handle>] <absolute asset path>`, or `[image <handle>] (not stored yet)` for a legacy
image. The message editor edits only the text of a message and keeps its images in place.

## Configuration

| Key | File | Default | Flag |
| --- | --- | --- | --- |
| `auto-open-images` | `photoConfig.json` | `false` | `-open` |
| `image-opener` | `photoConfig.json` | `""`: `xdg-open` on Linux, `open` on macOS | — |
| `model` | `photoConfig.json` | `gpt-image-2.5-flare`; `generate_image` prefers the chat vendor's backend (above) | `-pm` |

Every command that shows an image, `clai photo` and `clai q` alike, reads these keys from
`photoConfig.json`. The keys reach existing configs through the usual config upgrade. No default
changes what an existing setup does. See [config.md](./config.md#image-configuration).

## Tests never spend money

Every image generator is built through an injected `imagegen.NewGeneratorFunc`:
`cli.Deps.NewImageGenerator`, which is `route.NewGenerator` in `DefaultDeps`. Every e2e
`TestMain` injects `e2etest.RefusingImageGenerator`, so a test that reaches `generate_image`
gets an `ERROR:` tool result, and only a test that opts in with
`Harness.UseFakeImageGenerator` gets a fake. Selecting `generate_image` builds nothing until the
model calls it.

## Package map

| Package | Role |
| --- | --- |
| `pkg/text/models` (`parts.go`) | `Part`, `TextPart`, `ImagePart`, `Describe`, handles, the JSON codec and the legacy decode |
| `internal/asset` | the content-addressed store: `Put`, `Load`, `Resolve`, `Path`, `Sweep`, `Sniff` |
| `internal/chat` (`persister.go`) | `Persister`: `Save`, `Promote`, `Load`, `Delete` with the sweep |
| `internal/imageinput` | `ImageCollector`: `-img`, `-g` and stdin |
| `internal/imagenorm` | `Normalizer`: labels, relocation and refusals per route capability |
| `internal/models` (`images.go`, `image_output.go`) | `ImageEncoder`, `ImageCapabilities`, `ImageEvent`, `ImageProgressEvent` |
| `internal/vendors/*` | each vendor's capabilities and wire encoding; OpenRouter decodes image-output models' images |
| `internal/imagegen`, `imagegen/route`, `imagegen/tool` | `ImageGenerator`, the backend router, the `generate_image` tool |
| `internal/present` | `Presenter` (the printed line, the tool-block `Rows`, `Open`) and the command opener |
| `internal/text` (`turn.go`, `turn_images.go`, `query_images.go`) | the turn's image flow and the query command's wiring |
| `pkg/tools` (`view_image.go`) | the `view_image` tool |

## Not built

Drawing images inline in the terminal (kitty, iTerm2, sixel), downscaling or re-encoding,
attaching images in the interactive `chat continue` prompt, and pricing image-generation tokens.
