# Video Command Architecture

Command: `clai [flags] video <text>` (aliases: `v`)

The **video** command generates videos using AI models (currently OpenAI Sora) from a text prompt, optionally with an input image.

## Entry Flow

```
internal/cli:Run()
  → cmd.Run(...)                       # go_away_boilerplate/pkg/cmd dispatch
    → video command Setup (internal/video/cmd.go)
    → LoadConfigFromFile("videoConfig.json")
    → applyFlagOverridesForVideo()
    → vConf.SetupPrompts()             # build prompt from args/stdin/reply
    → video.CreateQuerier(vConf)        # vendor-specific querier
  → querier.Query(ctx)                # execute the video generation
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/video/cmd.go` | video command — loads config, applies overrides, wires the injected querier factory (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/video/conf.go` | `Configurations` struct, `Default`, `OutputType` enum |
| `internal/video/generic/prompt.go` | `SetupPrompts()` — prompt assembly with reply context and the input image from `-img` or stdin |
| `internal/video/store.go` | `SaveVideo()` — decodes base64 and writes to disk |
| `internal/video/create_querier.go` | `video.CreateQuerier()` — routes to OpenAI Sora |
| `internal/vendors/openai/sora.go` | OpenAI Sora video querier implementation |

## Configuration

### `videoConfig.json`

```json
{
  "model": "sora-2",
  "prompt-format": "%v",
  "output": {
    "type": "unset",
    "dir": "$HOME/Videos",
    "prefix": "clai"
  }
}
```

### Key Fields

| Field | Description |
|-------|-------------|
| `model` | Model name (currently only `sora-*` supported) |
| `prompt-format` | Go format string; `%v` is replaced with the user prompt |
| `output.type` | `"local"` (save to disk), `"url"` (print URL), or `"unset"` |
| `output.dir` | Directory for saved videos (default: `~/Videos`). clai expands a leading `~` and each `$VAR`, in the file and on `-vd`. With `HOME` set, `~/Videos` and `$HOME/Videos` are the same |
| `output.prefix` | Filename prefix (default: `clai`) |

### Flag Overrides

| Flag | Config Field |
|------|-------------|
| `-vm` / `-video-model` | `model` |
| `-vd` / `-video-dir` | `output.dir` |
| `-vp` / `-video-prefix` | `output.prefix` |
| `-re` / `-reply` | Enables reply mode |
| `-I` / `-replace` | Stdin replacement token |
| `-img` / `-image` | The input image the video starts from: a path or a glob, `~` allowed (not a config field) |

## Prompt Assembly

`Configurations.SetupPrompts()` in `internal/video/prompt.go`:

1. If **reply mode** (`-re`): loads `globalScope.json`, serializes its messages as JSON context and
   prepends it to the prompt. Every image in that copy is its `[image <handle>]` text
   (`chat.ImagesDescribed`), so a text-only context is byte-identical to what it was before images.
2. Runs `imageinput.ImageCollector.Collect` (see [query.md](./query.md#image-input-collection)) over
   the `-img` values, stdin and the args. Stdin that is wholly an image, raw or base64, is the input
   image; any other stdin is prompt text exactly as `utils.Prompt` builds it. Base64 inside the
   prompt text stays text.
3. With one input image: `PromptImageB64` carries it for image-to-video generation, and the prompt
   is the text alone, with no reply context and no `PromptFormat`. More than one image fails with
   `generic.ErrOneInputImage`.
4. With no image: applies `PromptFormat` to the prompt text.

`Configurations.Client` (from `video.CommandDeps.Client`) replaces the vendor's HTTP client; nil
keeps it, and only tests set it.

## Vendor Routing

`video.CreateQuerier()` in `internal/video/create_querier.go`:

| Model Pattern | Vendor |
|---------------|--------|
| contains `sora` | OpenAI (`openai.NewVideoQuerier`) |

Only Sora models are currently supported. The output directory is auto-created if it doesn't exist.

## Output

### Local Storage

`SaveVideo()` in `internal/video/store.go`:

1. Decodes base64 response from the API
2. Generates filename: `<prefix>_<random>.<container>`
3. Writes to `output.dir`; falls back to `/tmp` on failure

### URL Mode

When `output.type` is `"url"`, the querier prints the video URL directly.

## Validation

- `ValidateOutputType()` ensures `output.type` is one of `local`, `url`, `unset`
- If `output.type` is `local`, the directory is created via `os.MkdirAll` if missing
