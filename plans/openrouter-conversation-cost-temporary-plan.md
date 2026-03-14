# OpenRouter conversation cost temporary plan

## Goal

Track estimated conversation cost as persisted per-invocation query entries,
derive conversation totals from those entries, and show the total cost in chat
list and chat info views.

The implementation should be cleanly separated from chat/inference internals so the
cost subsystem can later expand into adjacent concerns like price comparison,
alternative provider analysis, or other model-cost metadata work.

## Constraints and decisions

- Use the OS cache location, not `<clai-config>/cache`
  - Use `os.UserCacheDir()` through `internal/utils.GetClaiCacheDir()`
  - Keep support for `CLAI_CACHE_DIR` override
- Create a dedicated `internal/cost` module for the cost-related functionality
- The cost subsystem must be best-effort only
  - it must never fail main query operations
  - failures should only be logged by the caller/integration layer
- Use OpenRouter only as the pricing metadata source
  - this applies even when inference is performed through another vendor
  - for this temporary feature, assume the OpenRouter-listed model price is a good
    enough estimate even when calling the vendor directly
- Do not derive pricing from message roles or transcript internals
  - only use the full model response usage as exposed via `models.UsageTokenCounter`
- Format prices with max 2 digits after the decimal point when shown in the UI
- For the OpenRouter `ModelCatalogFetcher` implementation:
  - use structured OpenRouter model JSON, not docs HTML scraping

## High-level architecture

Introduce a new `internal/cost` package responsible for:

1. fetching and caching model pricing metadata
2. managing a background best-effort cost lookup flow for a query invocation
3. estimating per-invocation query cost from:
   - model pricing
   - token usage returned by `UsageTokenCounter`
4. appending persisted query-cost entries to chats
5. exposing helpers for formatting and total-cost derivation

The text querier should only:

- tell the cost manager which model is being used
- optionally start it in parallel
- give it the final token usage and chat to enrich before persistence
- log cost errors and continue

## Data model

Extend `pkg/text/models.Chat` with persisted cost history:

```go
type QueryCost struct {
	CreatedAt time.Time `json:"created_at"`

	CostUSD float64 `json:"cost_usd"`

	Model string `json:"model,omitempty"`

	Usage Usage `json:"usage"`
}

type Chat struct {
	Created    time.Time   `json:"created"`
	ID         string      `json:"id"`
	Profile    string      `json:"profile,omitempty"`
	Messages   []Message   `json:"messages"`
	TokenUsage *Usage      `json:"usage,omitempty"`
	Queries    []QueryCost `json:"queries,omitempty"`
}
```

Notes:

- Do not persist a cumulative total
- Conversation total cost should always be derived from `Queries`
- For now, do not persist user-message index or derive costs from transcript role analysis
- The pricing unit is one full invocation cost, based on final token usage only
- `HasCostEstimates()` should return `len(c.Queries) > 0`
- `QueryCost.CreatedAt` should be set at enrichment/persistence time with `time.Now()`
- A query-cost entry may be persisted even if `Model` is empty, as long as the estimate itself is valid

Helpers to add in `pkg/text/models/chat.go`:

```go
func (c Chat) TotalCostUSD() float64
func (c Chat) HasCostEstimates() bool
```

Backward compatibility requirements:

- old chats without `queries` must continue to unmarshal unchanged
- new chats must round-trip cleanly

## New `internal/cost` module

Suggested files:

- `internal/cost/manager.go`
- `internal/cost/catalog_fetcher.go`
- `internal/cost/cache.go`
- `internal/cost/openrouter_models.go`
- `internal/cost/estimate.go`
- `internal/cost/format.go`

### Responsibilities

`internal/cost` should own:

- OpenRouter catalog/types for pricing lookup
- catalog fetch orchestration
- cache read/write
- query-scope asynchronous lookup lifecycle
- estimate calculation
- UI formatting helpers if shared by multiple call sites

This keeps the solution scalable and prevents cost logic from leaking into chat logic.

## Model catalog fetching

Define an abstraction that the manager depends on:

```go
type ModelCatalogFetcher interface {
	FetchModel(ctx context.Context, model string) (ModelPriceScheme, error)
}
```

The model fetcher is injected into the cost manager.

This keeps the manager open for:

- OpenRouter HTTP-backed fetchers
- fixtures/fakes in tests
- future alternative cost sources

## Cache location and format

Use `internal/utils.GetClaiCacheDir()`.

Store model cache entries under a cost-specific directory, with one file per model:

- `<clai-cache-dir>/cost/<model>.json`

The cache file should store a history list so price fluctuations can be tracked over time,
rather than only storing the newest snapshot.

Suggested shape:

```go
type CachedModelPriceHistory struct {
	Entries []CachedModelPrice `json:"entries"`
}

type CachedModelPrice struct {
	FetchedAt time.Time        `json:"fetched_at"`
	Model     string           `json:"model"`
	Price     ModelPriceScheme `json:"price"`
}
```

Behavior:

- cache is optional and best-effort
- cache failures must not fail query flow
- malformed cache should be ignored and refreshed
- the most recent valid entry is used for estimation
- fetching a fresh value should append a new history entry
- TTL can initially be simple, eg 12h or 24h, and should be evaluated against the latest cached entry

The cache should not include vendor in the filename or lookup semantics for now.

## OpenRouter model structs

Define full structs for structured OpenRouter model metadata, not pricing-only structs.

Prefer to model:

- top-level response
- model entries
- pricing
- architecture
- providers / top-provider
- supported parameters
- context / limits / modality metadata if present

Even if not all fields are used today, keep the models broad enough for later reuse.

## Cost manager lifecycle

The cost manager should be started optionally and asynchronously during query execution.

Suggested shape:

```go
type Manager struct {
	fetcher  ModelCatalogFetcher
	cacheDir string
	debug    bool
}

type Session interface {
	Start(ctx context.Context) <-chan error
	Enrich(chat pub_models.Chat) (pub_models.Chat, error)
}
```

Flow:

1. setup manager:
   * exit early if `ModelCatalogFetcher` is nil
   * exit early if cache dir resolution fails
   * construct with `NewManager(fetcher ModelCatalogFetcher, cacheDir string)`
   * be in debug mode if `DEBUG=truthy` or `DEBUG_COST_MANAGER=truthy`
2. text querier passes the model name to the cost manager in setup
3. text querier starts a cost session in parallel via `Start(ctx)`
4. `Start(ctx)` is non-blocking and returns a receive-only error channel
5. background lookup fetches price metadata or resolves a cache hit
6. query inference proceeds independently
7. immediately before persistence, the querier calls `Enrich(chat)`
8. the querier logs any cost errors and continues with the original chat

Error behavior:

- `Start(ctx)` reports asynchronous fetch/cache/setup failures through the returned channel
- the returned channel should emit at most one terminal error and then close, or just close on success
- `Enrich(chat)` returns an error for missing usage, missing pricing, invalid cache state, or estimation failure
- manager and session code should return contextual errors and not log directly
- caller/integration code should catch, log, and continue

Important behavior:

- if the manager/session fails, the original chat should still be persisted unchanged
- no main operation should fail because cost estimation failed

## Persistence behavior

At persistence time, the session should:

1. inspect final `chat.TokenUsage`
2. inspect fetched model pricing if available
3. calculate estimated USD cost for the full invocation
4. append exactly one `QueryCost`
5. return the enriched chat

If any required input is missing:

- return a contextual error
- do not log inside the cost manager
- let the caller keep and persist the original chat unchanged

This preserves reliability and honesty of stored cost data.

## Estimation rules

The estimate should only use:

- the model pricing fetched by the cost manager
- the final token counts from `UsageTokenCounter`

The invocation is priced as one full response using the final usage object.

Calculator should support:

- prompt tokens
- completion tokens
- cached prompt tokens if the pricing payload distinguishes them

Rules:

- `TotalTokens` is display-only and should not be used for pricing math
- if cached token pricing is absent, fall back cleanly to normal input pricing behavior
- derive non-cached prompt tokens from `PromptTokens - CachedTokens`
- clamp any negative derived prompt-token amount to zero
- a non-nil usage object with zero tokens should produce a valid `$0` estimate and persist one `QueryCost`

The implementation must normalize pricing into a clearly defined internal unit.
For the first implementation, normalize all model price components to USD per token
before performing the estimate.

## UI cost formatting

Chat list should show conversation total cost derived from `Queries`.

Formatting rule:

- maximum 2 decimal digits

Examples:

- `$0`
- `$0.01`
- `$1.2`
- `$14.53`
- `N/A`

Suggested helper:

```go
func FormatUSD(v float64) string
```

This can live in `internal/cost/format.go`.

## Chat list output

Update `internal/chat/handler_list_chat.go` to show total cost.

Columns:

- narrow terminal: `Index | Created | Messages | Cost | Prompt`
- wide terminal: `Index | Created | Messages | Profile | Cost | Tokens | Prompt`

Rules:

- narrow mode prefers the `Cost` column over the `Tokens` column
- wide mode should show both cost and tokens
- the cost value should be produced from `chat.TotalCostUSD()` and formatted through `internal/cost.FormatUSD(...)`
- if `chat.HasCostEstimates()` is false, show `N/A`

## Chat info output

Update `internal/chat/handler_list_chat.go` `printChatInfo()` to also show the
conversation total cost.

Use:

- `chat.HasCostEstimates()` to decide whether to show a formatted cost or `N/A`
- `chat.TotalCostUSD()` for the derived total

## Persistence/copy audit

Audit all places that manually reconstruct `pub_models.Chat` and ensure `Queries` is preserved.

Known place and guaranteed breakage point:

- `internal/chat/reply.go`

Any chat copy path must preserve:

- `Messages`
- `Profile`
- `TokenUsage`
- `Queries`

## Testing plan

Follow repository rules:

- tests first
- validate with timeout

### Unit tests

1. `pkg/text/models/chat.go`
   - old JSON without `queries` unmarshals
   - new JSON round-trips
   - `TotalCostUSD()` sums correctly
   - `HasCostEstimates()` matches `len(Queries) > 0`

2. `internal/cost/openrouter_models.go`
   - realistic fixture JSON parses cleanly
   - target model lookup works
   - malformed payload returns wrapped error

3. `internal/cost/cache.go`
   - writes under `GetClaiCacheDir()`-resolved cache root
   - fresh cache hit is used
   - stale cache is ignored/refreshed
   - malformed cache is tolerated
   - fetching a fresh value appends a new history entry
   - latest history entry is selected for estimation

4. `internal/cost/estimate.go`
   - prompt + completion pricing
   - cached token pricing
   - zero tokens yields `$0`
   - missing usage returns wrapped error
   - missing pricing returns wrapped error
   - negative derived non-cached prompt amount is clamped to zero

5. `internal/cost/manager.go`
   - `Start(ctx)` is non-blocking
   - `Start(ctx)` returns a closed channel on success with no emitted errors
   - background fetch success makes `Enrich(chat)` append one query-cost entry
   - background fetch failure makes `Enrich(chat)` return an error

6. `internal/chat/reply.go`
   - save/load preserves `Queries`

7. `internal/chat/handler_list_chat.go`
   - narrow list shows cost instead of tokens
   - wide list shows both cost and tokens
   - cost formatting is shown with max 2 decimals
   - `N/A` shown when no cost estimates exist
   - chat info view shows total cost

### Integration tests

1. success path
   - fake fetcher returns pricing
   - fake model returns token usage
   - query persists one query-cost entry
   - chat list shows total cost

2. failure-tolerant path
   - fetcher fails
   - query still succeeds
   - chat persists without cost entry
   - chat list shows `N/A`

3. continuation path
   - first query persists one cost entry
   - second query persists another
   - total cost equals sum of entries

## Implementation order

1. add/adjust chat model tests
2. add chat model `Queries` + helpers
3. add `internal/cost` model + estimation tests
4. add fetcher/cache abstractions + tests
5. add manager/session behavior + tests
6. integrate best-effort session into querier before `SaveAsPreviousQuery`
7. preserve `Queries` in save/copy flows
8. update chat list and chat info rendering
9. add integration coverage