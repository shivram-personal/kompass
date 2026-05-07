package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/skyhook-io/radar/internal/filter"
	"github.com/skyhook-io/radar/internal/issues"
)

// handleIssues serves GET /api/issues — the unified cluster-health
// endpoint. Composes problems + audit findings (opt-in) + warning
// events + generic CRD condition fallback into one normalized list.
//
// Query params:
//
//	namespace= / namespaces=  one or comma-separated
//	severity=  critical,warning,info  (default: all)
//	source=    problem,audit,event,condition  (default: all except audit)
//	kind=      Pod,Deployment,...  (default: all)
//	since=     duration like 15m, 1h (default: no time restriction; only affects events)
//	limit=     default 200, max 1000
//	include_audit=true  shorthand to opt audit findings in
func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}
	provider := issues.NewCacheProvider()
	if provider == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Resource cache not available")
		return
	}

	q := r.URL.Query()

	filters := issues.Filters{
		Namespaces:   parseNamespaces(q),
		Severities:   parseSeverities(q.Get("severity")),
		Sources:      parseSources(q.Get("source")),
		Kinds:        splitCSV(q.Get("kind")),
		Since:        parseDuration(q.Get("since")),
		Limit:        parseLimit(q.Get("limit")),
		IncludeAudit: q.Get("include_audit") == "true" || hasSourceAudit(q.Get("source")),
	}
	if expr := q.Get("filter"); expr != "" {
		f, err := filter.CachedIssueFilter(expr)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "filter: "+err.Error())
			return
		}
		filters.Filter = f
	}

	out := issues.Compose(provider, filters)
	s.writeJSON(w, map[string]any{
		"issues": out,
		"total":  len(out),
	})
}

func parseSeverities(v string) []issues.Severity {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]issues.Severity, 0, len(parts))
	for _, p := range parts {
		s := strings.ToLower(strings.TrimSpace(p))
		switch s {
		case "critical":
			out = append(out, issues.SeverityCritical)
		case "warning":
			out = append(out, issues.SeverityWarning)
		case "info":
			out = append(out, issues.SeverityInfo)
		}
	}
	return out
}

func parseSources(v string) []issues.Source {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]issues.Source, 0, len(parts))
	for _, p := range parts {
		s := strings.ToLower(strings.TrimSpace(p))
		switch s {
		case "problem":
			out = append(out, issues.SourceProblem)
		case "audit":
			out = append(out, issues.SourceAudit)
		case "event":
			out = append(out, issues.SourceEvent)
		case "condition":
			out = append(out, issues.SourceCondition)
		}
	}
	return out
}

// hasSourceAudit lets `?source=audit` implicitly opt audit in without
// the caller also passing `?include_audit=true` — the param-source
// list is more discoverable.
func hasSourceAudit(v string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), "audit") {
			return true
		}
	}
	return false
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseDuration(v string) time.Duration {
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0
	}
	return d
}
