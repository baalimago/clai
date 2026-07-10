package openai

import (
	"net/url"
	"strings"
)

// canonicalOpenAIHost is the only host for which an unrecognized/empty path is
// defaulted to the Responses API. Custom proxy hosts keep the legacy Chat
// Completions API unless they explicitly name a /responses path, so existing
// persisted proxy configs are never silently migrated to a wire format the proxy
// may not speak.
const canonicalOpenAIHost = "api.openai.com"

// selectOpenAIURL resolves which OpenAI text API a model should use and returns
// the concrete endpoint URL together with whether the Responses API was selected.
//
// The Responses API is the default for the canonical OpenAI host (empty URL or
// api.openai.com). A URL is kept on the legacy Chat Completions API when its path
// names "/chat/completions", or when it points at a custom host without a
// "/responses" path (conservative rollout — no forced migration of proxy configs).
// Codex-family models are Responses-only, so a chat/completions URL is transparently
// redirected to the responses endpoint for them.
//
// currentURL may be empty (use the OpenAI default host), a full OpenAI endpoint, or
// a custom proxy base. A bare host (no path) is normalized to the selected endpoint;
// a URL that already names an endpoint path is preserved as-is.
func selectOpenAIURL(model, currentURL string) (string, bool) {
	useResponses := resolveUseResponses(model, currentURL)
	return openAIEndpoint(currentURL, useResponses), useResponses
}

// resolveUseResponses decides whether the Responses API should be used for the given
// model and configured URL. See selectOpenAIURL for the resolution rules.
func resolveUseResponses(model, currentURL string) bool {
	if isCodexModel(model) {
		return true
	}
	if currentURL == "" {
		return true
	}
	host, path := hostAndPath(currentURL)
	switch {
	case strings.Contains(path, "/chat/completions"):
		return false
	case strings.Contains(path, "/responses"):
		return true
	default:
		// Path names neither endpoint: only default to Responses for the canonical
		// OpenAI host; any other host keeps the legacy Chat Completions API.
		return host == canonicalOpenAIHost
	}
}

// hostAndPath splits currentURL into host and path, tolerating unparseable input by
// treating the whole string as the path (host empty).
func hostAndPath(currentURL string) (string, string) {
	if parsed, err := url.Parse(currentURL); err == nil {
		return parsed.Host, parsed.Path
	}
	return "", currentURL
}

// openAIEndpoint returns the concrete endpoint for the selected API, preserving any
// custom host in currentURL.
func openAIEndpoint(currentURL string, useResponses bool) string {
	if currentURL == "" {
		if useResponses {
			return ResponsesURL
		}
		return ChatURL
	}

	parsed, err := url.Parse(currentURL)
	if err != nil {
		return currentURL
	}

	if parsed.Path == "" || parsed.Path == "/" {
		if useResponses {
			parsed.Path = "/v1/responses"
		} else {
			parsed.Path = "/v1/chat/completions"
		}
		return parsed.String()
	}
	// api.openai.com/v1 is a documented API base URL, not a request endpoint.
	// Once routing selected an API, turn that base into the concrete endpoint.
	if parsed.Host == canonicalOpenAIHost && strings.TrimSuffix(parsed.Path, "/") == "/v1" {
		if useResponses {
			parsed.Path = "/v1/responses"
		} else {
			parsed.Path = "/v1/chat/completions"
		}
		return parsed.String()
	}

	// Codex on a chat/completions URL: redirect to the responses endpoint since
	// codex cannot be served by chat completions.
	if useResponses && strings.Contains(parsed.Path, "/chat/completions") {
		parsed.Path = strings.Replace(parsed.Path, "/chat/completions", "/responses", 1)
		return parsed.String()
	}

	return currentURL
}

// isCodexModel reports whether the model belongs to the Responses-only codex family.
func isCodexModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "codex")
}

// normalizeModelID strips provider and fine-tune wrappers from a model name so the
// bare model ID can be matched. It handles:
//   - fine-tuned models: "ft:<base>:<org>::<id>" -> "<base>"
//   - provider-qualified names: "openai/o3-mini" -> "o3-mini"
//
// Without this, prefix-based o-series detection misses qualified names and wrongly
// forwards sampling params that such models reject.
func normalizeModelID(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndexByte(m, '/'); idx >= 0 {
		m = m[idx+1:]
	}
	if rest, ok := strings.CutPrefix(m, "ft:"); ok {
		if idx := strings.IndexByte(rest, ':'); idx >= 0 {
			m = rest[:idx]
		} else {
			m = rest
		}
	}
	return m
}

// isReasoningModel reports whether the model is a reasoning model. Reasoning models
// (gpt-5.x, the o-series, codex) reject sampling parameters such as temperature and
// top_p, so those are omitted from Responses requests for them.
func isReasoningModel(model string) bool {
	m := normalizeModelID(model)
	switch {
	case strings.Contains(m, "gpt-5-chat"):
		// gpt-5-chat-latest is a non-reasoning chat model that accepts sampling
		// params, despite matching the general gpt-5 rule below.
		return false
	case strings.Contains(m, "gpt-5"):
		return true
	case isCodexModel(m):
		return true
	case strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return true
	default:
		return false
	}
}
