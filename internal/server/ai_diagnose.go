package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/skyhook-io/radar/internal/ai"
)

// handleListAgents reports the local agent CLIs detected on PATH, for the OSS
// "AI Agent" picker. Safe: only fixed known names are probed (see ai.DetectAgents).
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	// Version probing is slow (execs `<cli> --version`); only do it when asked
	// (e.g. a settings/picker view) so the Diagnose button's check stays instant.
	withVersions := r.URL.Query().Get("versions") == "1"
	s.writeJSON(w, map[string]any{
		"agents":  ai.DetectAgents(r.Context(), withVersions),
		"enabled": s.aiDiagnoser != nil,
	})
}

// handleDiagnoseStream runs a read-only AI investigation of a resource and
// streams normalized events over SSE. Keyless — the agent CLI authenticates on
// the user's own subscription. GET (EventSource-compatible); params in the query.
func (s *Server) handleDiagnoseStream(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if kind == "" || name == "" {
		s.writeError(w, http.StatusBadRequest, "kind and name are required")
		return
	}
	// Multi-turn: a follow-up resumes the prior CLI session with the user's question.
	session := strings.TrimSpace(r.URL.Query().Get("session"))
	question := strings.TrimSpace(r.URL.Query().Get("q"))
	// Apply: a user-confirmed remediation turn (write tools enabled). Requires a
	// session to resume (the investigation it's applying the fix from). fix is the
	// exact recommended-fix text the user confirmed, so the apply is bound to it.
	apply := r.URL.Query().Get("apply") == "1" && session != ""
	fix := strings.TrimSpace(r.URL.Query().Get("fix"))

	if s.aiDiagnoser == nil {
		s.writeError(w, http.StatusNotImplemented, "no agent CLI available — install Claude Code to enable AI diagnosis")
		return
	}
	if !s.requireConnected(w) {
		return
	}
	// Namespace-scoped resources require read access to that namespace; the agent
	// also calls the MCP, which re-enforces RBAC, but gate here too for a clean error.
	if namespace != "" {
		if allowed := s.getUserNamespaces(r, []string{namespace}); noNamespaceAccess(allowed) {
			s.writeError(w, http.StatusForbidden, "no access to namespace "+namespace)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	send := func(ev ai.StreamEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
		flusher.Flush()
	}

	// onEvent is called synchronously from the parse loop (single goroutine), so
	// writing to w here is race-free.
	diag, err := s.aiDiagnoser.DiagnoseStream(r.Context(), ai.Request{
		Kind: kind, Namespace: namespace, Name: name, MCPPort: s.ActualPort(),
		SessionID: session, Question: question, Apply: apply, Fix: fix,
	}, send)
	if err != nil {
		if r.Context().Err() != nil {
			return // client disconnected / cancelled
		}
		send(ai.StreamEvent{Type: "error", Error: err.Error()})
		return
	}
	send(ai.StreamEvent{Type: "done", Diag: &diag})
}
