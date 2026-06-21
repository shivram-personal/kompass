package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunManager owns AI investigations as durable, server-side jobs. An investigation
// runs in a goroutine bound to a manager-owned context — NOT to any HTTP request —
// so it keeps going when the browser closes the panel, navigates away, or refreshes.
// Clients subscribe to a run's event stream (with replay) to watch live or catch up.
//
// In-memory only: runs are lost when the radar process restarts. The feature is
// gated to no-auth standalone radar, so a single local user owns all runs.
//
// Locking: the manager mutex (m.mu) guards the runs map/order. Each Run's mutable
// state (status, session, events, subs, …) is guarded by r.mu. Immutable identity
// fields (ID/Kind/Namespace/Name/Context/CreatedAt) are set once and read freely.
// Lock order is always m.mu → r.mu, never the reverse; the run goroutine never
// takes m.mu.
type RunManager struct {
	d        *Diagnoser
	mcpPort  func() int    // resolved lazily — the listener port isn't known at construction
	ctxLabel func() string // current kube-context label, for the run's baseline

	mu            sync.Mutex
	runs          map[string]*Run
	order         []string // insertion order, for eviction
	nextID        int
	maxRetained   int // total runs kept in memory (running + finished)
	maxConcurrent int // concurrent running cap
}

// Run is one investigation: identity, status, the agent session to resume, and the
// canonical append-only event log (every subscriber reconstructs state from it).
type Run struct {
	ID        string // immutable
	Kind      string // immutable
	Namespace string // immutable
	Name      string // immutable
	Context   string // immutable — kube-context the run is about (baseline)
	CreatedAt time.Time

	mu        sync.Mutex
	status    string // running | done | error | stopped | stale
	sessionID string
	preview   string // last root cause, for the list
	updatedAt time.Time
	events    []RunEvent
	inFlight  bool
	subs      map[int]chan RunEvent
	nextSub   int
	cancel    context.CancelFunc
}

// RunEvent is a sequenced stream event. Seq drives SSE id: / Last-Event-ID replay.
type RunEvent struct {
	Seq   int         `json:"seq"`
	Event StreamEvent `json:"event"`
}

// RunSummary is an immutable snapshot of a run (no event log) for JSON responses.
type RunSummary struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Context   string    `json:"context"`
	Status    string    `json:"status"`
	SessionID string    `json:"sessionId,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var (
	// ErrAtCapacity is returned by Start when too many investigations are running.
	ErrAtCapacity = errors.New("too many investigations running")
	// ErrRunNotFound is returned for an unknown run id.
	ErrRunNotFound = errors.New("investigation not found")
	// ErrTurnInFlight is returned when a turn is already running for a run.
	ErrTurnInFlight = errors.New("a turn is already running")
	// ErrNoSession is returned when a follow-up/apply is attempted before the
	// agent has produced a resumable session id.
	ErrNoSession = errors.New("investigation has no resumable session yet")
	// ErrStale is returned when continuing a run whose cluster context changed.
	ErrStale = errors.New("investigation ran against a different cluster")
)

const (
	defaultMaxConcurrent = 3  // running child processes
	defaultMaxRetained   = 20 // total runs kept in memory
)

// NewRunManager builds a manager over a resolved Diagnoser. mcpPort/ctxLabel are
// callbacks because the listener port and kube-context are only known at runtime.
func NewRunManager(d *Diagnoser, mcpPort func() int, ctxLabel func() string) *RunManager {
	return &RunManager{
		d:             d,
		mcpPort:       mcpPort,
		ctxLabel:      ctxLabel,
		runs:          map[string]*Run{},
		maxRetained:   defaultMaxRetained,
		maxConcurrent: defaultMaxConcurrent,
	}
}

func (m *RunManager) ctx() string {
	if m.ctxLabel != nil {
		return m.ctxLabel()
	}
	return ""
}

// Start creates and launches an investigation, or focuses an existing live run for
// the same target+context instead of duplicating it. Returns ErrAtCapacity when
// the concurrent-running cap is reached.
func (m *RunManager) Start(kind, namespace, name string) (RunSummary, error) {
	cur := m.ctx()
	m.mu.Lock()
	running := 0
	for _, id := range m.order {
		r := m.runs[id]
		st := r.snapshotStatus()
		if st == "running" {
			running++
			if r.Kind == kind && r.Namespace == namespace && r.Name == name && r.Context == cur {
				m.mu.Unlock()
				return r.Summary(), nil
			}
		}
	}
	if running >= m.maxConcurrent {
		m.mu.Unlock()
		return RunSummary{}, ErrAtCapacity
	}
	m.nextID++
	r := &Run{
		ID: fmt.Sprintf("run-%d", m.nextID), Kind: kind, Namespace: namespace,
		Name: name, Context: cur, CreatedAt: nowUTC(),
		status: "running", updatedAt: nowUTC(), subs: map[int]chan RunEvent{},
	}
	m.runs[r.ID] = r
	m.order = append(m.order, r.ID)
	m.evictLocked()
	m.mu.Unlock()

	m.launchTurn(r, "", false, "")
	return r.Summary(), nil
}

// AddTurn runs a follow-up (question) or an apply turn (with the confirmed fix).
// Requires a resumable session + no in-flight turn + a non-stale run.
func (m *RunManager) AddTurn(id, question string, apply bool, fix string) error {
	r := m.get(id)
	if r == nil {
		return ErrRunNotFound
	}
	r.mu.Lock()
	switch {
	case r.inFlight:
		r.mu.Unlock()
		return ErrTurnInFlight
	case r.status == "stale":
		r.mu.Unlock()
		return ErrStale
	case r.sessionID == "":
		r.mu.Unlock()
		return ErrNoSession
	}
	r.mu.Unlock()
	m.launchTurn(r, question, apply, fix)
	return nil
}

// launchTurn emits a turn marker then runs the agent in a manager-owned goroutine.
// Subscribers stay attached across turns — only an explicit stop / stale / evict
// closes them.
func (m *RunManager) launchTurn(r *Run, question string, apply bool, fix string) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.inFlight = true
	r.status = "running"
	r.updatedAt = nowUTC()
	r.cancel = cancel
	session := r.sessionID
	r.mu.Unlock()

	r.append(StreamEvent{Type: "turn", Question: question, Apply: apply})

	go func() {
		defer cancel()
		diag, err := m.d.DiagnoseStream(ctx, Request{
			Kind: r.Kind, Namespace: r.Namespace, Name: r.Name,
			MCPPort: m.mcpPort(), SessionID: session,
			Question: question, Apply: apply, Fix: fix,
		}, func(ev StreamEvent) { r.append(ev) })

		r.mu.Lock()
		r.inFlight = false
		r.updatedAt = nowUTC()
		stopped := r.status == "stopped"
		stale := r.status == "stale"
		switch {
		case err != nil && (stopped || stale):
			// Status already set by Stop/OnContextSwitch; nothing more to record.
			r.mu.Unlock()
		case err != nil:
			r.status = "error"
			r.mu.Unlock()
			r.append(StreamEvent{Type: "error", Error: err.Error()})
		default:
			if diag.SessionID != "" {
				r.sessionID = diag.SessionID
			}
			if diag.RootCause != "" {
				r.preview = diag.RootCause
			}
			r.status = "done"
			r.mu.Unlock()
			r.append(StreamEvent{Type: "done", Diag: &diag})
		}
	}()
}

// Stop cancels a run's in-flight agent (killing its process group) and marks it stopped.
func (m *RunManager) Stop(id string) error {
	r := m.get(id)
	if r == nil {
		return ErrRunNotFound
	}
	r.mu.Lock()
	if !r.inFlight {
		r.mu.Unlock()
		return nil // nothing to stop
	}
	r.status = "stopped"
	c := r.cancel
	r.mu.Unlock()
	if c != nil {
		c() // the run goroutine sees status=stopped and won't overwrite it
	}
	r.append(StreamEvent{Type: "error", Error: "Investigation stopped."})
	return nil
}

// OnContextSwitch cancels running investigations and marks every run stale + closed:
// their reasoning is about the previous cluster, so they can't safely continue or
// apply against the newly-connected one.
func (m *RunManager) OnContextSwitch() {
	m.mu.Lock()
	runs := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		r.mu.Lock()
		c := r.cancel
		r.status = "stale"
		r.mu.Unlock()
		if c != nil {
			c()
		}
		r.append(StreamEvent{Type: "error", Error: "Cluster context changed — this investigation was about a different cluster."})
		r.finalize()
	}
}

// Get returns a run by id (nil if unknown).
func (m *RunManager) Get(id string) *Run { return m.get(id) }

func (m *RunManager) get(id string) *Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[id]
}

// List returns run summaries, newest first.
func (m *RunManager) List() []RunSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RunSummary, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, m.runs[m.order[i]].Summary())
	}
	return out
}

// evictLocked drops the oldest finished run when over the retention cap. Running
// runs are never evicted. Caller holds m.mu.
func (m *RunManager) evictLocked() {
	for len(m.order) > m.maxRetained {
		idx := -1
		for i, id := range m.order {
			if m.runs[id].snapshotStatus() != "running" {
				idx = i
				break
			}
		}
		if idx < 0 {
			return // all running — keep them
		}
		id := m.order[idx]
		victim := m.runs[id]
		delete(m.runs, id)
		m.order = append(m.order[:idx], m.order[idx+1:]...)
		victim.finalize()
	}
}

// Summary snapshots a run's current state under r.mu.
func (r *Run) Summary() RunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RunSummary{
		ID: r.ID, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name,
		Context: r.Context, Status: r.status, SessionID: r.sessionID,
		Preview: r.preview, CreatedAt: r.CreatedAt, UpdatedAt: r.updatedAt,
	}
}

func (r *Run) snapshotStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Subscribe returns the backlog after afterSeq plus a channel of future events.
// The channel is closed only when the run is finalized (stale/evicted) — NOT when
// a turn completes, so the same subscription sees later turns.
func (r *Run) Subscribe(afterSeq int) (backlog []RunEvent, ch <-chan RunEvent, cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Seq > afterSeq {
			backlog = append(backlog, e)
		}
	}
	c := make(chan RunEvent, 256)
	if r.subs == nil { // finalized run — replay then close
		close(c)
		return backlog, c, func() {}
	}
	id := r.nextSub
	r.nextSub++
	r.subs[id] = c
	return backlog, c, func() {
		r.mu.Lock()
		if ch, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(ch)
		}
		r.mu.Unlock()
	}
}

// append records an event and fans it out non-blockingly. A subscriber whose buffer
// is full is dropped (it reconnects with Last-Event-ID to replay).
func (r *Run) append(ev StreamEvent) {
	r.mu.Lock()
	re := RunEvent{Seq: len(r.events) + 1, Event: ev}
	r.events = append(r.events, re)
	r.updatedAt = nowUTC()
	for id, ch := range r.subs {
		select {
		case ch <- re:
		default:
			delete(r.subs, id)
			close(ch)
		}
	}
	r.mu.Unlock()
}

// finalize emits a terminal sentinel and closes all subscribers; further Subscribe
// calls replay the log then close. Used when a run can no longer produce turns.
func (r *Run) finalize() {
	r.append(StreamEvent{Type: "closed"})
	r.mu.Lock()
	for id, ch := range r.subs {
		delete(r.subs, id)
		close(ch)
	}
	r.subs = nil
	r.mu.Unlock()
}

func nowUTC() time.Time { return time.Now().UTC() }
