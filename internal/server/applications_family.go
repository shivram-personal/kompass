package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
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

// The ONLY hardcoded env vocabulary: the universal trio, kept solely because
// promotion DIRECTION (dev < staging < prod) cannot be derived from a
// snapshot. Everything else — autopush, qa, loadtest, e2e, whatever a team
// calls its environments — is DISCOVERED per cluster: a token qualifies as an
// env when the cluster's own structure says so (see qualifyEnvTokens).
// Discovered tokens group and label but never rank (wrong lag arrows are
// trust-fatal; ranking beyond the trio needs explicit config).
var envCanonical = map[string]string{
	"dev": "dev", "development": "dev",
	"staging": "staging", "stage": "staging", "stg": "staging",
	"prod": "prod", "production": "prod", "prd": "prod",
}

func trioEnv(tok string) (string, bool) {
	c, ok := envCanonical[strings.ToLower(tok)]
	return c, ok
}

// Explicit env label keys, strongest evidence first. There is no official
// k8s environment label — app.kubernetes.io/environment is the de-facto
// extension of the recommended-labels family, plain environment/env are the
// common shorthands, and tags.datadoghq.com/env is Datadog's Unified Service
// Tagging key (already deployed in many fleets). Set any of these on a
// workload or its namespace and Radar takes your word over every heuristic.
var envLabelKeys = []string{
	"app.kubernetes.io/environment",
	"environment",
	"env",
	"tags.datadoghq.com/env",
}

func envLabelOf(lbls map[string]string) string {
	for _, k := range envLabelKeys {
		if v := strings.TrimSpace(lbls[k]); v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

// namespaceLister is the slice of the resource cache the resolver needs for
// namespace env labels.
type namespaceLister interface {
	Namespaces() listerscorev1.NamespaceLister
}

// namespaceEnvLabels maps namespace → explicit env label (envLabelKeys).
func namespaceEnvLabels(cache namespaceLister) map[string]string {
	out := map[string]string{}
	nss, err := cache.Namespaces().List(labels.Everything())
	if err != nil {
		return out
	}
	for _, ns := range nss {
		if v := envLabelOf(ns.Labels); v != "" {
			out[ns.Name] = v
		}
	}
	return out
}

// ── Env-token discovery ──────────────────────────────────────────────────────
//
// No hardcoded env vocabulary beyond the trio. A token qualifies as an env
// when the CLUSTER's structure says so:
//   - it's the trio (dev/staging/prod + synonyms) — always an env; or
//   - a declared GitOps path names it as the overlay segment; or
//   - it recurs as the differing affix/namespace across ≥2 viable stem-groups
//     (a stem-group = same stem, ≥2 rows, distinct tokens, shared image
//     repo); or
//   - it appears in 1 viable stem-group AND is itself a namespace in the
//     cluster (env-per-namespace corroboration).
// "billing-staging"/"billing" qualifies staging; OUR "autopush" qualifies on
// recurrence + namespace existence; a customer's "loadtest"/"e2e" qualifies on
// THEIR cluster the same way — zero code, zero config. One-off oddities
// ("myapp-v2", a lone "load-test") never qualify, replacing the old
// conservative token lists with structure.

// reading is one way a row could split into (stem, env token).
type reading struct {
	stem  string
	token string // raw lowercased token (canonicalized later)
	prio  int    // lower wins: 0 suffix, 1 prefix, 2 namespace, 3 ns segment
}

func readingsOf(name, ns string) []reading {
	var out []reading
	if i := strings.LastIndex(name, "-"); i > 0 && i < len(name)-1 {
		out = append(out, reading{stem: name[:i], token: strings.ToLower(name[i+1:]), prio: 0})
	}
	if i := strings.Index(name, "-"); i > 0 && i < len(name)-1 {
		out = append(out, reading{stem: name[i+1:], token: strings.ToLower(name[:i]), prio: 1})
	}
	if ns != "" {
		// A full namespace name is an env candidate only when it's a single
		// token ("dev", "autopush", "loadtest") — "skyhook-clients-frps" is a
		// name, not an environment; only its segments may qualify.
		if !strings.ContainsAny(ns, "-_") {
			out = append(out, reading{stem: name, token: strings.ToLower(ns), prio: 2})
		}
		for _, seg := range strings.FieldsFunc(ns, func(r rune) bool { return r == '-' || r == '_' }) {
			if !strings.EqualFold(seg, ns) {
				out = append(out, reading{stem: name, token: strings.ToLower(seg), prio: 3})
			}
		}
	}
	return out
}

// canonicalEnvToken maps a qualified token to its display form: trio synonyms
// canonicalize; discovered tokens pass through as-is.
func canonicalEnvToken(tok string) string {
	if c, ok := trioEnv(tok); ok {
		return c
	}
	return tok
}

// pathStemEnv extracts (stem, env) from a declared GitOps source path using
// STRUCTURE, not vocabulary: the segment after an "overlays"/"envs" directory
// is the env (kustomize convention); else a trio segment. The stem drops the
// env segment and the convention directory.
func pathStemEnv(path string) (stem, env string) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	kept := make([]string, 0, len(segs))
	prevWasOverlayDir := false
	for _, s := range segs {
		lower := strings.ToLower(s)
		if env == "" && prevWasOverlayDir {
			env = lower
			prevWasOverlayDir = false
			continue
		}
		if lower == "overlays" || lower == "envs" || lower == "environments" {
			prevWasOverlayDir = true
			continue
		}
		prevWasOverlayDir = false
		if env == "" {
			if c, ok := trioEnv(s); ok {
				env = c
				continue
			}
		}
		kept = append(kept, s)
	}
	if env == "" {
		return "", ""
	}
	return strings.Join(kept, "/"), env
}

// familyCandidate is one row's chosen env-instance reading, post-qualification.
type familyCandidate struct {
	row      *appRow
	stem     string
	env      string // canonical display token
	pathStem string // F1 declared stem ("" when none)
	labelEnv string // explicit env label ("" when none)
	repos    map[string]bool
	// adopted: this member's env token never qualified on its own — it joins
	// only because the stem-group's CORE proved itself (≥2 qualified envs),
	// and it never counts toward that proof. Single-token namespaces only;
	// adopting name affixes would re-admit variant suffixes (-cos/-ubuntu).
	adopted bool
}

// resolveAppFamilies tags env-family classifications onto rows, in place.
// Two phases: discover which tokens are envs from the cluster's structure,
// then form families using only qualified tokens. argoSourcePaths maps an
// in-cluster Argo Application name → its source path.
func resolveAppFamilies(rows []appRow, argoSourcePaths map[string]string, nsEnvLabels map[string]string) {
	type rowInfo struct {
		row      *appRow
		readings []reading
		repos    map[string]bool
		pathStem string
		pathEnv  string
		labelEnv string // explicit env label (workload, else namespace)
	}
	infos := make([]rowInfo, 0, len(rows))
	clusterNamespaces := map[string]bool{}
	for i := range rows {
		r := &rows[i]
		ns := r.Namespace
		if ns == "" && len(r.Namespaces) == 1 {
			ns = r.Namespaces[0]
		}
		for _, n := range r.Namespaces {
			clusterNamespaces[strings.ToLower(n)] = true
		}
		if ns != "" {
			clusterNamespaces[strings.ToLower(ns)] = true
		}
		info := rowInfo{row: r, readings: readingsOf(r.Name, ns), repos: map[string]bool{}}
		wlEnv, wlEnvAgree := "", true
		for _, w := range r.Workloads {
			if repo := imageRepo(w.Image); repo != "" {
				info.repos[repo] = true
			}
			if w.envLabel != "" {
				if wlEnv == "" {
					wlEnv = w.envLabel
				} else if wlEnv != w.envLabel {
					wlEnvAgree = false // disagreeing labels — refuse the tier
				}
			}
		}
		if wlEnv != "" && wlEnvAgree {
			info.labelEnv = wlEnv
		} else if v := nsEnvLabels[ns]; v != "" {
			info.labelEnv = v
		}
		if r.Tier == 3 || r.Tier == 4 {
			if p, ok := argoSourcePaths[appNameFromKey(r.Key)]; ok {
				if ps, pe := pathStemEnv(p); ps != "" {
					info.pathStem, info.pathEnv = ps, pe
				}
			}
		}
		infos = append(infos, info)
	}

	// Phase 1 — qualify tokens. Viable stem-group: ≥2 rows sharing a stem
	// under SOME reading, with ≥2 distinct tokens and a shared image repo.
	type groupStat struct {
		members map[*appRow]reading
		tokens  map[string]bool
	}
	rowNs := func(r *appRow) string {
		if r.Namespace != "" {
			return r.Namespace
		}
		if len(r.Namespaces) == 1 {
			return r.Namespaces[0]
		}
		return ""
	}
	stems := map[string]*groupStat{}
	for i := range infos {
		seen := map[string]bool{}
		for _, rd := range infos[i].readings {
			if rd.stem == "" || rd.token == "" || seen[rd.stem+"\x00"+rd.token] {
				continue
			}
			seen[rd.stem+"\x00"+rd.token] = true
			g := stems[rd.stem]
			if g == nil {
				g = &groupStat{members: map[*appRow]reading{}, tokens: map[string]bool{}}
				stems[rd.stem] = g
			}
			if prev, ok := g.members[infos[i].row]; !ok || rd.prio < prev.prio {
				g.members[infos[i].row] = rd
			}
			g.tokens[rd.token] = true
		}
	}
	tokenViableGroups := map[string]int{}
	tokenNsSpread := map[string]bool{}
	affixTokens := map[string]bool{}
	for i := range infos {
		for _, rd := range infos[i].readings {
			if rd.prio <= 1 {
				affixTokens[rd.token] = true
			}
		}
	}
	for stem, g := range stems {
		if len(g.members) < 2 || len(g.tokens) < 2 {
			continue
		}
		// shared-repo check across the group's members
		var cands []*familyCandidate
		for row, rd := range g.members {
			var repos map[string]bool
			for j := range infos {
				if infos[j].row == row {
					repos = infos[j].repos
					break
				}
			}
			cands = append(cands, &familyCandidate{row: row, stem: stem, env: rd.token, repos: repos})
		}
		if sharedRepo(cands) == "" {
			continue
		}
		nss := map[string]bool{}
		for row := range g.members {
			nss[rowNs(row)] = true
		}
		for tok := range g.tokens {
			tokenViableGroups[tok]++
			if len(nss) >= 2 {
				tokenNsSpread[tok] = true
			}
		}
	}
	qualified := map[string]bool{}
	for tok, n := range tokenViableGroups {
		_, isTrio := trioEnv(tok)
		switch {
		case isTrio:
			qualified[tok] = true
		case n >= 3 && tokenNsSpread[tok]:
			// Real env tokens recur across many apps AND their instances
			// isolate by namespace. Parallel variant dimensions (-cos/-ubuntu
			// gpu-plugin flavors all in kube-system) fail one or both.
			qualified[tok] = true
		case affixTokens[tok] && clusterNamespaces[tok]:
			// A name affix that is ALSO a namespace ("api-loadtest" + a
			// loadtest namespace) — the cluster corroborates the token.
			qualified[tok] = true
		}
	}
	// Declared overlay segments and explicit env labels always qualify.
	for i := range infos {
		if infos[i].pathEnv != "" {
			qualified[strings.ToLower(infos[i].pathEnv)] = true
		}
		if infos[i].labelEnv != "" {
			qualified[infos[i].labelEnv] = true
		}
	}

	// Phase 2 — each row's best QUALIFIED reading, then family formation with
	// the original validation (≥2 instances, ≥2 distinct envs, shared repo,
	// declared-stem conflicts refuse).
	byStem := map[string][]*familyCandidate{}
	for i := range infos {
		info := &infos[i]
		var chosen *reading
		better := func(a, b *reading) bool {
			_, aTrio := trioEnv(a.token)
			_, bTrio := trioEnv(b.token)
			if aTrio != bTrio {
				return aTrio // the universal ladder outranks discovered tokens
			}
			return a.prio < b.prio
		}
		for j := range info.readings {
			rd := info.readings[j]
			if !qualified[rd.token] {
				continue
			}
			if chosen == nil || better(&rd, chosen) {
				cp := rd
				chosen = &cp
			}
		}
		if chosen == nil && info.pathEnv == "" && info.labelEnv == "" {
			// Adoption candidate: a single-token namespace reading that didn't
			// qualify. Joins a family only if the stem's core proves itself.
			for _, rd := range info.readings {
				if rd.prio == 2 {
					c := &familyCandidate{row: info.row, repos: info.repos, stem: rd.stem, env: canonicalEnvToken(rd.token), adopted: true}
					byStem[c.stem] = append(byStem[c.stem], c)
					break
				}
			}
			continue
		}
		c := &familyCandidate{row: info.row, repos: info.repos, pathStem: info.pathStem, labelEnv: info.labelEnv}
		if chosen != nil {
			c.stem = chosen.stem
			c.env = canonicalEnvToken(chosen.token)
		} else {
			c.stem = info.row.Name
			c.env = canonicalEnvToken(info.pathEnv)
		}
		// Env precedence: explicit label > declared path > structural reading.
		if info.pathEnv != "" {
			c.env = canonicalEnvToken(info.pathEnv)
		}
		if info.labelEnv != "" {
			c.env = canonicalEnvToken(info.labelEnv)
		}
		byStem[c.stem] = append(byStem[c.stem], c)
	}
	for stem, cands := range byStem {
		declared := map[string]bool{}
		for _, c := range cands {
			if c.pathStem != "" {
				declared[c.pathStem] = true
			}
		}
		if len(declared) > 1 {
			continue // ambiguous declarations — refuse to group by name stem
		}
		envs := map[string]bool{}
		core := 0
		for _, c := range cands {
			if !c.adopted {
				envs[c.env] = true
				core++
			}
		}
		if core < 2 || len(envs) < 2 {
			continue // the CORE must prove the family; adoption never bootstraps
		}
		shared := sharedRepo(cands)
		anyDeclared := len(declared) == 1
		if shared == "" && !anyDeclared {
			continue // uncorroborated name coincidence — refuse
		}
		for _, c := range cands {
			conf, why := "medium", ""
			switch {
			case c.labelEnv != "":
				conf = "high"
				why = fmt.Sprintf("environment label %q", c.labelEnv)
			case c.pathStem != "":
				conf = "high"
				why = "Argo CD source path " + c.pathStem + " (env overlay " + c.env + ")"
			case shared != "":
				why = fmt.Sprintf("name stem %q + shared image repo %s", stem, shared)
			default:
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
