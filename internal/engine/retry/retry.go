// Package retry implements the shared retry/backoff policy (§11) used by every
// HTTP-based engine: up to MaxRetries retries on 429/5xx responses and
// network/DNS errors, using exponential backoff (base 500ms, factor 2) with
// ±20% jitter, honoring Retry-After when present. The whole sequence runs
// inside the engine's per-engine context; a deadline firing mid-retry aborts
// the loop promptly (the caller reports it as "timeout").
//
// Centralizing the loop here — rather than duplicating it per engine — ensures
// every engine shares one correct, testable retry implementation.
package retry

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openmtx/meta-search-tool/internal/httpx"
)

// MaxRetries is the maximum number of retry attempts after the initial request
// (§11: "up to 2 retries"). The total request count is MaxRetries+1.
const MaxRetries = 2

// baseBackoff is the base delay for exponential backoff (§11: "base 500ms").
const baseBackoff = 500 * time.Millisecond

// backoffFactor is the multiplier per retry attempt (§11: "factor 2"):
// attempt 0 → 500ms, attempt 1 → 1s, attempt 2 → 2s.
const backoffFactor = 2

// errorBodyLimit caps how much of an error response body is read into the
// error message (keeps messages bounded).
const errorBodyLimit = 4096

// Test hooks — overridden via SetBackoffOverride/SetJitterSource for
// deterministic tests. They are package-level so all engines sharing this
// package are controlled together.
var (
	backoffOverride func(int) time.Duration
	jitterSource    = defaultJitter
)

// SetBackoffOverride replaces the backoff calculation for deterministic tests.
// Pass nil to restore the default exponential backoff. Callers should restore
// the original (via defer) to avoid leaking the override across tests.
func SetBackoffOverride(f func(attempt int) time.Duration) { backoffOverride = f }

// SetJitterSource replaces the jitter function for deterministic tests. Pass
// nil to restore the default ±20% jitter.
func SetJitterSource(f func(time.Duration) time.Duration) {
	if f == nil {
		f = defaultJitter
	}
	jitterSource = f
}

// Do executes an HTTP request with the §11 retry policy and returns a 2xx
// response (body open; the caller MUST close it) or an error. It retries up to
// MaxRetries times on:
//   - HTTP 429 (Too Many Requests) and all 5xx server errors;
//   - network/DNS errors (treated identically to timeouts, §11).
//
// Non-retryable non-2xx responses (e.g. 400/401/403/404) return immediately.
// The engineName prefixes every error so callers can distinguish sources.
//
// newReq builds a fresh request for each attempt. For GET requests with no body
// the same *http.Request may be returned each time; rebuilding per attempt is
// safe regardless.
func Do(ctx context.Context, engineName string, newReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error

	for attempt := 0; ; attempt++ {
		// Context already expired (e.g. per-engine timeout fired) — abort.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", engineName, err)
		}

		req, err := newReq()
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %w", engineName, err)
		}

		resp, err := httpx.Client(ctx).Do(req)
		if err != nil {
			// Network/DNS error — retryable (§11: "treated identically to
			// timeouts"). A context-cancellation surfaced here means the
			// deadline fired, so do not retry.
			lastErr = fmt.Errorf("%s: request: %w", engineName, err)
			if ctx.Err() != nil || attempt >= MaxRetries {
				return nil, lastErr
			}
			if !Sleep(ctx, CalcBackoff(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}

		// 2xx → success: hand the open body to the caller.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		// Non-2xx: read a bounded snippet for the error message and close.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		resp.Body.Close()
		errMsg := fmt.Errorf("%s: HTTP %d: %s", engineName, resp.StatusCode, strings.TrimSpace(string(body)))

		if !IsRetryable(resp.StatusCode) {
			// Non-retryable (400/401/403/404/…) — fail fast.
			return nil, errMsg
		}

		// Retryable (429/5xx). Exhausted → return the last error; otherwise
		// back off (honoring Retry-After) and retry.
		lastErr = errMsg
		if attempt >= MaxRetries {
			return nil, lastErr
		}
		delay := ApplyRetryAfter(resp.Header.Get("Retry-After"), CalcBackoff(attempt))
		if !Sleep(ctx, delay) {
			return nil, ctx.Err()
		}
	}
}

// IsRetryable reports whether an HTTP status code is retryable per §11:
// 429 (Too Many Requests) and all 5xx server errors.
func IsRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// CalcBackoff computes the exponential backoff delay for attempt (0-based):
// base × factor^attempt → 500ms, 1s, 2s, … (§11).
func CalcBackoff(attempt int) time.Duration {
	if backoffOverride != nil {
		return backoffOverride(attempt)
	}
	d := baseBackoff
	for i := 0; i < attempt; i++ {
		d *= backoffFactor
	}
	return d
}

// ApplyRetryAfter adjusts the backoff based on a Retry-After header value
// (parsed as seconds, the delta-seconds form). Per §11, Retry-After is honored
// but capped at the remaining per-engine budget — that budget is enforced by
// the context deadline in Sleep, not here. When Retry-After is present we use
// the larger of Retry-After and the calculated backoff, so we never retry
// faster than our backoff strategy while still respecting the server's request
// to wait. An absent or unparseable header falls back to the calculated backoff.
func ApplyRetryAfter(retryAfter string, backoff time.Duration) time.Duration {
	if retryAfter == "" {
		return backoff
	}
	secs, err := strconv.Atoi(retryAfter)
	if err != nil {
		return backoff
	}
	ra := time.Duration(secs) * time.Second
	if ra > backoff {
		return ra
	}
	return backoff
}

// Sleep sleeps for d (after applying ±20% jitter), returning false if ctx is
// cancelled before the sleep completes. Callers should abort the retry loop on
// false (the context deadline — i.e. the per-engine budget — is the real cap
// on server-requested Retry-After waits, §11).
func Sleep(ctx context.Context, d time.Duration) bool {
	d = jitterSource(d)
	if d <= 0 {
		// Still respect cancellation even with a zero/negative delay.
		select {
		default:
			return true
		case <-ctx.Done():
			return false
		}
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Jitter adds ±20% random variation to a duration (§11): result ∈ [0.8d, 1.2d].
func Jitter(d time.Duration) time.Duration { return defaultJitter(d) }

// defaultJitter is the default jitter implementation used by Sleep.
func defaultJitter(d time.Duration) time.Duration {
	ratio := 0.8 + rand.Float64()*0.4 // 0.8 to 1.2
	return time.Duration(float64(d) * ratio)
}
