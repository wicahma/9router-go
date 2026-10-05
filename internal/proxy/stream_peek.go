package proxy

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Committing SSE headers is what makes a streaming fallback impossible: once
// the client holds a 200, the combo/account layers can no longer retry the
// next provider. An upstream that rejects a request (quota, auth, rate limit)
// answers in milliseconds — often as HTTP 200 with an error object in the body,
// which the stream path used to forward as a successful empty completion.
const (
	// ssePeekWindow bounds the wait for the first upstream line before headers
	// commit. A healthy-but-slow model pays only this much extra header
	// latency. It stays well under DefaultHeartbeatInterval: the heartbeat
	// writer starts after the peek, and a tick that fired before commit would
	// send ": keep-alive" under a not-yet-SSE Content-Type.
	ssePeekWindow = 3 * time.Second
	// maxPeekBytes caps the head buffered while waiting for the first line.
	maxPeekBytes = 64 << 10
)

// PeekStreamError reads the head of an upstream streaming response before any
// SSE header is committed and reports it as an *UpstreamError when it is an
// error payload rather than a stream. The caller must return that error
// unchanged so the combo/account fallback can move to the next provider while
// the response is still uncommitted.
//
// On success it returns a reader that replays every byte the peek consumed
// followed by the rest of upstream. A slow first token is not an error: when
// nothing arrives within ssePeekWindow the reader is returned and the caller
// commits headers and streams as before.
func PeekStreamError(upstream io.Reader) (io.Reader, error) {
	ch := make(chan peekResult, 1)
	go func() {
		head, err := readHead(upstream)
		ch <- peekResult{head: head, err: err}
	}()

	timer := time.NewTimer(ssePeekWindow)
	defer timer.Stop()
	var res peekResult
	select {
	case res = <-ch:
	case <-timer.C:
		// Still silent: a slow but healthy stream. Hand the reader back and
		// let the caller commit and stream as it did before.
		return &peekReader{ch: ch, upstream: upstream}, nil
	}

	if uerr := classifyStreamError(res.head); uerr != nil {
		return nil, uerr
	}
	if len(res.head) == 0 && res.err != nil {
		// 200 with an empty body: a broken turn, not a completion. A 502 lets
		// the fallback layer try the next provider.
		return nil, &UpstreamError{
			StatusCode: http.StatusBadGateway,
			Body:       []byte(`{"error":{"message":"upstream returned an empty stream","type":"upstream_error","code":502}}`),
		}
	}
	return io.MultiReader(bytes.NewReader(res.head), upstream), nil
}

type peekResult struct {
	head []byte
	err  error
}

// peekReader serves the head the peek goroutine is still fetching, then reads
// upstream directly. It is returned only on the slow path, where the goroutine
// may still be blocked in Read: the first Read waits on ch before touching
// upstream so the two never read concurrently.
type peekReader struct {
	ch       chan peekResult
	upstream io.Reader
	primed   bool
	head     []byte
}

func (p *peekReader) Read(b []byte) (int, error) {
	if !p.primed {
		p.primed = true
		res := <-p.ch
		p.head = res.head
		if len(p.head) == 0 && res.err != nil {
			return 0, res.err
		}
	}
	if len(p.head) > 0 {
		n := copy(b, p.head)
		p.head = p.head[n:]
		return n, nil
	}
	return p.upstream.Read(b)
}

// readHead reads up to the first line boundary, EOF, or maxPeekBytes.
func readHead(r io.Reader) ([]byte, error) {
	var head []byte
	buf := make([]byte, 4096)
	for len(head) < maxPeekBytes {
		n, err := r.Read(buf)
		if n > 0 {
			head = append(head, buf[:n]...)
			if bytes.Contains(head, []byte("\n")) {
				return head, nil
			}
		}
		if err != nil {
			return head, err
		}
	}
	return head, nil
}

// utf8BOM is stripped before classification so a BOM'd JSON body still reads
// as JSON.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ClassifyErrorBody applies the PeekStreamError rules to a fully-read body so
// the non-stream paths can fail over on a 200 that carries an error object
// instead of writing it as a successful empty completion. It returns nil for
// any body that is not a recognizable error.
func ClassifyErrorBody(body []byte) *UpstreamError {
	return classifyStreamError(body)
}

// classifyStreamError reports whether head is an upstream error payload rather
// than the start of a stream: a raw JSON error body (an upstream answering 200
// with an error object), an SSE `event: error` frame, a `data:` frame carrying
// an error object, or an HTML gateway page. It returns nil for anything it
// cannot name, so healthy first frames never trip it.
func classifyStreamError(head []byte) *UpstreamError {
	head = bytes.TrimSpace(bytes.TrimPrefix(head, utf8BOM))
	if len(head) == 0 {
		return nil
	}
	switch {
	case head[0] == '<':
		return &UpstreamError{StatusCode: http.StatusBadGateway, Body: head}
	case bytes.HasPrefix(head, []byte("event:")):
		line, _, _ := bytes.Cut(head, []byte("\n"))
		name := strings.TrimSpace(string(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("event:"))))
		if strings.EqualFold(name, "error") {
			return &UpstreamError{StatusCode: http.StatusBadGateway, Body: head}
		}
		return nil
	case bytes.HasPrefix(head, []byte("data:")):
		line, _, _ := bytes.Cut(head, []byte("\n"))
		payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			return nil
		}
		return jsonStreamError(payload, head)
	case head[0] == '{':
		return jsonStreamError(head, head)
	}
	return nil
}

// jsonStreamError names a JSON error object: a non-null "error" member, or a
// top-level "type" of "error" (the Anthropic streaming shape). The status the
// body implies is preserved so the fallback layer's rate-limit cooldown and
// reactive 401 token refresh still apply; anything unrecognized becomes a 502,
// which is retryable.
func jsonStreamError(payload, raw []byte) *UpstreamError {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil
	}
	_, hasError := m["error"]
	typeIsError, _ := m["type"].(string)
	if (!hasError || !nonEmptyJSON(m["error"])) && !strings.EqualFold(typeIsError, "error") {
		return nil
	}
	status := errorStatus(m)
	if status == 0 {
		status = http.StatusBadGateway
	}
	return &UpstreamError{StatusCode: status, Body: append([]byte(nil), raw...)}
}

// errorStatus reads the HTTP status an upstream error body implies. It only
// needs to be right about 401 (reactive token refresh), 429 (rate-limit
// cooldown) and the 5xx family; anything unrecognized falls back to 502.
func errorStatus(m map[string]any) int {
	nested, _ := m["error"].(map[string]any)
	for _, src := range []map[string]any{nested, m} {
		if src == nil {
			continue
		}
		for _, key := range []string{"code", "status"} {
			switch v := src[key].(type) {
			case float64:
				if n := int(v); n >= 400 && n <= 599 {
					return n
				}
			case string:
				if n, err := strconv.Atoi(v); err == nil && n >= 400 && n <= 599 {
					return n
				}
				if n := statusFromText(v); n != 0 {
					return n
				}
			}
		}
	}
	for _, src := range []map[string]any{nested, m} {
		if src == nil {
			continue
		}
		if s, _ := src["type"].(string); s != "" {
			if n := statusFromText(s); n != 0 {
				return n
			}
		}
	}
	return 0
}

// statusFromText maps the provider error type strings that carry a status:
// [OI]'s rate_limit_error, Google's RESOURCE_EXHAUSTED, Anthropic's
// overloaded_error, and the auth variants.
func statusFromText(s string) int {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "rate") || strings.Contains(l, "quota"),
		strings.Contains(l, "resource_exhausted"), strings.Contains(l, "overloaded"):
		return http.StatusTooManyRequests
	case strings.Contains(l, "unauthorized"), strings.Contains(l, "invalid_api_key"),
		strings.Contains(l, "authentication"):
		return http.StatusUnauthorized
	case strings.Contains(l, "permission"), strings.Contains(l, "forbidden"):
		return http.StatusForbidden
	case strings.Contains(l, "unavailable"), strings.Contains(l, "server_error"),
		strings.Contains(l, "internal"):
		return http.StatusServiceUnavailable
	}
	return 0
}

// nonEmptyJSON reports whether a decoded JSON value carries anything:
// {"error":null} and {"error":""} are not errors.
func nonEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	}
	return true
}
