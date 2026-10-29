# Photo Command Architecture

Command: `clai [flags] photo [-img <path|glob>]... [-open] <prompt>` (aliases: `p`)

The **photo** command turns a prompt, and optional input images, into image files. It is
built on the injected image generator (`internal/imagegen`, see below), the input
collector (`internal/imageinput`) and the presenter (`internal/present`). It holds no
vendor code of its own.

## Entry Flow

```text
internal/cli:Run()
  -> cmd.Run(...)                          # go_away_boilerplate/pkg/cmd dispatch
     -> photo command Setup (internal/photo/cmd.go, query.go)
        -> deps check                      # nil NewGenerator/Collector/NewPresenter/NewOpener,
        |                                  # or -re without PreviousImage: ErrPhotoUnconfigured
        -> deps.ConfigPrep()               # config dir, theme, migrations
        -> deps.LoadConfig(confDir)        # photoConfig.json
        -> ApplyFlagOverrides()            # -pm, -pd, -pp, -re, -I/-i
        -> output.type "url"?              # *URLOutputRemovedError, before any request
        -> deps.NewGenerator({Model, ConfigDir})
        -> deps.Collector.Collect(-img, stdin, args)
        -> -re: chat.LoadPrevQuery + generic.ReplyContext, then deps.PreviousImage(confDir)
        -> deps.NewPresenter(Options{..., Opener: deps.NewOpener(image-opener)})
  -> query.Query(ctx)
     -> ImageGenerator.Generate(prompt, inputs)   # board.Status "generating image" on a terminal without -raw
     -> per image: write <output.dir>/<prefix>_<random>.<ext>, then Presenter.Present
```

`CommandDeps` is the injection seam. `internal/cli` fills it: `NewGenerator` is
`cli.Deps.NewImageGenerator` (`route.NewGenerator` in production), `Collector` is
`imageinput.New()`, `NewPresenter` is `present.New`, `NewOpener` is `present.NewCommandOpener`,
and `PreviousImage` is `photo.PreviousQueryImage`. Tests run the command with fake generators
and openers and never reach a paid API: every e2e `TestMain` injects a refusing generator, and
only a test that opts in swaps in a fake. A missing image dependency fails with
`ErrPhotoUnconfigured` before any work.

## Key Files

| File | Purpose |
|------|---------|
| `internal/photo/cmd.go` | the command, its flags (`Flags`: the media group plus `-img`/`-image` and `-open`), `CommandDeps`, the dependency check and config loading |
| `internal/photo/query.go` | one run: builds the request, generates, saves and presents each image |
| `internal/photo/previous_image.go` | `PreviousQueryImage`: the production `PreviousImageFunc` |
| `internal/photo/conf.go` | `Configurations`, `DEFAULT`, `OutputType` |
| `internal/photo/generic/prompt.go` | `ReplyContext()`: the `-re` preamble, with every image replaced by its `[image <handle>]` label |

## Configuration

### `photoConfig.json`

```json
{
  "model": "gpt-image-2.5-flare",
  "prompt-format": "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
  "output": {
    "type": "unset",
    "dir": "$HOME/Pictures",
    "prefix": "clai"
  },
  "raw": false,
  "auto-open-images": false,
  "image-opener": ""
}
```

### Key Fields

| Field | Description |
|-------|-------------|
| `model` | Image model; see the routing table under [Image generators](#image-generators), or a `model-aliases` verb such as `strong` (see [model-aliases.md](./model-aliases.md)) |
| `prompt-format` | Go format string; `%v` is replaced with the user prompt |
| `output.type` | `"local"` or `"unset"`: both save to `output.dir`. `"url"` was removed and fails with `*URLOutputRemovedError`; any other value fails too |
| `output.dir` | Directory for saved images (default: `~/Pictures`). clai expands a leading `~` and each `$VAR`, in the file and on `-pd`. With `HOME` set, `~/Pictures` and `$HOME/Pictures` are the same |
| `output.prefix` | Filename prefix (default: `clai`) |
| `raw` | Turns the progress row off, like `-raw` |
| `auto-open-images` | Open each saved image, like `-open` (default `false`) |
| `image-opener` | The viewer command; `""` is `xdg-open` on Linux and `open` on macOS |

The two image fields carry `migrate:"true"`, so the config upgrade adds them to an
existing `photoConfig.json`. Every command that shows an image reads them from this
file, `clai q` included.

### Flags

| Flag | Effect |
|------|--------|
| `-pm` / `-photo-model` | `model` |
| `-pd` / `-photo-dir` | `output.dir` |
| `-pp` / `-photo-prefix` | `output.prefix` |
| `-img` / `-image` | An input image to edit or use as a reference: a path or a glob. Repeatable, kept in order. Photo only, not on `video` |
| `-open` | Open each saved image. Photo only |
| `-re` / `-reply` | The previous query as text context, and its latest image as the last input |
| `-I` / `-replace`, `-i` | Stdin replacement token |
| `-r` / `-raw` | No progress row |

## Prompt and inputs

1. `ImageCollector.Collect` expands each `-img` value and reads stdin. Stdin that is wholly
   an image, raw or as base64, becomes an input image; any other stdin is prompt text, as
   `utils.Prompt` builds it.
2. The prompt is formatted through `prompt-format`.
3. With `-re`, `generic.ReplyContext` prepends the previous messages as JSON. Each image in
   that copy is its `[image <handle>]` label, so no image bytes reach the prompt, and a
   text-only context is byte-identical to the one before image support.
4. The inputs are the collected images in order, then the image `deps.PreviousImage`
   returns for `-re`. `ErrNoPreviousImage` means no extra input; any other error fails the
   command. `photo.PreviousQueryImage` loads `globalScope.json` through `chat.Persister.Load`
   and takes its last image part: a stored part is read from the asset store, a legacy
   (pending) part gives its own bytes. A missing `globalScope.json`, or one without an image,
   is `ErrNoPreviousImage`, so `-re` on a fresh config behaves as a plain run.

## Output

stdout holds exactly one line per saved image, written by the presenter: on a colour
terminal `▸ image <handle> · <model> · <W>×<H> (<ratio>) · <size> · <time> → file://<absolute path>`
(a clickable URL, also an OSC 8 link; the details as in [images.md](./images.md#the-cli-surfaces)),
otherwise the bare absolute path, so
`clai photo a fox | xargs xdg-open` works. The handle is the first 6 hex digits of the
file's SHA-256. The extension comes from the sniffed MIME type (`png`, `jpg`, `gif`,
`webp`).

stderr holds the errors, the opener notice (a failed viewer never fails the command), and
the fallback notice: when `output.dir` is not writable, the image is written to
`os.TempDir()` and stderr names both.

`photo` writes nothing to the conversation asset store; its images are files in the output
directory only. The exit status is 0 when every image is saved and 1 on any error.

## Image generators

`internal/imagegen` declares `ImageGenerator`: a prompt and optional input images in,
image bytes out. A generator never prints, animates or writes a file; every image's MIME
type is sniffed from its bytes and checked against the type the vendor declares. The
`generate_image` tool and `clai photo` use it (see [tooling.md](./tooling.md#image-results)).

| Model | Backend | Endpoint | Key |
|-------|---------|----------|-----|
| `gpt-image-*`, `dall-e-*`, `chatgpt-image-*` | `internal/vendors/openai/images.go` | `/v1/images/generations`, or multipart `/v1/images/edits` with one `image[]` per input | `OPENAI_API_KEY` |
| `or:<model>` (prefix stripped) | `internal/vendors/openrouter/images.go` | `/api/v1/images`, inputs as `input_references` data URLs | `OPENROUTER_API_KEY` |
| contains `gemini` or `nano-banana` | `internal/vendors/gemini/image.go` | `/v1beta/models/<model>:generateContent`, inputs as `inlineData` parts | `GEMINI_API_KEY` |

`route.NewGenerator` (`internal/imagegen/route`) is the production
`imagegen.NewGeneratorFunc`; callers receive it by injection, so tests never reach a
paid API. The OpenAI backend reads quality, size and n from
`openai_dalle_<model>.json` in the config dir when that file exists and never creates
it; a request's size overrides the file.
