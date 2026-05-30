package issues

import "strings"

func applyFilters(in []Issue, f Filters) []Issue {
	if len(f.Severities) == 0 && len(f.Kinds) == 0 {
		return in
	}
	wantSev := map[Severity]bool{}
	for _, s := range f.Severities {
		wantSev[s] = true
	}
	wantKind := map[string]bool{}
	for _, k := range f.Kinds {
		wantKind[strings.ToLower(k)] = true
	}
	out := in[:0]
	for _, i := range in {
		if len(wantSev) > 0 && !wantSev[i.Severity] {
			continue
		}
		if len(wantKind) > 0 && !wantKind[strings.ToLower(i.Kind)] {
			continue
		}
		out = append(out, i)
	}
	return out
}

func applyClusterScopedAccess(in []Issue, f Filters) []Issue {
	if f.CanReadClusterScoped == nil {
		return in
	}
	out := make([]Issue, 0, len(in))
	for _, i := range in {
		if i.Namespace != "" {
			out = append(out, i)
			continue
		}
		// Namespace-less issue: must be cluster-scoped (a namespaced
		// resource without a namespace would be invalid wire data). We
		// previously gated on k8s.ClassifyKindScope (a hardcoded list of
		// known cluster-scoped kinds) and silently dropped anything that
		// didn't match — which meant CRDs like Karpenter NodePool, whose
		// emitter already classified them as cluster-scoped via dynamic
		// API discovery, vanished from the issues list for authenticated
		// users. CanReadClusterScoped (SAR-backed) is authoritative on
		// access; we don't need a pre-classification gate at this layer.
		if f.CanReadClusterScoped(i.Kind, i.Group) {
			out = append(out, i)
		}
	}
	return out
}

// SeverityRank orders the normalized issue severity, higher = worse. Shared by
// the grouping comparators here and by the per-resource summary rollups in the
// server/MCP layers (which previously each carried an identical local copy).
func SeverityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	}
	return 0
}
