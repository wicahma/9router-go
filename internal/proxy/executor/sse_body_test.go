package executor

import (
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/proxy"
)

// jsonResponse now folds an event stream into one chat.completion, so the
// risk this test guards is a false positive: an ordinary JSON body must reach
// the client byte-for-byte. Every other provider on this path (openai, iflow,
// kimchi, commandcode, opencode) answers with plain JSON.
func TestJSONResponse_LeavesPlainJSONUntouched(t *testing.T) {
	const upstream = `{"id":"chatcmpl-7","object":"chat.completion","created":9,` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"plain"},"finish_reason":"stop"}]}`

	rec := httptest.NewRecorder()
	if err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil); err != nil {
		t.Fatalf("jsonResponse: %v", err)
	}
	if got := rec.Body.String(); got != upstream {
		t.Errorf("body was rewritten.\n got: %s\nwant: %s", got, upstream)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// An error envelope from a provider that does not speak SSE must fail over,
// not pass through as a 200: the client would otherwise treat it as a
// successful completion and the account/combo fallback would never fire.
func TestJSONResponse_ErrorEnvelopeFailsOver(t *testing.T) {
	const upstream = `{"error":{"message":"model not found","type":"invalid_request_error","code":404}}`

	rec := httptest.NewRecorder()
	err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil)
	ue, ok := err.(*proxy.UpstreamError)
	if !ok {
		t.Fatalf("expected *proxy.UpstreamError, got %T (%v)", err, err)
	}
	if ue.StatusCode != 404 {
		t.Errorf("status: got %d want 404", ue.StatusCode)
	}
	if rec.Code != 200 {
		t.Errorf("nothing may be committed before fallback: got %d", rec.Code)
	}
}
