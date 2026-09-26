package usagetracker

import (
	"sort"
	"sync"
	"time"
)

// Request phases, in the order a request normally passes through them.
// A flight sitting in one phase far longer than its peers is the actionable
// signal: "queue" for a saturated pool, "db" for lock contention, "upstream"
// for a provider that accepted the connection and went quiet.
const (
	PhaseQueue    = "queue"
	PhaseDB       = "db"
	PhaseUpstream = "upstream"
	PhaseStream   = "stream"
	PhaseDone     = "done"
)

const (
	// flightTTL drops entries whose owning request never reported completion
	// (panicked handler, killed process). Without it a single leak pins a row
	// in the dashboard forever.
	flightTTL = 15 * time.Minute
	// maxFlights is a hard ceiling so a leak can never grow the map without
	// bound. Registration is dropped rather than queued when full: a full table
	// already means something is wrong, and blocking a real request to record
	// telemetry about it would be backwards.
	maxFlights = 512
	// maxFlightsInPayload bounds how many rows one SSE broadcast carries.
	maxFlightsInPayload = 50
)

// Flight is one in-flight request, identified by the X-Request-ID assigned by
// middleware so it can be matched against server logs.
type Flight struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Account   string `json:"account"`
	Phase     string `json:"phase"`
	Detail    string `json:"detail"`
	StartedMs int64  `json:"startedMs"`
	PhaseMs   int64  `json:"phaseMs"`
	AgeMs     int64  `json:"ageMs"`
	Attempt   int    `json:"attempt"`
}

type flightEntry struct {
	id       string
	model    string
	provider string
	account  string
	phase    string
	detail   string
	started  time.Time
	phaseAt  time.Time
	attempt  int
}

var (
	flightsMu sync.Mutex
	flights   = make(map[string]*flightEntry, 64)
)

// lockFlights acquires the flight table without ever blocking the caller past a
// short attempt. Telemetry must not become a latency source: if another
// goroutine holds the lock, this request simply skips its phase update and
// keeps going. Losing one sample is free; making a user wait for a dashboard
// row is not.
//
// TryLock first keeps the uncontended path at a single atomic CAS, with no
// futex involvement at all.
func lockFlights() bool { return flightsMu.TryLock() }

func unlockFlights() { flightsMu.Unlock() }

// StartFlight registers an in-flight request. Repeated calls with the same id
// refresh its phase instead of adding a row, so a combo retry shows one entry
// whose attempt counter climbs.
func StartFlight(id, model, provider, account string) {
	if id == "" || !lockFlights() {
		return
	}
	defer unlockFlights()

	if e, ok := flights[id]; ok {
		e.model, e.provider, e.account = model, provider, account
		return
	}
	if len(flights) >= maxFlights {
		return
	}
	now := time.Now()
	flights[id] = &flightEntry{
		id: id, model: model, provider: provider, account: account,
		phase: PhaseQueue, started: now, phaseAt: now,
	}
}

// SetFlightPhase moves a request into a new phase and, for wait phases, records
// why it is waiting.
func SetFlightPhase(id, phase, detail string) {
	if id == "" || !lockFlights() {
		return
	}
	defer unlockFlights()

	e, ok := flights[id]
	if !ok {
		return
	}
	if e.phase == phase && e.detail == detail {
		return
	}
	e.phase = phase
	e.detail = detail
	e.phaseAt = time.Now()
}

// SetFlightTarget records which model/connection the request actually landed
// on. The middleware only knows the HTTP path, so this fills in the real
// target once routing has picked one.
func SetFlightTarget(id, model, provider, account string) {
	if id == "" || !lockFlights() {
		return
	}
	defer unlockFlights()

	if e, ok := flights[id]; ok {
		e.model, e.provider, e.account = model, provider, account
	}
}

// SetFlightAttempt records which upstream attempt (combo pass / connection try)
// the request is on. attempt is 1-based.
func SetFlightAttempt(id string, attempt int) {
	if id == "" || !lockFlights() {
		return
	}
	defer unlockFlights()

	if e, ok := flights[id]; ok {
		e.attempt = attempt
	}
}

// EndFlight removes a request once its response is fully written.
func EndFlight(id string) {
	if id == "" || !lockFlights() {
		return
	}
	defer unlockFlights()

	delete(flights, id)
}

// SnapshotFlights returns the live flights, oldest first, dropping stale entries.
// It blocks (unlike the writers) because it runs in the SSE broadcast goroutine,
// never on a request path.
func SnapshotFlights() []Flight {
	now := time.Now()
	flightsMu.Lock()
	defer flightsMu.Unlock()

	out := make([]Flight, 0, min(len(flights), maxFlightsInPayload))
	for id, e := range flights {
		if now.Sub(e.started) > flightTTL {
			delete(flights, id)
			continue
		}
		out = append(out, Flight{
			ID:        e.id,
			Model:     e.model,
			Provider:  e.provider,
			Account:   e.account,
			Phase:     e.phase,
			Detail:    e.detail,
			StartedMs: e.started.UnixMilli(),
			PhaseMs:   now.Sub(e.phaseAt).Milliseconds(),
			AgeMs:     now.Sub(e.started).Milliseconds(),
			Attempt:   e.attempt,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StartedMs < out[j].StartedMs })
	if len(out) > maxFlightsInPayload {
		out = out[len(out)-maxFlightsInPayload:]
	}
	return out
}
