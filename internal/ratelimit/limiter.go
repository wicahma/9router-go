// Package ratelimit enforces an operator-configured requests-per-second cap
// per model key ("provider/model") using a token bucket. It exists because
// several upstream subscriptions are sold with a hard RPS ceiling: firing
// past it earns a 429 from the provider, so the router would rather spend
// that budget on the next model in the chain.
package ratelimit

import (
	"sync"
	"time"
)

// Registry holds one token bucket per limited model key.
//
// Design constraints, all deliberate:
//   - No background goroutine and no ticker. Refill is computed lazily from
//     elapsed wall time on the next Allow, so an idle model costs nothing.
//   - One mutex, not per-bucket locks. The map is tiny (a handful of
//     limited models) and the critical section is a few float operations.
//   - No new dependency: x/time/rate is not in go.mod and this is the only
//     thing we would use it for.
type Registry struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
	rps    float64
}

// New returns an empty registry. Models with no configured limit are always
// allowed through.
func New() *Registry {
	return &Registry{
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// shared is the process-wide registry. Limits come from the global settings
// row, so all handlers must share one set of buckets — a per-handler
// registry would let each one spend the full RPS budget.
var shared = New()

// Shared returns the process-wide registry used by the request hot path.
func Shared() *Registry { return shared }

// Limits returns a copy of the currently active limits, for logging and the
// dashboard. The hot path does not use this.
func (r *Registry) Limits() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.buckets))
	for k, b := range r.buckets {
		out[k] = int(b.rps)
	}
	return out
}

// FromRaw normalises a modelRps value decoded from JSON. A JSON object decodes
// as map[string]any, so map[string]int is never populated automatically, and a
// client can send anything — entries that are not positive integers are
// dropped rather than turned into a limit of zero (which would throttle a
// model to nothing). Shared by the settings reader and the dashboard write
// path so both agree on what a valid limit is.
func FromRaw(v any) map[string]int {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]int, len(obj))
	for k, raw := range obj {
		f, ok := raw.(float64)
		if !ok || f <= 0 {
			continue
		}
		out[k] = int(f)
	}
	return out
}

// Load makes limits the registry's entire configuration, dropping every key
// that is not present. Buckets for surviving keys are discarded too, so a
// limit that was just edited starts from a full bucket rather than inheriting
// tokens burned under the old cap. Called on startup and on every settings
// write; the hot path only ever reads.
func (r *Registry) Load(limits map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.buckets {
		if limits[k] <= 0 {
			delete(r.buckets, k)
		}
	}
	for k, v := range limits {
		if v <= 0 {
			continue
		}
		if b, ok := r.buckets[k]; ok {
			b.rps = float64(v)
			b.tokens = float64(v)
			b.last = r.now()
			continue
		}
		r.buckets[k] = &bucket{tokens: float64(v), last: r.now(), rps: float64(v)}
	}
}

// SetLimit sets the requests-per-second ceiling for key, where a value of
// zero or less removes the limit. Changing a limit clamps the live bucket to
// the new burst instead of topping it up, so lowering the cap takes effect at
// once rather than after a refill.
func (r *Registry) SetLimit(key string, rps int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rps <= 0 {
		delete(r.buckets, key)
		return
	}
	b, ok := r.buckets[key]
	if !ok {
		r.buckets[key] = &bucket{tokens: float64(rps), last: r.now(), rps: float64(rps)}
		return
	}
	b.rps = float64(rps)
	if b.tokens > b.rps {
		b.tokens = b.rps
	}
}

// Allow consumes one token for key. It reports whether the request may
// proceed, and when it may not, how long the caller would have to wait for
// the next token (used for the Retry-After hint on the final 429).
func (r *Registry) Allow(key string) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[key]
	if !ok {
		return true, 0
	}
	now := r.now()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.rps
		if b.tokens > b.rps {
			b.tokens = b.rps
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / b.rps * float64(time.Second))
}
