package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// resourceLister is the slice of the resource cache the family resolver
// needs — an interface so tests can stub the Argo Application feed.
type resourceLister interface {
	ListDynamicWithGroup(ctx context.Context, kind, namespace, group string) ([]*unstructured.Unstructured, error)
}

// Env-family classification — grouping per-env instances of one logical app
// (billing@dev, billing-staging, autopush-koala-backend-…) into a family.
//
// CONTRACT: family is classification, never identity. Rows keep their overlay
// key untouched; a family-tagged row is byte-identical minus the Family block.
// This sits ABOVE the pkg/subject overlay (which decides workload→app), so it
// is not a competing resolver — it groups the resolver's outputs by env, with
// provenance + confidence on the wire so the UI can render the grouping
// honestly and degrade to flat instances.
//
// Evidence tiers:
//   F1 (high)   — a declared source path with an env segment: the in-cluster
//                 Argo Application's spec.source(.s).path, e.g.
//                 billing/deploy/overlays/staging → stem billing/deploy.
//                 (The ApplicationSet NAME is deliberately not a key — env-
//                 scoped sets like platform-apps-staging would over-merge.)
//   F2 (medium) — name-stem env normalization (suffix billing-staging, prefix
//                 autopush-koala-…, or an env namespace) corroborated by a
//                 shared image repository across ALL members. The repo check
//                 is what makes this safe: same repo = same artifact.
//
// Guards: ≥2 instances, ≥2 distinct envs, strict repo intersection for F2,
// and conflicting F1 path stems never merge through a shared name stem.

// appFamily is the per-row family classification on the wire.
type appFamily struct {
	// Key is the family's display identity (the shared name stem). Derived
	// classification — never an address; instance keys remain the only URLs.
	Key string `json:"key"`
	// Env is this instance's canonical env token (dev|staging|prod|autopush|…).
	Env string `json:"env"`
	// Confidence: high (declared source path) | medium (name stem + shared repo).
	Confidence string `json:"confidence"`
	// Evidence is the human-readable why, rendered in the family chip tooltip.
	Evidence string `json:"evidence"`
}

// Ranked core — the only envs that carry promotion direction (lag arrows).
// Universal convention; safe without user config. Synonyms canonicalize.
var envCanonical = map[string]string{
	"dev": "dev", "development": "dev",
	"staging": "staging", "stage": "staging", "stg": "staging",
	"prod": "prod", "production": "prod", "prd": "prod",
}

// Recognized-but-unranked env tokens: they group families and label ladder
// cells but never participate in promotion-lag (wrong arrows are trust-fatal;
// rank these only via future explicit config).
var envUnranked = map[string]bool{
	"autopush": true, "qa": true, "uat": true, "preprod": true,
	"preview": true, "canary": true,
}

// Namespace-only env tokens: too generic to trust as NAME affixes (an app
// legitimately named "load-test" is not an env instance) but fine as
// namespace names, where env-per-namespace is the established convention.
var envNamespaceOnly = map[string]bool{
	"test": true, "demo": true, "sandbox": true, "perf": true, "integration": true,
}

func canonicalEnv(tok string) (string, bool) {
	t := strings.ToLower(tok)
	if c, ok := envCanonical[t]; ok {
		return c, true
	}
	if envUnranked[t] {
		return t, true
	}
	return "", false
}

func canonicalNamespaceEnv(tok string) (string, bool) {
	if c, ok := canonicalEnv(tok); ok {
		return c, true
	}
	t := strings.ToLower(tok)
	if envNamespaceOnly[t] {
		return t, true
	}
	return "", false
}

// splitNameEnv strips a recognized env affix from an app name:
// "billing-staging" → ("billing", "staging"), "autopush-koala-backend-us-east1"
// → ("koala-backend-us-east1", "autopush"). Suffix wins over prefix when both
// match (suffix is the more common convention).
func splitNameEnv(name string) (stem, env string) {
	if i := strings.LastIndex(name, "-"); i > 0 {
		if c, ok := canonicalEnv(name[i+1:]); ok {
			return name[:i], c
		}
	}
	if i := strings.Index(name, "-"); i > 0 {
		if c, ok := canonicalEnv(name[:i]); ok {
			return name[i+1:], c
		}
	}
	return name, ""
}

// namespaceEnv reads an env from a residence namespace: exact token ("dev"),
// or a delimited segment ("payments-staging", "staging-us-east1").
func namespaceEnv(ns string) string {
	if c, ok := canonicalNamespaceEnv(ns); ok {
		return c
	}
	for _, seg := range strings.FieldsFunc(ns, func(r rune) bool { return r == '-' || r == '_' }) {
		if c, ok := canonicalNamespaceEnv(seg); ok {
			return c
		}
	}
	return ""
}

// pathStemEnv extracts (stem, env) from a declared GitOps source path:
// "billing/deploy/overlays/staging" → ("billing/deploy", "staging"). The stem
// drops the env segment and the conventional "overlays" directory so sibling
// envs of one base share a stem. No env segment → no F1 evidence.
func pathStemEnv(path string) (stem, env string) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	kept := make([]string, 0, len(segs))
	for _, s := range segs {
		if env == "" {
			if c, ok := canonicalNamespaceEnv(s); ok {
				env = c
				continue
			}
		}
		if strings.EqualFold(s, "overlays") {
			continue
		}
		kept = append(kept, s)
	}
	if env == "" {
		return "", ""
	}
	return strings.Join(kept, "/"), env
}

// familyCandidate is one row's env-instance reading, pre-validation.
type familyCandidate struct {
	row      *appRow
	stem     string // shared name stem (family display key)
	env      string
	pathStem string // F1 declared stem ("" when none)
	repos    map[string]bool
}

// resolveAppFamilies tags env-family classifications onto rows, in place.
// argoSourcePaths maps an in-cluster Argo Application name → its source path
// (multi-source apps may map to the first path with an env segment).
func resolveAppFamilies(rows []appRow, argoSourcePaths map[string]string) {
	byStem := map[string][]*familyCandidate{}

	for i := range rows {
		r := &rows[i]
		name := r.Name
		stem, env := splitNameEnv(name)
		if env == "" {
			// No name affix — fall back to the residence namespace.
			ns := r.Namespace
			if ns == "" && len(r.Namespaces) == 1 {
				ns = r.Namespaces[0]
			}
			if ns != "" {
				env = namespaceEnv(ns)
			}
			stem = name
		}
		if env == "" {
			continue // no env signal → no family membership
		}

		c := &familyCandidate{row: r, stem: stem, env: env, repos: map[string]bool{}}
		for _, w := range r.Workloads {
			if repo := imageRepo(w.Image); repo != "" {
				c.repos[repo] = true
			}
		}
		// F1: a declared Argo source path for this row's Application.
		if r.Tier == 3 || r.Tier == 4 {
			if p, ok := argoSourcePaths[appNameFromKey(r.Key)]; ok {
				if ps, pe := pathStemEnv(p); ps != "" {
					c.pathStem = ps
					// The declared env wins over the name/namespace reading.
					c.env = pe
				}
			}
		}
		byStem[stem] = append(byStem[stem], c)
	}

	for stem, cands := range byStem {
		// Conflicting declared stems never merge through a shared name stem:
		// keep only the candidates of the dominant path stem plus pathless
		// ones, and only when no second declared stem exists.
		declared := map[string]bool{}
		for _, c := range cands {
			if c.pathStem != "" {
				declared[c.pathStem] = true
			}
		}
		if len(declared) > 1 {
			continue // ambiguous declarations — refuse to group by name stem
		}

		if len(cands) < 2 {
			continue
		}
		envs := map[string]bool{}
		for _, c := range cands {
			envs[c.env] = true
		}
		if len(envs) < 2 {
			continue // one env, N instances — replicas/shards, not a family
		}

		// Strict repo corroboration: every member shares ≥1 image repository.
		shared := sharedRepo(cands)
		anyDeclared := len(declared) == 1
		if shared == "" && !anyDeclared {
			continue // uncorroborated name coincidence — refuse
		}

		for _, c := range cands {
			conf, why := "medium", ""
			if c.pathStem != "" {
				conf = "high"
				why = "Argo CD source path " + c.pathStem + " (env overlay " + c.env + ")"
			} else if shared != "" {
				why = fmt.Sprintf("name stem %q + shared image repo %s", stem, shared)
			} else {
				// Declared siblings exist but this member has no shared repo —
				// keep it out rather than ride the family on name alone.
				continue
			}
			c.row.Family = &appFamily{Key: stem, Env: c.env, Confidence: conf, Evidence: why}
		}
	}
}

// sharedRepo returns one image repository present in EVERY candidate's repo
// set ("" when none). Deterministic pick (lexicographic) for stable evidence.
func sharedRepo(cands []*familyCandidate) string {
	if len(cands) == 0 {
		return ""
	}
	common := []string{}
	for repo := range cands[0].repos {
		ok := true
		for _, c := range cands[1:] {
			if !c.repos[repo] {
				ok = false
				break
			}
		}
		if ok {
			common = append(common, repo)
		}
	}
	if len(common) == 0 {
		return ""
	}
	sort.Strings(common)
	return common[0]
}

// argoSourcePaths maps each in-cluster Argo Application name to a source path
// carrying an env segment — the F1 evidence feed. Multi-source apps pick the
// first env-bearing path. Missing CRD / no Argo → empty map (F2 still works).
func argoSourcePaths(ctx context.Context, cache resourceLister) map[string]string {
	out := map[string]string{}
	items, err := cache.ListDynamicWithGroup(ctx, "Application", "", "argoproj.io")
	if err != nil {
		return out
	}
	for _, item := range items {
		spec, _ := item.Object["spec"].(map[string]any)
		if spec == nil {
			continue
		}
		paths := []string{}
		if src, _ := spec["source"].(map[string]any); src != nil {
			if p, _ := src["path"].(string); p != "" {
				paths = append(paths, p)
			}
		}
		if srcs, _ := spec["sources"].([]any); srcs != nil {
			for _, s := range srcs {
				if m, _ := s.(map[string]any); m != nil {
					if p, _ := m["path"].(string); p != "" {
						paths = append(paths, p)
					}
				}
			}
		}
		for _, p := range paths {
			if stem, _ := pathStemEnv(p); stem != "" {
				out[item.GetName()] = p
				break
			}
		}
	}
	return out
}
