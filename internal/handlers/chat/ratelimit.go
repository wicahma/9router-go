package chat

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// errRateLimited marks a request that the local per-model RPS ceiling
// rejected. It is deliberately NOT an *upstreamError: 429 is in
// providers.RetryableStatusCodes, and both the fallback loop and the combo
// loop answer a retryable status by locking the connection with a backoff
// cooldown. A limit the operator configured themselves says nothing about
// the health of the connection, so it must not travel that path.
var errRateLimited = errors.New("model rps limit reached")

// rateLimitError records which model ran out of budget and how long until one
// token frees up, so the caller can set Retry-After on the final 429 instead
// of inventing a delay.
type rateLimitError struct {
	key          string
	retryAfterMs int64
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("%v: %s exhausted its rps limit, retry in %dms", errRateLimited, e.key, e.retryAfterMs)
}

func (e *rateLimitError) Is(target error) bool { return target == errRateLimited }

func (e *rateLimitError) Unwrap() error { return errRateLimited }

// isRateLimited reports whether err came from the local RPS ceiling rather
// than from an upstream.
func isRateLimited(err error) bool { return errors.Is(err, errRateLimited) }

// RateLimitDenied counts locally throttled requests so the dashboard can show
// what the RPS ceilings are costing. A plain atomic counter: the hot path
// must not take a second lock, and a lost increment on shutdown is harmless.
var RateLimitDenied atomic.Int64

// newRateLimitError builds a denial carrying the computed wait, rounded up to
// the next millisecond so a sub-millisecond wait never serialises as 0.
func newRateLimitError(key string, wait time.Duration) *rateLimitError {
	ms := wait.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return &rateLimitError{key: key, retryAfterMs: ms}
}

// writeRpsError answers the client with 429 and a Retry-After in whole
// seconds. Seconds are the HTTP-defined unit, and the wait is rounded UP:
// rounding down would invite an immediate retry that gets denied again, which
// is exactly the hammering the limit exists to prevent.
func writeRpsError(w http.ResponseWriter, rl *rateLimitError) {
	w.Header().Set("Retry-After", strconv.FormatInt((rl.retryAfterMs+999)/1000, 10))
	body := fmt.Sprintf(
		`{"error":{"message":%q,"type":"rate_limit_error","code":429}}`,
		fmt.Sprintf("model %s reached its requests-per-second limit, retry shortly", rl.key))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(body))
}
