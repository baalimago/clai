package generic

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// RateLimiter is a vendor agnostic limiter for input tokens.
// It parses rate limit headers and pauses requests when the limit is hit.
type RateLimiter struct {
	remainingHeader string
	resetHeader     string

	remainingTokens int
	resetTokens     time.Time

	debug bool
}

// NewRateLimiter creates a new limiter using the provided header names.
func NewRateLimiter(remainingHeader, resetHeader string) RateLimiter {
	rl := RateLimiter{
		remainingHeader: strings.ToLower(remainingHeader),
		resetHeader:     strings.ToLower(resetHeader),
	}
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_RATE_LIMIT")) {
		rl.debug = true
	}
	return rl
}

// updateFromHeaders extracts rate limit information from an HTTP response.
// It resets previous values to avoid stale data.
// If the required headers are missing or malformed an error is returned.
func (r *RateLimiter) UpdateFromHeaders(h http.Header) error {
	if r.remainingHeader == "" || r.resetHeader == "" {
		return nil
	}

	r.remainingTokens = 0
	r.resetTokens = time.Time{}

	remStr := h.Get(r.remainingHeader)
	if remStr == "" {
		return fmt.Errorf("missing header '%s'", r.remainingHeader)
	}
	rem, err := strconv.Atoi(remStr)
	if err != nil {
		if r.debug {
			ancli.PrintWarn(fmt.Sprintf("failed to parse %s: %v", r.remainingHeader, err))
		}
		return fmt.Errorf("failed to parse %s: %w", r.remainingHeader, err)
	}
	r.remainingTokens = rem

	resetStr := h.Get(r.resetHeader)
	if resetStr == "" {
		return fmt.Errorf("missing header '%s'", r.resetHeader)
	}

	if dur, err := time.ParseDuration(resetStr); err == nil {
		r.resetTokens = time.Now().Add(dur)
	} else if ts, err2 := strconv.ParseInt(resetStr, 10, 64); err2 == nil {
		r.resetTokens = time.Unix(ts, 0)
	} else if sec, err3 := strconv.ParseFloat(resetStr, 64); err3 == nil {
		r.resetTokens = time.Now().Add(time.Duration(sec * float64(time.Second)))
	} else {
		if r.debug {
			ancli.PrintWarn(fmt.Sprintf("failed to parse %s: %v", r.resetHeader, err))
		}
		return fmt.Errorf("failed to parse %s", r.resetHeader)
	}
	return nil
}

// waitIfNeeded pauses execution when close to the rate limit.
func (r *RateLimiter) WaitIfNeeded(ctx context.Context) {
	if r.remainingHeader == "" {
		return
	}
	if r.remainingTokens > 50 || r.resetTokens.IsZero() {
		return
	}

	waitDuration := time.Until(r.resetTokens)
	if waitDuration <= 0 {
		return
	}
	ancli.PrintWarn("rate limit reached, pausing")
	ancli.PrintWarn("waiting " + waitDuration.Round(time.Second).String())
	timer := time.NewTimer(waitDuration)
	select {
	case <-ctx.Done():
		timer.Stop()
	case <-timer.C:
	}
}
