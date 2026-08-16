package text

import (
	"context"
	"unicode/utf8"
)

// slogTruncationMarker is the single rune (U+2026) inserted at the head/tail
// split of a truncated message. One rune keeps tiny limits valid — a limit
// of 1 yields exactly the marker — and avoids the sub-marker edge case a
// longer "…[truncated]…" marker would create (worklog 2026-08-15-agent-slog-output, D2).
const slogTruncationMarker = "…"

// truncateMiddleRunes returns s unchanged when utf8.RuneCountInString(s) <= limit,
// or when limit <= 0 (no cap, worklog 2026-08-15-agent-slog-output, D5). Otherwise it returns head + "…" + tail
// totalling exactly limit runes and reports truncated=true. The split is
// rune-safe: head is the first headLen runes, tail is the last tailLen runes,
// headLen + tailLen == limit-1, balanced head/tail.
func truncateMiddleRunes(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	n := utf8.RuneCountInString(s)
	if n <= limit {
		return s, false
	}
	headLen := limit / 2
	tailLen := limit - 1 - headLen
	return firstRunes(s, headLen) + slogTruncationMarker + lastRunes(s, tailLen), true
}

// firstRunes returns the first n runes of s. The caller guarantees n is
// within the rune count of s.
func firstRunes(s string, n int) string {
	end := len(s)
	i := 0
	for j := range s {
		if i == n {
			end = j
			break
		}
		i++
	}
	return s[:end]
}

// lastRunes returns the last n runes of s. The caller guarantees n is within
// the rune count of s.
func lastRunes(s string, n int) string {
	i := len(s)
	for range n {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// logMessage emits one truncated "clai message" record per completed message
// at its semantic completion site (worklog 2026-08-15-agent-slog-output,
// Phase 3). It is the sole gate for agent logging: nil agentSettings or a nil
// Logger disables the channel, and the record is never gated by
// structuredOutput, rawDisplay, debug, or the output writer. The caller-set
// level is the record level; the kind attribute is how a caller filters finer
// (worklog 2026-08-15-agent-slog-output, D3). ctx is the live query context, forwarded to the slog handler so a
// ctx-aware external handler can enrich or correlate records (worklog 2026-08-15-agent-slog-output, D6).
func (q *Querier[C]) logMessage(ctx context.Context, kind, text, tool string) {
	s := q.agentSettings
	if s == nil || s.Logger == nil {
		return
	}
	truncated, was := truncateMiddleRunes(text, s.RuneLimit)
	attrs := []any{"kind", kind, "text", truncated, "truncated", was}
	if tool != "" {
		attrs = append(attrs, "tool", tool)
	}
	s.Logger.Log(ctx, s.Level, "clai message", attrs...)
}
