// KOMPASS SEAM 3: in-memory kubeconfig injection routes — see docs/SPEC.md ADR-001.
//
// Loopback-only HTTP surface for seam #3. These routes are reachable solely on
// the engine's loopback bind (seam #1) and are called by kompass-core's
// server-side engine client; kompass-core blocks browser access to
// /api/engine/kompass/* and authorizes injection (admin-only) and selection
// (per-cluster scope) at its gate. The engine performs NO authorization itself
// (it trusts loopback, per the two-container model) and holds no encrypted
// material — it receives already-decrypted bytes and keeps them in memory only.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skyhook-io/radar/internal/k8s"
)

type kompassInjectRequest struct {
	ClusterID  string `json:"cluster_id"`
	Kubeconfig string `json:"kubeconfig"`
}

// registerKompassSeamRoutes wires the injection/selection/eviction routes.
// Called by the single KOMPASS SEAM 3 hook in server.go's setupRoutes.
func (s *Server) registerKompassSeamRoutes(r chi.Router) {
	r.Post("/kompass/inject", s.handleKompassInject)
	r.Post("/kompass/select/{cluster_id}", s.handleKompassSelect)
	r.Delete("/kompass/inject/{cluster_id}", s.handleKompassEvict)
}

// handleKompassInject loads an already-decrypted kubeconfig into engine memory,
// keyed by cluster id. Errors are generic — the kubeconfig is never echoed.
func (s *Server) handleKompassInject(w http.ResponseWriter, r *http.Request) {
	var req kompassInjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid injection request")
		return
	}
	if req.ClusterID == "" || req.Kubeconfig == "" {
		s.writeError(w, http.StatusBadRequest, "cluster_id and kubeconfig are required")
		return
	}
	contextName, err := k8s.KompassInject(req.ClusterID, []byte(req.Kubeconfig))
	if err != nil {
		// Generic message; never include kubeconfig content or key material.
		s.writeError(w, http.StatusBadRequest, "could not load kubeconfig")
		return
	}
	s.writeJSON(w, map[string]string{
		"cluster_id":   req.ClusterID,
		"context_key":  "kompass:" + req.ClusterID,
		"context_name": contextName,
	})
}

// handleKompassSelect makes the injected cluster the active context.
func (s *Server) handleKompassSelect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "cluster_id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}
	if !k8s.KompassHasInjected(id) {
		s.writeError(w, http.StatusNotFound, "cluster is not injected")
		return
	}
	if err := k8s.PerformContextSwitch("kompass:" + id); err != nil {
		s.writeError(w, http.StatusBadGateway, "could not select cluster")
		return
	}
	s.writeJSON(w, map[string]string{"status": "selected", "cluster_id": id})
}

// handleKompassEvict removes a cluster's credential from engine memory.
func (s *Server) handleKompassEvict(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "cluster_id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}
	k8s.KompassEvict(id)
	w.WriteHeader(http.StatusNoContent)
}
