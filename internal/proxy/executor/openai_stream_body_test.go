package executor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/proxy"
	"9router/proxy/internal/providers"
)

// A streaming request whose upstream answers 200 with a body that is not an
// event stream used to be piped through the SSE scanner: zero frames in, a
// clean [DONE] out, and no error for the fallback layer to act on. The
// upstream's own Content-Type is the only pre-header signal, so it is checked
// first and the body is then classified the same way a non-streaming request
// would be.
func TestForwardOpenAI_StreamRequestWithNonStreamBody(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantErr     bool
		wantStatus  int
	}{
		{
			name:        "html error page behind a 200",
			contentType: "text/html",
			body:        "<html><body>502 Bad Gateway</body></html>",
			wantErr:     true,
			wantStatus:  http.StatusBadGateway,
		},
		{
			name:        "blank 200",
			contentType: "application/json",
			body:        "",
			wantErr:     true,
			wantStatus:  http.StatusBadGateway,
		},
		{
			name:        "200 error envelope",
			contentType: "application/json",
			body:        `{"error":{"message":"model overloaded","type":"server_error"}}`,
			wantErr:     true,
			// The local stream classifier preserves the status the envelope names.
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:        "a completion sent as JSON still serves the client",
			contentType: "application/json",
			body:        `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`,
			wantErr:     false,
		},
		{
			name:        "an event stream mislabelled as JSON still streams",
			contentType: "application/json",
			body:        "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			rec := httptest.NewRecorder()
			err := ForwardOpenAI(rec, &Request{
				Ctx:      t.Context(),
				Client:   srv.Client(),
				Config:   &providers.ProviderConfig{BaseURL: srv.URL + "/chat/completions"},
				APIKey:   "k",
				Body:     []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`),
				IsStream: true,
			})

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ForwardOpenAI: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("served a 200 with no answer as a stream: %s", rec.Body.String())
			}
			var ue *proxy.UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("error is %T, want *proxy.UpstreamError", err)
			}
			// The local stream classifier (internal/proxy/stream_peek.go) preserves the
			// status the envelope names, so a server_error surfaces as 503. That status
			// is in providers.RetryableStatusCodes, so failover still happens.
			if ue.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d so the router fails over", ue.StatusCode, tt.wantStatus)
			}
		})
	}
}
