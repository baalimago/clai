# Photo Command Architecture

Command: `clai [flags] photo <text>` (aliases: `p`)

The **photo** command generates images using AI models (DALL-E, Gemini image generation) from a text prompt.

## Entry Flow

```
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()                    # extract CLI flags
    → getCmdFromArgs()                # returns PHOTO mode
    → LoadConfigFromFile("photoConfig.json")
    → applyFlagOverridesForPhoto()
    → pConf.SetupPrompts()            # build prompt from args/stdin/reply
    → CreatePhotoQuerier(pConf)        # vendor-specific querier
  → querier.Query(ctx)               # execute the photo generation
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | `Setup()` PHOTO case — loads config, creates querier |
| `internal/photo/conf.go` | `Configurations` struct, `DEFAULT`, `OutputType` enum |
| `internal/photo/prompt.go` | `SetupPrompts()` — prompt assembly with reply/stdin support |
| `internal/photo/store.go` | `SaveImage()` — decodes base64 and writes to disk |
| `internal/create_queriers.go` | `CreatePhotoQuerier()` — routes to OpenAI or Gemini |
| `internal/vendors/openai/dalle.go` | OpenAI DALL-E photo querier implementation |
| `internal/vendors/gemini/image.go` | Gemini photo querier implementation |

## Configuration

### `photoConfig.json`

```json
{
  "model": "gpt-image-1",
  "prompt-format": "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
  "output": {
    "type": "local",
    "dir": "$HOME/Pictures",
    "prefix": "clai"
  }
}
```

### Key Fields

| Field | Description |
|-------|-------------|
| `model` | Model name (e.g., `gpt-image-1`, `dall-e-2`, `gemini-*`) |
| `prompt-format` | Go format string; `%v` is replaced with the user prompt |
| `output.type` | `"local"` (save to disk), `"url"` (print URL), or `"unset"` |
| `output.dir` | Directory for saved images (default: `$HOME/Pictures`) |
| `output.prefix` | Filename prefix (default: `clai`) |

### Flag Overrides

| Flag | Config Field |
|------|-------------|
| `-pm` / `-photo-model` | `model` |
| `-pd` / `-photo-dir` | `output.dir` |
| `-pp` / `-photo-prefix` | `output.prefix` |
| `-re` / `-reply` | Enables reply mode |
| `-I` / `-replace` | Stdin replacement token |

## Prompt Assembly

`Configurations.SetupPrompts()` in `internal/photo/prompt.go`:

1. If **reply mode** (`-re`): loads `globalScope.json`, serializes messages as JSON context, prepends to prompt
2. Calls `utils.Prompt(stdinReplace, args)` to build user prompt from CLI args + stdin
3. Formats prompt through `PromptFormat` (e.g., wrapping in the "AS-IS" instruction)

## Vendor Routing

`CreatePhotoQuerier()` in `internal/create_queriers.go`:

| Model Pattern | Vendor |
|---------------|--------|
| contains `dall-e` or `gpt` | OpenAI (`openai.NewPhotoQuerier`) |
| contains `gemini` | Google (`gemini.NewPhotoQuerier`) |

## Output

### Local Storage

`SaveImage()` in `internal/photo/store.go`:

1. Decodes base64 response from the API
2. Generates filename: `<prefix>_<random>.png`
3. Writes to `output.dir`; falls back to `/tmp` on failure

### URL Mode

When `output.type` is `"url"`, the querier prints the image URL directly instead of downloading.

## Validation

Before creating a querier:
- `ValidateOutputType()` ensures `output.type` is one of `local`, `url`, `unset`
- If `output.type` is `local`, the output directory must exist
