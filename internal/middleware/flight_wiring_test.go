package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"9router/proxy/internal/usagetracker"
)

// TestRequestLogger_TracksUpstreamFlights verifies the wiring end to end: a
// request through the middleware appears in the flight table while it is being
// served, carries the phase the handler set, and is gone once it returns.
// Registration is done by the middleware precisely so this cannot drift.
func TestRequestLogger_TracksUpstreamFlights(t *testing.T) {
	reqID := "req_mw_flight_test"
	entered := make(chan struct{})
	release := make(chan struct{})

	h := RequestID(RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usagetracker.SetFlightPhase(reqID, usagetracker.PhaseUpstream, "openai/gpt-4o")
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	})))

	srv := httptest.NewServer(h)
	defer srv.Close()

	go func() {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", nil)
		req.Header.Set("X-Request-ID", reqID)
		srv.Client().Do(req)
	}()

	<-entered

	var seen *usagetracker.Flight
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range usagetracker.SnapshotFlights() {
			if f.ID == reqID {
				cp := f
				seen = &cp
			}
		}
		if seen != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if seen == nil {
		close(release)
		t.Fatal("in-flight request was not registered by the middleware")
	}
	if seen.Phase != usagetracker.PhaseUpstream || seen.Detail != "openai/gpt-4o" {
		t.Errorf("expected upstream phase with detail, got %+v", *seen)
	}

	close(release)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		still := false
		for _, f := range usagetracker.SnapshotFlights() {
			if f.ID == reqID {
				still = true
			}
		}
		if !still {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("flight was not removed when the request returned")
}

// TestRequestLogger_IgnoresNonUpstreamPaths keeps dashboard polling and asset
// traffic out of the table; they finish in microseconds and would only dilute
// the view.
func TestRequestLogger_IgnoresNonUpstreamPaths(t *testing.T) {
	for _, path := range []string{"/health", "/api/usage/stats", "/providers/openai.png"} {
		if isUpstreamPath(path) {
			t.Errorf("%s must not be tracked as in-flight", path)
		}
	}
	for _, path := range []string{"/chat/completions", "/messages", "/search", "/scrape", "/images/generations"} {
		if !isUpstreamPath(path) {
			t.Errorf("%s must be tracked as in-flight", path)
		}
	}
}
