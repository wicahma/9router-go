package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// advance rewinds a bucket's clock so refill can be tested without sleeping.
// Test-only: it reaches into the bucket because the production code has no
// way to move time.
func (r *Registry) advance(key string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.buckets[key]; ok {
		b.last = b.last.Add(-d)
	}
}

func TestAllowsUpToBurst(t *testing.T) {
	r := New()
	r.SetLimit("openai/gpt-4o", 10)
	allowed := 0
	for range 15 {
		if ok, _ := r.Allow("openai/gpt-4o"); ok {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("allowed = %d, want 10 (burst == rps)", allowed)
	}
}

func TestUnlimitedWhenNoLimitConfigured(t *testing.T) {
	r := New()
	for range 1000 {
		if ok, _ := r.Allow("openai/gpt-4o"); !ok {
			t.Fatal("model without a configured limit must never be throttled")
		}
	}
}

func TestRefillsOverTime(t *testing.T) {
	r := New()
	r.SetLimit("openai/gpt-4o", 10)
	for range 10 {
		if ok, _ := r.Allow("openai/gpt-4o"); !ok {
			t.Fatal("first 10 must be allowed")
		}
	}
	if ok, _ := r.Allow("openai/gpt-4o"); ok {
		t.Fatal("11th immediate request must be denied")
	}
	r.advance("openai/gpt-4o", 200*time.Millisecond)
	allowed := 0
	for range 5 {
		if ok, _ := r.Allow("openai/gpt-4o"); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed after 200ms at 10rps = %d, want 2", allowed)
	}
}

func TestDenialReportsPositiveWait(t *testing.T) {
	r := New()
	r.SetLimit("openai/gpt-4o", 10)
	for range 10 {
		r.Allow("openai/gpt-4o")
	}
	ok, d := r.Allow("openai/gpt-4o")
	if ok {
		t.Fatal("want denial")
	}
	if d <= 0 {
		t.Fatalf("wait = %v, want > 0", d)
	}
}

func TestLimitsAreIsolatedPerModel(t *testing.T) {
	r := New()
	r.SetLimit("openai/gpt-4o", 2)
	r.SetLimit("google/gemini-3.7-flash-high", 5)
	for range 2 {
		r.Allow("openai/gpt-4o")
	}
	if ok, _ := r.Allow("openai/gpt-4o"); ok {
		t.Fatal("gpt-4o should be exhausted")
	}
	if ok, _ := r.Allow("google/gemini-3.7-flash-high"); !ok {
		t.Fatal("gemini must be unaffected by gpt-4o's exhaustion")
	}
}

func TestLoweredLimitDoesNotGrantTokens(t *testing.T) {
	r := New()
	r.SetLimit("m", 100)
	for range 100 {
		r.Allow("m")
	}
	r.SetLimit("m", 5)
	if ok, _ := r.Allow("m"); ok {
		t.Fatal("lowering the limit must clamp tokens, not top the bucket up")
	}
}

func TestRemovedLimitRestoresUnlimited(t *testing.T) {
	r := New()
	r.SetLimit("m", 1)
	r.Allow("m")
	if ok, _ := r.Allow("m"); ok {
		t.Fatal("should be exhausted")
	}
	r.SetLimit("m", 0)
	for range 100 {
		if ok, _ := r.Allow("m"); !ok {
			t.Fatal("removing the limit must restore unlimited throughput")
		}
	}
}

func TestConcurrentAllowRespectsLimit(t *testing.T) {
	r := New()
	r.SetLimit("m", 50)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := r.Allow("m"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed > 50 {
		t.Fatalf("allowed = %d, must never exceed the 50-token burst", allowed)
	}
}
