package handlerutil

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"sync/atomic"

	"9router/proxy/internal/constants"
)

// deterministicJSON makes Go maps serialize with their keys in sorted order, so
// the same data always produces byte-identical JSON. Without it, encoding/json/v2
// marshals maps in the runtime's randomized iteration order, which makes list
// order shift between requests (e.g. the dashboard models list on every refresh).
var deterministicJSON = json.Deterministic(true)

// errorTypes maps HTTP status codes to OpenAI-compatible error types and codes.
var errorTypes = map[int]struct {
	errType string
	errCode string
}{
	http.StatusBadRequest:          {errType: "invalid_request_error", errCode: "bad_request"},
	http.StatusUnauthorized:        {errType: "authentication_error", errCode: "invalid_api_key"},
	http.StatusPaymentRequired:     {errType: "billing_error", errCode: "payment_required"},
	http.StatusForbidden:           {errType: "permission_error", errCode: "insufficient_quota"},
	http.StatusNotFound:            {errType: "invalid_request_error", errCode: "model_not_found"},
	http.StatusMethodNotAllowed:    {errType: "invalid_request_error", errCode: "method_not_allowed"},
	http.StatusNotAcceptable:       {errType: "invalid_request_error", errCode: "model_not_supported"},
	http.StatusTooManyRequests:     {errType: "rate_limit_error", errCode: "rate_limit_exceeded"},
	http.StatusInternalServerError: {errType: "server_error", errCode: "internal_server_error"},
	http.StatusBadGateway:          {errType: "server_error", errCode: "bad_gateway"},
	http.StatusServiceUnavailable:  {errType: "server_error", errCode: "service_unavailable"},
	http.StatusGatewayTimeout:      {errType: "server_error", errCode: "gateway_timeout"},
}

// WriteJSONError writes a standardized JSON error response with status-code-aware
// error types matching OpenAI API conventions.
func WriteJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set(constants.HeaderContentType, constants.ContentTypeJSON)
	w.WriteHeader(status)

	errType := "invalid_request_error"
	errCode := fmt.Sprintf("%d", status)
	if t, ok := errorTypes[status]; ok {
		errType = t.errType
		errCode = t.errCode
	}

	errResp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    errCode,
		},
	}
	if err := json.MarshalWrite(w, errResp, deterministicJSON); err != nil {
		w.Write([]byte(`{"error":{"message":"internal error","type":"server_error","code":"internal_server_error"}}`))
	}
}

// WriteJSON writes a JSON response directly to the ResponseWriter with zero intermediate byte buffering.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set(constants.HeaderContentType, constants.ContentTypeJSON)
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, data, deterministicJSON); err != nil {
		w.Write([]byte(`{"error":{"message":"internal error","type":"invalid_request_error","code":500}}`))
	}
}

// UpdateModelInBody returns a copy of body with the "model" field set to modelName.
func UpdateModelInBody(body []byte, modelName string) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return body
	}
	m["model"] = modelName
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// SetAuthHeader applies the provider's auth scheme to the request.
func SetAuthHeader(req *http.Request, apiKey, authHeader, authScheme string) {
	if authHeader == "" {
		authHeader = "Authorization"
	}
	switch authScheme {
	case "bearer":
		req.Header.Set(authHeader, "Bearer "+apiKey)
	case "raw":
		req.Header.Set(authHeader, apiKey)
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

// GetString safely extracts a string value from a map[string]any by key.
func GetString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SessionHeaderKeys defines the client request header keys that carry session IDs in priority order.
var SessionHeaderKeys = []string{
	"x-claude-code-session-id",
	"x-session-id",
	"session-id",
	"session_id",
	"x-amp-thread-id",
}

// ExtractSessionID extracts the session identifier from incoming HTTP request headers.
func ExtractSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range SessionHeaderKeys {
		if val := r.Header.Get(key); val != "" {
			return val
		}
	}
	return ""
}

type sessionIDKey struct{}

// WithSessionID returns a context carrying the given session ID.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// GetSessionID retrieves the session ID from the context if present.
func GetSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return val
	}
	return ""
}

type attemptCountKey struct{}

// WithAttemptCounter installs a per-request upstream attempt counter. Install it
// where the request context is created: fallback and combo loops rebuild the
// connection on every retry but keep the context, so the counter is the only
// place the total number of upstream tries survives to logging time.
func WithAttemptCounter(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptCountKey{}, &atomic.Int64{})
}

// CountAttempt records one upstream forward attempt.
func CountAttempt(ctx context.Context) {
	if ctx == nil {
		return
	}
	if c, ok := ctx.Value(attemptCountKey{}).(*atomic.Int64); ok {
		c.Add(1)
	}
}

// GetAttempts returns the upstream attempts recorded on this context, or 1 when
// no counter is installed — a request that reached the forward path tried at
// least once, so 1 is the honest floor, not 0.
func GetAttempts(ctx context.Context) int {
	if ctx != nil {
		if c, ok := ctx.Value(attemptCountKey{}).(*atomic.Int64); ok {
			if n := int(c.Load()); n > 0 {
				return n
			}
		}
	}
	return 1
}
