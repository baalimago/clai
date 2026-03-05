package openai

import (
	"net/url"
	"strings"
)

func selectOpenAIURL(model, currentURL string) (string, bool) {
	useResponses := strings.Contains(strings.ToLower(model), "codex")
	if currentURL == "" {
		if useResponses {
			return ResponsesURL, true
		}
		return ChatURL, false
	}

	if currentURL == ResponsesURL {
		if useResponses {
			return ResponsesURL, true
		}
		return ChatURL, false
	}
	if currentURL == ChatURL {
		if useResponses {
			return ResponsesURL, true
		}
		return ChatURL, false
	}

	parsed, err := url.Parse(currentURL)
	if err != nil {
		if useResponses {
			return ResponsesURL, true
		}
		return ChatURL, false
	}
	if parsed.Path == "" || parsed.Path == "/" {
		if useResponses {
			parsed.Path = "/v1/responses"
		} else {
			parsed.Path = "/v1/chat/completions"
		}
		return parsed.String(), useResponses
	}

	return currentURL, useResponses
}
