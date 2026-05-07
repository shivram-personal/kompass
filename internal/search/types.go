package search

import "github.com/skyhook-io/radar/internal/filter"

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// CELFilter is the optional structured predicate applied AFTER the
// modifier + free-token match. When set, only candidates that compile
// down to truthy under the CEL program are kept; eval errors count as
// a non-match (consistent with "missing field" semantics).
type CELFilter = filter.Filter

// Query is a parsed search request.
//
// Free Tokens are AND'd: a hit must match every token in at least one site.
// Modifiers (KindFilter, NSFilter, LabelFilter, ImageFilter) act as hard
// boolean filters — a candidate that doesn't satisfy them is excluded
// before scoring.
type Query struct {
	Tokens      []string  // free tokens; each must match somewhere; sum of best per-site scores = total
	KindFilter  []string  // kind:Foo modifiers, lowercased; matches Kind name (singular or plural)
	NSFilter    []string  // ns:foo modifiers
	LabelFilter []LabelEq // label:k=v modifiers; AND'd
	ImageFilter []string  // image:foo modifiers; substring match on container images
	Cluster     string    // cluster: modifier — radar ignores; hub uses for routing
	Raw         string    // original query string, for debugging / round-tripping
}

// LabelEq is a single key=value label filter parsed from "label:k=v".
type LabelEq struct {
	Key   string
	Value string
}

// Hit is a single ranked search result.
type Hit struct {
	Score     int            `json:"score"`
	Cluster   string         `json:"cluster,omitempty"`
	Kind      string         `json:"kind"`
	Group     string         `json:"group,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name"`
	Summary   any            `json:"summary,omitempty"`
	Raw       any            `json:"raw,omitempty"`
	Matched   []MatchedField `json:"matched,omitempty"`
}

// MatchedField records where a query token landed (debug + UI highlight).
type MatchedField struct {
	Token string `json:"token"`
	Site  string `json:"site"` // "name" | "namespace" | "label:k" | "annotation:k" | "image" | "kind"
	Score int    `json:"score"`
}

// Result is the full response shape for a search request.
type Result struct {
	Hits     []Hit `json:"hits"`
	Total    int   `json:"total"`    // number of hits returned (after limit)
	Searched int   `json:"searched"` // approx. number of objects scanned
}

// IncludeMode controls per-hit verbosity.
type IncludeMode int

const (
	IncludeSummary IncludeMode = iota
	IncludeRaw
	IncludeNone // identity only (cheapest)
)

