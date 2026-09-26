package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"9router/proxy/internal/log"
	"9router/proxy/internal/usagetracker"
)

// WriteSSEHeaders sets standard SSE headers on the response and writes HTTP 200.
// Returns the http.Flusher if the ResponseWriter supports it.
func WriteSSEHeaders(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	if f != nil {
		f.Flush()
	}
	return f
}

// SSECopy reads from upstream in a raw loop and writes each chunk to the client.
// A simplified passthrough that does NOT parse SSE framing — use when translation is not needed.
// onChunk is called for each chunk before writing (for metrics/TTFT tracking).
// Returns the first upstream or write error so a truncated stream is not
// reported as a successful completion.
func SSECopy(w http.ResponseWriter, upstream io.Reader, flusher http.Flusher, onChunk func([]byte)) error {
	// Allocate a local buffer instead of using the shared pool. The buffer is
	// alive for the entire read loop, so there is no safe point to return it
	// to the pool, and the pool would add a race window between ReleaseByteSlice
	// and the next iteration's write/flush completing.
	buf := make([]byte, 4096)
	var tail [64]byte
	tailLen := 0
	hasTerminal := false
	hasDone := false
	seenSSE := false

	for {
		n, err := upstream.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if onChunk != nil {
				onChunk(chunk)
			}
			if _, werr := w.Write(chunk); werr != nil {
				return fmt.Errorf("write stream to client: %w", werr)
			}
			if flusher != nil {
				flusher.Flush()
			}

			var checkBuf []byte
			if tailLen > 0 {
				checkBuf = append(tail[:tailLen], chunk...)
			} else {
				checkBuf = chunk
			}
			if bytes.Contains(chunk, []byte("data:")) {
				seenSSE = true
			}
			if bytes.Contains(checkBuf, []byte("[DONE]")) {
				hasDone = true
				return nil
			}
			if bytes.Contains(checkBuf, []byte(`"finish_reason":`)) && !bytes.Contains(checkBuf, []byte(`"finish_reason":null`)) {
				hasTerminal = true
			}
			if bytes.Contains(checkBuf, []byte(`"stop_reason":`)) && !bytes.Contains(checkBuf, []byte(`"stop_reason":null`)) {
				hasTerminal = true
			}
			if bytes.Contains(checkBuf, []byte(`"message_stop"`)) {
				hasTerminal = true
			}

			if n >= 64 {
				copy(tail[:], chunk[n-64:])
				tailLen = 64
			} else {
				copy(tail[:], chunk)
				tailLen = n
			}
		}
		if err != nil {
			if err == io.EOF {
				if seenSSE && !hasDone {
					// PR #4079: If stream tail was not terminated with double newline, emit one
					// so [DONE] or the synthesized terminal does not merge with prior line.
					if tailLen > 0 && !bytes.HasSuffix(tail[:tailLen], []byte("\n\n")) {
						if bytes.HasSuffix(tail[:tailLen], []byte("\n")) {
							_, _ = w.Write([]byte("\n"))
						} else {
							_, _ = w.Write([]byte("\n\n"))
						}
					}
					// If upstream ended without finish_reason, synthesize network_error terminal
					// so clients like Oh My Pi do not fail with "Stream ended without finish_reason"
					if !hasTerminal {
						_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"network_error\"}]}\n\n"))
					}
					_, _ = w.Write([]byte("data: [DONE]\n\n"))
					if flusher != nil {
						flusher.Flush()
					}
				}
				return nil
			}
			return fmt.Errorf("read upstream stream: %w", err)
		}
	}
}

// DefaultHeartbeatInterval is the default period for sending SSE keep-alive ping comments.
// Set to 15 seconds so strict clients (Oh My Pi / Cline / Roo) with 30-60s idle timeouts
// never consider the stream stalled during prolonged thinking/reasoning phases.
const DefaultHeartbeatInterval = 15 * time.Second

// HeartbeatWriter wraps an http.ResponseWriter to periodically emit SSE keep-alive
// comments (": keep-alive\n\n") when no data has been written for the interval.
// Thread-safe: synchronizes concurrent writes, flushes, and heartbeat ticks.
type HeartbeatWriter struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	interval  time.Duration
	lastWrite time.Time
	mu        sync.Mutex
	stopCh    chan struct{}
	done      bool
	ctx       context.Context
	// streamMarked records that the first response byte reached the client.
	// Set once, inside the mutex Write already takes, so marking the flight
	// phase costs one branch per write and one map lookup per stream.
	streamMarked bool
}

// NewHeartbeatWriter starts a background ticker that emits ": keep-alive\n\n"
// every interval if no writes occurred since the last interval.
// Call Close() when the stream completes to terminate the ticker.
func NewHeartbeatWriter(ctx context.Context, w http.ResponseWriter, interval time.Duration) *HeartbeatWriter {
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	flusher, _ := w.(http.Flusher)
	hw := &HeartbeatWriter{
		w:         w,
		flusher:   flusher,
		interval:  interval,
		lastWrite: time.Now(),
		stopCh:    make(chan struct{}),
		ctx:       ctx,
	}
	go func() {
		ticker := time.NewTicker(hw.interval)
		defer ticker.Stop()
		for {
			if ctx != nil && ctx.Done() != nil {
				select {
				case <-hw.stopCh:
					return
				case <-ctx.Done():
					_ = hw.Close()
					return
				case <-ticker.C:
					hw.mu.Lock()
					if hw.done {
						hw.mu.Unlock()
						return
					}
					if time.Since(hw.lastWrite) >= hw.interval {
						if _, err := hw.w.Write([]byte(": keep-alive\n\n")); err == nil {
							if hw.flusher != nil {
								hw.flusher.Flush()
							}
						}
					}
					hw.mu.Unlock()
				}
			} else {
				select {
				case <-hw.stopCh:
					return
				case <-ticker.C:
					hw.mu.Lock()
					if hw.done {
						hw.mu.Unlock()
						return
					}
					if time.Since(hw.lastWrite) >= hw.interval {
						if _, err := hw.w.Write([]byte(": keep-alive\n\n")); err == nil {
							if hw.flusher != nil {
								hw.flusher.Flush()
							}
						}
					}
					hw.mu.Unlock()
				}
			}
		}
	}()
	return hw
}

func (hw *HeartbeatWriter) Header() http.Header {
	return hw.w.Header()
}

func (hw *HeartbeatWriter) WriteHeader(statusCode int) {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	hw.w.WriteHeader(statusCode)
}

func (hw *HeartbeatWriter) Write(b []byte) (int, error) {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	if hw.done {
		return 0, io.ErrClosedPipe
	}
	hw.lastWrite = time.Now()
	// First real byte to the client ends the upstream wait. The keep-alive
	// ticker writes to the underlying writer directly, so a heartbeat can
	// never flip the phase while the upstream is still silent.
	if !hw.streamMarked {
		hw.streamMarked = true
		if hw.ctx != nil {
			usagetracker.SetFlightPhase(log.RequestIDFromContext(hw.ctx), usagetracker.PhaseStream, "")
		}
	}
	n, err := hw.w.Write(b)
	return n, err
}

func (hw *HeartbeatWriter) Flush() {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	if hw.flusher != nil && !hw.done {
		hw.flusher.Flush()
	}
}

func (hw *HeartbeatWriter) Close() error {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	if !hw.done {
		hw.done = true
		close(hw.stopCh)
	}
	return nil
}
