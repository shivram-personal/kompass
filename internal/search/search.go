// Package search provides cluster-wide free-text search over typed and
// dynamic-cached resources. It walks the in-memory radar cache, scores
// each object against the parsed query, and returns ranked hits with
// optional minified summaries or raw objects.
//
// Search is O(N) per kind: we scan each lister rather than maintaining
// inverted indexes. For radar's typical cluster sizes (≤50K objects)
// this stays well under a second per query and avoids any cache-update
// invalidation bookkeeping.
package search

import (
	"context"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aicontext "github.com/skyhook-io/radar/pkg/ai/context"
	"github.com/skyhook-io/radar/internal/k8s"
)

// Provider abstracts the cache so tests can inject a fake.
type Provider interface {
	ListTyped(kind string, namespaces []string) ([]runtime.Object, error)
	ListDynamic(ctx context.Context, gvr schema.GroupVersionResource, namespace string) ([]*unstructured.Unstructured, error)
	WatchedDynamic() []schema.GroupVersionResource
	KindForGVR(gvr schema.GroupVersionResource) string
}

// typedKinds is the set of typed kinds we walk for unfiltered queries.
// Order is intentional: we scan workloads first (they're what users
// usually ask about) so partial-result truncation favors them.
//
// Events are excluded — they're high-volume diagnostic data, not
// resources users want to find by name. A query with kind:Event still
// scans them because the kind filter overrides the default skip-set.
var typedKinds = []struct {
	Kind   string // singular Kind name for display ("Pod")
	Plural string // lowercase plural for fetch.go ("pods")
	Group  string
}{
	{"Pod", "pods", ""},
	{"Service", "services", ""},
	{"Deployment", "deployments", "apps"},
	{"DaemonSet", "daemonsets", "apps"},
	{"StatefulSet", "statefulsets", "apps"},
	{"ReplicaSet", "replicasets", "apps"},
	{"Job", "jobs", "batch"},
	{"CronJob", "cronjobs", "batch"},
	{"Ingress", "ingresses", "networking.k8s.io"},
	{"ConfigMap", "configmaps", ""},
	{"Secret", "secrets", ""},
	{"PersistentVolumeClaim", "persistentvolumeclaims", ""},
	{"PersistentVolume", "persistentvolumes", ""},
	{"StorageClass", "storageclasses", "storage.k8s.io"},
	{"HorizontalPodAutoscaler", "hpas", "autoscaling"},
	{"PodDisruptionBudget", "poddisruptionbudgets", "policy"},
	{"Node", "nodes", ""},
	{"Namespace", "namespaces", ""},
	{"Event", "events", ""},
}

// Options configures a Search call.
type Options struct {
	Limit   int
	Include IncludeMode
	// Filter is an optional compiled CEL predicate. When set, each
	// candidate that passed the modifier+token match is also evaluated
	// against the filter; non-truthy results (including eval errors)
	// drop the candidate. Compile happens in the handler; this layer
	// just runs the program.
	Filter *CELFilter
}

// Search runs the parsed query against the provider and returns ranked hits.
func Search(ctx context.Context, p Provider, q Query, opts Options) (Result, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	if opts.Limit > MaxLimit {
		opts.Limit = MaxLimit
	}

	var res Result
	var hits []Hit

	// Typed kinds.
	for _, tk := range typedKinds {
		if !shouldScanTyped(tk.Kind, q) {
			continue
		}
		objs, err := p.ListTyped(tk.Plural, nil)
		if err != nil {
			// Forbidden / unknown — silently skip this kind, partial
			// results are better than blanking the whole search.
			continue
		}
		res.Searched += len(objs)
		for _, obj := range objs {
			c, ok := fromObject(obj, tk.Kind)
			if !ok {
				continue
			}
			c.Group = tk.Group
			score, matched, ok := match(q, c)
			if !ok {
				continue
			}
			if opts.Filter != nil {
				act, err := objectActivation(obj, tk.Kind)
				if err != nil {
					continue
				}
				ok, _ := opts.Filter.Match(act)
				if !ok {
					continue
				}
			}
			hits = append(hits, buildHit(score, matched, c, opts.Include, obj, nil))
		}
	}

	// Dynamic kinds (CRDs).
	for _, gvr := range p.WatchedDynamic() {
		kind := p.KindForGVR(gvr)
		if kind == "" {
			continue
		}
		if !shouldScanCRD(kind, q) {
			continue
		}
		items, err := p.ListDynamic(ctx, gvr, "")
		if err != nil {
			continue
		}
		res.Searched += len(items)
		for _, u := range items {
			c := fromUnstructured(u, kind, gvr.Group)
			score, matched, ok := match(q, c)
			if !ok {
				continue
			}
			if opts.Filter != nil {
				act := unstructuredActivation(u, kind)
				if act == nil {
					continue
				}
				ok, _ := opts.Filter.Match(act)
				if !ok {
					continue
				}
			}
			hits = append(hits, buildHit(score, matched, c, opts.Include, nil, u))
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Kind != hits[j].Kind {
			return hits[i].Kind < hits[j].Kind
		}
		if hits[i].Namespace != hits[j].Namespace {
			return hits[i].Namespace < hits[j].Namespace
		}
		return hits[i].Name < hits[j].Name
	})
	if len(hits) > opts.Limit {
		hits = hits[:opts.Limit]
	}
	res.Hits = hits
	res.Total = len(hits)
	return res, nil
}

func shouldScanTyped(kind string, q Query) bool {
	if len(q.KindFilter) > 0 {
		return kindMatches(kind, q.KindFilter)
	}
	// Default skip-list: events are high-volume diagnostic data, not
	// resources users find by name. Honored only when no explicit kind
	// filter is set.
	return strings.ToLower(kind) != "event"
}

func shouldScanCRD(kind string, q Query) bool {
	if len(q.KindFilter) > 0 {
		return kindMatches(kind, q.KindFilter)
	}
	return true
}

// buildHit assembles the response shape for a matched candidate. Exactly
// one of obj/u will be non-nil. minify-on-demand keeps the cost of
// IncludeNone (identity-only) flat.
func buildHit(score int, matched []MatchedField, c candidate, mode IncludeMode, obj runtime.Object, u *unstructured.Unstructured) Hit {
	h := Hit{
		Score:     score,
		Kind:      c.Kind,
		Group:     c.Group,
		Namespace: c.Namespace,
		Name:      c.Name,
		Matched:   matched,
	}
	switch mode {
	case IncludeSummary:
		if obj != nil {
			k8s.SetTypeMeta(obj)
			if s, err := aicontext.Minify(obj, aicontext.LevelSummary); err == nil {
				h.Summary = s
			}
		} else if u != nil {
			h.Summary = aicontext.MinifyUnstructured(u, aicontext.LevelSummary)
		}
	case IncludeRaw:
		if obj != nil {
			k8s.SetTypeMeta(obj)
			if s, err := aicontext.Minify(obj, aicontext.LevelDetail); err == nil {
				h.Raw = s
			}
		} else if u != nil {
			h.Raw = aicontext.MinifyUnstructured(u, aicontext.LevelDetail)
		}
	}
	return h
}
