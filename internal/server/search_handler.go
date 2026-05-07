package server

import (
	"net/http"
	"strconv"

	"github.com/skyhook-io/radar/internal/filter"
	"github.com/skyhook-io/radar/internal/search"
)

// handleSearch serves GET /api/search.
//
// Query params:
//
//	q       — search string (free tokens + modifiers: kind:, ns:, label:k=v, image:, cluster:)
//	limit   — max hits returned (default 50, capped at 500)
//	include — "summary" (default), "raw", or "none"
//
// The returned shape is a search.Result. Per-cluster, no cross-cluster
// concerns — radar-hub is responsible for fan-out and re-ranking.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}
	q := r.URL.Query().Get("q")
	parsed := search.Parse(q)

	provider := search.NewCacheProvider()
	if provider == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Resource cache not available")
		return
	}

	opts := search.Options{
		Limit:   parseLimit(r.URL.Query().Get("limit")),
		Include: parseInclude(r.URL.Query().Get("include")),
	}
	if expr := r.URL.Query().Get("filter"); expr != "" {
		f, err := filter.CachedObjectFilter(expr)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "filter: "+err.Error())
			return
		}
		opts.Filter = f
	}

	result, err := search.Search(r.Context(), provider, parsed, opts)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, result)
}

func parseLimit(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func parseInclude(v string) search.IncludeMode {
	switch v {
	case "raw":
		return search.IncludeRaw
	case "none":
		return search.IncludeNone
	default:
		return search.IncludeSummary
	}
}
