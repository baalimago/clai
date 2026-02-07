# Video Command Architecture

Command: `clai [flags] video <text>` (aliases: `v`)

The **video** command generates videos using AI models (currently OpenAI Sora) from a text prompt, optionally with an input image.

## Entry Flow

```
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()                     # extract CLI flags
    → getCmdFromArgs()                 # returns VIDEO mode
    → LoadConfigFromFile("videoConfig.json")
    → applyFlagOverridesForVideo()
    → vConf.SetupPrompts()             # build prompt from args/stdin/reply
    → CreateVideoQuerier(vConf)         # vendor-specific querier
  → querier.Query(ctx)                # execute the video generation
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | `Setup()` VIDEO case — loads config, creates querier |
| `internal/video/conf.go` | `Configurations` struct, `Default`, `OutputType` enum |
| `internal/video/prompt.go` | `SetupPrompts()` — prompt assembly with reply/stdin/image support |
| `internal/video/store.go` | `SaveVideo()` — decodes base64 and writes to disk |
| `internal/create_queriers.go` | `CreateVideoQuerier()` — routes to OpenAI Sora |
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
| `output.dir` | Directory for saved videos (default: `$HOME/Videos`) |
| `output.prefix` | Filename prefix (default: `clai`) |

### Flag Overrides

| Flag | Config Field |
|------|-------------|
| `-vm` / `-video-model` | `model` |
| `-vd` / `-video-dir` | `output.dir` |
| `-vp` / `-video-prefix` | `output.prefix` |
| `-re` / `-reply` | Enables reply mode |
| `-I` / `-replace` | Stdin replacement token |

## Prompt Assembly

`Configurations.SetupPrompts()` in `internal/video/prompt.go`:

1. If **reply mode** (`-re`): loads `prevQuery.json`, serializes messages as JSON context, prepends to prompt
2. Calls `utils.Prompt(stdinReplace, args)` to build user prompt from CLI args + stdin
3. Runs `chat.PromptToImageMessage(prompt)` to detect base64-encoded images in the prompt
   - If an image is found: sets `PromptImageB64` for image-to-video generation
   - Text portion becomes the prompt
4. If no image: applies `PromptFormat` to the prompt text

## Vendor Routing

`CreateVideoQuerier()` in `internal/create_queriers.go`:

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
