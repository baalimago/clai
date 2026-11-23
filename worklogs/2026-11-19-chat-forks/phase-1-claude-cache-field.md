# Phase 1 — Claude cache field

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Every Messages API request the `anthropic` vendor sends carries Anthropic's one top-level `cache_control` field, so a Claude chat that is continued or forked reads its earlier turns from the provider's cache.

## Interface

Written to `internal/vendors/anthropic/claude.go` (the `claudeReq` struct and `cacheControl`), set in `internal/vendors/anthropic/claude_stream.go` (`constructRequest`). The vendor package is the only place the field lives (I5); `internal/text/generic/` and `internal/vendors/openrouter/` are not touched.

```go
// claudeReq is the Messages API request body. CacheControl is the last field,
// so it follows every field the body already has and a request without it is
// byte-identical to today's.
type claudeReq struct {
	// ... the existing fields, in their existing order ...
	OutputConfig *outputConfig `json:"output_config,omitempty"`

	// CacheControl asks Anthropic to cache the request up to its last
	// cacheable block (automatic caching).
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// cacheControl is {"type":"ephemeral"}: the 5-minute TTL (P1).
type cacheControl struct {
	Type string `json:"type"`
}

// ephemeralCache is the only value the vendor ever sends.
var ephemeralCache = cacheControl{Type: "ephemeral"}
```

Clauses, on `(*Claude).constructRequest(ctx, chat) (*http.Request, error)`:

- `constructRequest.1` The body of every request it returns has the top-level member `"cache_control":{"type":"ephemeral"}` (P1, P2, I6), for every model name, with and without tools, with and without a response format. No other vendor's request body changes: an OpenRouter request and a generic OpenAI-compatible request carry no `cache_control` (I5).
- `constructRequest.2` No other member of the body, and no content block, system block, tool or message, carries `cache_control` (P2). The body's other members are byte-for-byte what they were before this phase.
- `constructRequest.3` Two requests built from the same `Claude` and chats where the second is the first plus appended messages have bodies in which everything before the appended messages is byte-identical, including the cache member (I1).
- `constructRequest.4` Errors are as before (a bad URL, a bad message projection, a bad response format): the field adds no failure.
- `TokenUsage.1` The usage the vendor reports is unchanged: `CacheReadInputTokens` of `message_start` still becomes `PromptTokensDetails.CachedTokens` (`claude_usage.go`), so the "(N cached)" usage line needs no change.
- `CountInputTokens.1` The `count_tokens` body does not carry the field: it is built from the same struct with `CacheControl` unset, and is byte-identical to today's.

`ephemeralCache` is a package-level value, which AGENTS.md discourages; it is an immutable constant-like literal, so the implementation may equally build the pointer inside `constructRequest` through a small unexported constructor. The clauses do not depend on which.

Consumes: None.

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | A streamed request to a stand-in Messages endpoint arrives with `cache_control` equal to `{"type":"ephemeral"}` at the top level of the JSON object | constructRequest.1, I6 | `Test_constructRequest_cacheControl`, `Test_StreamCompletions_sendsCacheControl` |
| 2  | The field is present for the tools and response-format variants alike | constructRequest.1 | `Test_constructRequest_cacheControl` |
| 3  | No `cache_control` appears below the top level of any body | constructRequest.2 | `Test_constructRequest_cacheControlOnlyAtTopLevel` |
| 4  | With the field removed, the rest of the body equals the pre-phase body for the same inputs | constructRequest.2 | `Test_constructRequest_restOfBodyUnchanged` |
| 5  | Request N+1 of a chat repeats request N up to the appended messages | constructRequest.3, I1 | `Test_constructRequest_prefixStable` |
| 6  | The `count_tokens` body has no `cache_control` | CountInputTokens.1 | `Test_CountInputTokens_bodyHasNoCacheControl` |
| 7  | Cached tokens a stand-in reports in `message_start` still arrive as `Usage.PromptTokensDetails.CachedTokens` | TokenUsage.1 | `TestStreamCompletions_reportsStreamedUsage` (existing, stays green) |
| 8  | `internal/text/generic/` and `internal/vendors/openrouter/` have no diff against `main` | constructRequest.1, I5 | `git diff --stat main -- internal/text/generic internal/vendors/openrouter` prints nothing |

## Test cases

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `Test_constructRequest_cacheControl` | unit (table: plain, tools, `json_schema` response format, `omitsTemperature` model, long chat) | constructRequest.1 | `Claude{Model, URL}` and a chat; body read through the existing `requestBody(t, c)` helper | `body["cache_control"]` deep-equals `map[string]any{"type":"ephemeral"}` in every row | add a second member to the object |
| `Test_constructRequest_cacheControlOnlyAtTopLevel` | unit | constructRequest.2 | a chat with system, user, assistant tool call and tool result messages, and tools | walking the decoded body, `cache_control` occurs exactly once, at the root | mark a message, a system block or a tool |
| `Test_constructRequest_restOfBodyUnchanged` | unit | constructRequest.2 | golden bodies `testdata/claude_request_*.json`, recorded first from the unmodified code for three chats (new files) | the decoded body minus `cache_control` equals the golden, and the raw body ends with `,"cache_control":{"type":"ephemeral"}}` | reorder or drop a pre-existing member |
| `Test_constructRequest_prefixStable` | unit | constructRequest.3, I1 | a chat of N messages and the same chat plus two messages | the raw bodies share the prefix up to the first appended message; the trailing `cache_control` member is equal | depend on map iteration order |
| `Test_CountInputTokens_bodyHasNoCacheControl` | unit | CountInputTokens.1 | `URL` containing `anthropic.com`, served by a transport stand-in that records the body | the recorded body has no `cache_control` | reach the network |
| `TestStreamCompletions_reportsStreamedUsage` | unit (existing, stays green) | TokenUsage.1 | the existing `message_start` fixture | cached tokens arrive as before | change `claude_usage.go` |
| `Test_constructRequest_errorsUnchanged` | unit | constructRequest.4 | an unparseable URL; an invalid response format | the same errors `TestClaudeConstructRequest` and `Test_constructRequest_responseFormat` already assert | wrap or swallow them |
| `Test_StreamCompletions_sendsCacheControl` | integration scenario | constructRequest.1 | an `httptest` server answering a minimal Messages SSE stream; a `Claude` built the way `claude_stream_usage_test.go` builds one | the server's recorded request body has the member, and the stream still delivers its text and usage | leave the request body unread |
| `generic and openrouter unchanged` | command | criterion 8, constructRequest.1, I5 | `git diff --stat main -- internal/text/generic internal/vendors/openrouter` | prints nothing | edit either package |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| The request URL does not parse | `constructRequest` returns the wrapped `failed to create request` error, as today | `Test_constructRequest_errorsUnchanged` |
| The response format cannot map onto `output_config` | The existing error from `outputConfig()`, as today | `Test_constructRequest_errorsUnchanged` |
| The body cannot be encoded | The `failed to marshal ClaudeReq` error, as today; the cache member cannot cause it (a struct of one string) | `Test_constructRequest_errorsUnchanged` |

## Golden diff

None.

## Removes

None.

## Local facts

- The cost of always-on caching (1.25x write on a one-shot query) is P5; there is no switch to avoid it (I6).
- `count_tokens` is left without the field: it counts and caches nothing, and that endpoint's body shape is not part of this effort.
- Cached tokens already reach `pub_models.Usage` (`claude_usage.go`: `CacheReadInputTokens` becomes `PromptTokensDetails.CachedTokens`), so the "(N cached)" usage line needs no change.
- Claude through OpenRouter is not cached (A01; Scope).

## Human required

None. The live Anthropic check is the root's Human check "caching" (phase 6).

## Implementation notes

| Run   | Commit       | Command                                   | Result |
| ----- | ------------ | ----------------------------------------- | ------ |
| start | 6658380      | make qa QA_PROCS=5 (API keys unset)       | exit 0, all packages ok |
| end   | 6658380+wip  | make qa QA_PROCS=5 (API keys unset)       | exit 2: `internal/text` `TestTurn_TextOnlyUnchanged`, `internal/vendors` `TestImageEncoderContract/anthropic/TextUnchanged`; `internal/vendors/anthropic` ok |
| ported | 615303b+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa; go vet ./...; dupl -t 80 .` | Ported from the worktree onto 615303b (branch). Goldens `testdata/claude_request_{plain,tools,schema}.json` re-recorded from this branch's unmodified anthropic code (they equal the main-recorded ones byte for byte, apart from a trailing newline). The three A02 snapshots updated by edit, each gaining only `,"cache_control":{"type":"ephemeral"}`. Criterion 8 checked as `git diff 615303b -- internal/text/generic internal/vendors/openrouter` (empty). qa exit 0 (three runs), vet 0 |

Andon: A02 (design andon). Code and tests in `internal/vendors/anthropic/` are done (`claudeReq.CacheControl`, `cacheControl`, `ephemeralCache()`, set in `constructRequest`; `claude_cache_test.go`, goldens `testdata/claude_request_{plain,tools,schema}.json` recorded from the unmodified code). Three existing goldens outside the phase encode the old body and now fail: `internal/text/testdata/turn_text_only/request-1.json`, `request-2.json`, `internal/vendors/testdata/image_encoder/anthropic.text.golden`. Updating them is outside the phase's paths and contradicts Golden diff: None.
Surprises:

- The worktree head is 6658380, not 615303b as the brief says.
- `streamUntilEnd` in `claude_stream_usage_test.go` was split to expose `streamUntilEndAt(t, url)`.

## Review findings

### Review 1, 2026-11-23

Ready. No `blocker`, `major` or `minor` findings.

- **R1-N1 (note)** `internal/vendors/anthropic/claude.go:123-137`, `claude_stream.go:399`: `ephemeralCache()` is a constructor returning a fresh pointer, the alternative the Interface allows for the package-level value; the field is last in `claudeReq`, so bodies end `,"cache_control":{"type":"ephemeral"}}` (checked by `Test_constructRequest_restOfBodyUnchanged`).
- **R1-N2 (note)** `claude_stream.go:435-438`: `CountInputTokens` builds its own `claudeReq` without `CacheControl`, so CountInputTokens.1 holds by construction (`Test_CountInputTokens_bodyHasNoCacheControl`).
- **R1-N3 (note)** I5 verified: `git diff 615303b --stat -- internal/text/generic internal/vendors/openrouter` is empty. The only other changes outside `internal/vendors/anthropic/` are the three A02-approved snapshots, each gaining only the cache member.
- **R1-N4 (note)** No `architecture/` or help text describes Anthropic request caching today, so no documentation disagrees with this phase; phase 6 owns the architecture rewrites.
- **R1-N5 (note)** Gates run twice on 615303b+wip: `make qa QA_PROCS=5` exit 0 both runs (anthropic 89.8% coverage), `go vet ./...` 0, dupl 49 groups outside `.claude/worktrees` (unchanged; the one clone group in `internal/vendors/anthropic` is pre-existing in `claude_stream_test.go`).
