package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPeekStreamError_NormalSSEPassesThrough(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	r, err := PeekStreamError(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != body {
		t.Errorf("stream not replayed intact:\n got %q\nwant %q", got, body)
	}
}

func TestPeekStreamError_JSONErrorBodyIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"rate limit", `{"error":{"message":"slow down","type":"rate_limit_error","code":429}}`, http.StatusTooManyRequests},
		{"auth", `{"error":{"message":"bad key","type":"invalid_api_key","code":"401"}}`, http.StatusUnauthorized},
		{"overload by type", `{"error":{"message":"busy","type":"overloaded_error"}}`, http.StatusTooManyRequests},
		{"codex error event", `{"type":"error","error":{"type":"server_error","message":"boom"}}`, http.StatusServiceUnavailable},
		{"bare error object", `{"error":"internal failure"}`, http.StatusBadGateway},
		{"html gateway", `<!DOCTYPE html><html><body>502</body></html>`, http.StatusBadGateway},
		{"sse error event", "event: error\ndata: {\"message\":\"nope\"}\n\n", http.StatusBadGateway},
		{"sse data error", "data: {\"error\":{\"message\":\"quota\",\"code\":429}}\n\n", http.StatusTooManyRequests},
		{"empty body", ``, http.StatusBadGateway},
		{"provider error envelope", `{"code":11148,"msg":"tool calls and tool results do not match, please start a new conversation and retry","requestId":"9dbbd213","extError":{"code":"invalid_request_error","message":"tool calls and tool results do not match","StatusCode":400}}`, http.StatusBadRequest},
		{"provider envelope via msg", `{"code":50001,"msg":"quota exceeded"}`, http.StatusBadGateway},
		{"provider envelope via message", `{"code":50002,"message":"upstream busy"}`, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := PeekStreamError(strings.NewReader(tc.body))
			if r != nil {
				t.Errorf("expected nil reader on error body")
			}
			ue, ok := err.(*UpstreamError)
			if !ok {
				t.Fatalf("expected *UpstreamError, got %T (%v)", err, err)
			}
			if ue.StatusCode != tc.want {
				t.Errorf("status: got %d want %d", ue.StatusCode, tc.want)
			}
		})
	}
}

func TestPeekStreamError_NonErrorJSONIsNotAnError(t *testing.T) {
	body := `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"message":{"content":"hi"}}]}`
	r, err := PeekStreamError(strings.NewReader(body))
	if err != nil {
		t.Fatalf("valid completion body must not be an error, got %v", err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != body {
		t.Errorf("body not replayed intact: %q", got)
	}
}

func TestPeekStreamError_NullErrorIsNotAnError(t *testing.T) {
	body := "data: {\"error\":null,\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"
	if _, err := PeekStreamError(strings.NewReader(body)); err != nil {
		t.Fatalf("{\"error\":null} must not be an error, got %v", err)
	}
}

func TestPeekStreamError_SlowUpstreamStillStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the peek window")
	}
	pr, pw := io.Pipe()
	go func() {
		time.Sleep(ssePeekWindow + 500*time.Millisecond)
		pw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n"))
		pw.Close()
	}()
	r, err := PeekStreamError(pr)
	if err != nil {
		t.Fatalf("a silent-but-healthy upstream must not fail, got %v", err)
	}
	got, rerr := io.ReadAll(r)
	if rerr != nil {
		t.Fatalf("read slow stream: %v", rerr)
	}
	if !bytes.Contains(got, []byte("late")) {
		t.Errorf("late first chunk was lost: %q", got)
	}
}

func TestClassifyErrorBody_CompletionIsNotAnError(t *testing.T) {
	if ue := ClassifyErrorBody([]byte(`{"choices":[{"message":{"content":"ok"}}]}`)); ue != nil {
		t.Errorf("valid body classified as error: %v", ue)
	}
	if ue := ClassifyErrorBody([]byte(`{"error":{"message":"x","code":429}}`)); ue == nil {
		t.Error("error body not classified")
	}
}

func TestPeekStreamError_HealthyBodiesAreNotErrorEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"completion chunk", "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"},
		{"usage-only chunk", "data: {\"id\":\"x\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"},
		{"non-stream completion", `{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1}}`},
		{"code with choices", `{"code":200,"choices":[{"delta":{"content":"hi"}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := PeekStreamError(strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("healthy body classified as error: %v", err)
			}
			if r == nil {
				t.Fatal("expected a reader for a healthy body")
			}
			got, _ := io.ReadAll(r)
			if string(got) != tc.body {
				t.Errorf("body not replayed intact:\n got %q\nwant %q", got, tc.body)
			}
		})
	}
}
