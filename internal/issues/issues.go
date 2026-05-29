package issues

import (
	"log"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/skyhook-io/radar/internal/k8s"
	bp "github.com/skyhook-io/radar/pkg/audit"
	"github.com/skyhook-io/radar/pkg/packages"
)

// Provider abstracts the data sources Compose needs. Implementations
// in production come from the in-process radar caches; tests can
// inject fakes without standing up an informer stack.
type Provider interface {
	DetectProblems(namespaces []string) []k8s.Problem
	DetectCAPIProblems(namespaces []string) []k8s.Problem
	// DetectMissingRefs returns dangling-reference problems (Pod→missing
	// PVC/CM/Secret/SA, HPA→missing target, Ingress→missing backend, etc.)
	// plus webhook-config refs. Surfaced under SourceMissingRef so agents
	// can filter the "direct config error" category separately from the
	// workload-state-based SourceProblem signals.
	DetectMissingRefs(namespaces []string) []k8s.Problem
	// DetectScheduling returns placement/admission/post-bind failures —
	// unschedulable Pods (with the offending node constraint resolved),
	// admission rejections (quota/LimitRange/PodSecurity/webhook, where no
	// Pod exists), and pods stuck post-bind (CNI/volume). Surfaced under
	// SourceScheduling so agents/UI can isolate "why won't this run".
	DetectScheduling(namespaces []string) []k8s.Problem
	// CRD-condition fallback inputs.
	WatchedDynamic() []schema.GroupVersionResource
	ListDynamic(gvr schema.GroupVersionResource, namespace string) ([]*unstructured.Unstructured, error)
	KindForGVR(gvr schema.GroupVersionResource) string
}

type dynamicScopeProvider interface {
	NamespacedForGVR(gvr schema.GroupVersionResource) (bool, bool)
}

// ComposeStats reports anything the caller would want to surface
// alongside the issue list — currently CEL-filter eval-error
// counters so the caller can distinguish "filter excluded
// everything" from "cluster has nothing matching."
type ComposeStats struct {
	FilterErrors      int
	FilterErrorSample string
	// TotalMatched is the count of issues that survived ALL filters
	// (severity, source, kind, namespace, CEL) but BEFORE the Limit
	// truncation. Surfaced so the hub aggregator + agents + UI can
	// distinguish "this cluster had 500 issues; we returned 200" from
	// "this cluster had 200." Equal to len(returned slice) when no
	// truncation occurred.
	TotalMatched int
}

// Compose runs the curated operational sources and merges their output.
// Backward-compatible signature for callers that don't care about stats.
func Compose(p Provider, f Filters) []Issue {
	out, _ := ComposeWithStats(p, f)
	return out
}

// ComposeWithStats does the same work as Compose but also returns
// counters the caller may want to forward — currently the per-row
// CEL filter eval-error count + first error sample. Sort order is
// severity desc, then last-seen desc, then kind/ns/name for stable
// tiebreaks.
func ComposeWithStats(p Provider, f Filters) ([]Issue, ComposeStats) {
	// Negative Limit is the "uncapped" sentinel: callers that need the
	// full matched set (per-resource issue indexes for /api/ai list +
	// search summaryContext) pass NoLimit so a 5000-issue cluster
	// doesn't silently drop counts for resources whose issues fall in
	// the tail beyond MaxLimit. Zero still maps to DefaultLimit so the
	// public /api/issues + MCP issues_list keep their tight caps.
	uncapped := f.Limit < 0
	if f.Limit == 0 {
		f.Limit = DefaultLimit
	}
	if !uncapped && f.Limit > MaxLimit {
		f.Limit = MaxLimit
	}

	out := make([]Issue, 0, 64)
	now := time.Now()

	// issues = "what's broken right now" — the curated operational
	// sources, always composed. Raw Warning events live in get_events /
	// the timeline; Kyverno / policy posture lives with audit/compliance;
	// static best-practice findings live in audit. None of those belong in
	// the live-failure stream, so they are deliberately NOT sources here.
	// `source` survives only as an output label on each row (+ CEL filter),
	// not as an input filter — detection provenance is not a triage axis.

	// ---- Source: problem (radar's hardcoded checks) -----------------
	for _, p := range p.DetectProblems(f.Namespaces) {
		out = append(out, fromProblem(p, now, SourceProblem))
	}
	for _, p := range p.DetectCAPIProblems(f.Namespaces) {
		out = append(out, fromProblem(p, now, SourceProblem))
	}

	// ---- Source: missing_ref (dangling-ref detection) --------------
	// Direct by-name reference targets that don't exist (Pod → missing
	// PVC / CM / Secret / SA, HPA → missing scaleTargetRef, Ingress →
	// missing backend Service, etc.).
	for _, p := range p.DetectMissingRefs(f.Namespaces) {
		out = append(out, fromProblem(p, now, SourceMissingRef))
	}

	// ---- Source: scheduling (placement + admission + post-bind) -----
	// Why a Pod can't reach Running, decomposed: unschedulable (with the
	// offending node label/taint named), admission-rejected (quota/
	// PodSecurity/webhook — no Pod object exists), or stuck post-bind
	// (CNI/volume).
	for _, p := range p.DetectScheduling(f.Namespaces) {
		out = append(out, fromProblem(p, now, SourceScheduling))
	}

	// ---- Source: condition (generic CRD .status.conditions fallback) ----
	out = append(out, detectGenericCRDIssues(p, f)...)

	// Apply remaining filters (severity, kind, namespace) post-compose
	// since each source has its own native filtering surface and
	// pushing filters down individually would multiply branching.
	out = applyFilters(out, f)
	out = applyClusterScopedAccess(out, f)
	out = dedupePodSchedulingOverProblem(out)
	out = dedupeWorkloadDegradedOverChild(out)

	// Optional CEL filter — evaluated last so it sees the normalized
	// row shape. Eval errors count as non-match (matches "missing
	// field" semantics; agent gets zero hits + a clean response,
	// rather than a 500). Runtime causes: missing-field traversal,
	// type mismatches on dyn-typed nested fields, cost-limit
	// overruns. Parse/type errors against the declared bindings
	// fail at compile and never reach here. ComposeStats surfaces
	// the count + first sample back to the handler so the agent can
	// distinguish "filter excluded everything" from "cluster has
	// nothing matching."
	var stats ComposeStats
	if f.Filter != nil {
		filtered := out[:0]
		var firstErr error
		errCount := 0
		for _, i := range out {
			ok, err := f.Filter.Match(issueToActivation(i))
			if err != nil {
				errCount++
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if ok {
				filtered = append(filtered, i)
			}
		}
		if errCount > 0 {
			log.Printf("[issues] CEL filter eval errors: %d/%d rows; first=%v", errCount, len(out), firstErr)
			stats.FilterErrors = errCount
			if firstErr != nil {
				stats.FilterErrorSample = firstErr.Error()
			}
		}
		out = filtered
	}

	// Grouped folds the flat evidence rows into the public issue model
	// before the cap, so the limit counts issue groups rather than replica
	// fan-out (a 50-pod crashloop is one issue). Flat callers keep the
	// per-object rows. GroupIssues sorts deterministically; the flat path
	// uses the same comparator so both orders agree.
	if f.Grouped {
		out = GroupIssues(out)
	} else {
		sort.SliceStable(out, func(i, j int) bool { return lessIssue(out[i], out[j]) })
	}
	stats.TotalMatched = len(out)
	if !uncapped && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, stats
}

// detectGenericCRDIssues walks every watched dynamic CRD and emits a
// warning Issue for each object that has a False Ready/Available/etc.
// condition. Skips kinds owned by curated checkers (Cluster API today)
// to avoid double-reporting.
//
// When f.Kinds is non-empty (e.g. summaryContext building a per-resource
// issue index for a list_resources call on a single kind), GVRs whose
// kind isn't in the filter are skipped BEFORE the ListDynamic call —
// without this gate, a pods-only request still scanned every watched
// CRD up front and applyFilters discarded the rows afterward. Kind
// comparison mirrors applyFilters: lowercase for case-insensitive
// match against the user's filter (which itself is canonicalized to
// the singular form upstream).
func detectGenericCRDIssues(p Provider, f Filters) []Issue {
	gvrs := p.WatchedDynamic()
	if len(gvrs) == 0 {
		return nil
	}
	wantKind := map[string]bool{}
	for _, k := range f.Kinds {
		wantKind[strings.ToLower(k)] = true
	}
	var out []Issue
	for _, gvr := range gvrs {
		if isCuratedCRDGroup(gvr.Group) {
			continue
		}
		kind := p.KindForGVR(gvr)
		if kind == "" {
			continue
		}
		// applyFilters runs after Compose returns — but on hot paths that
		// pin a single kind (summaryContext per-row index), routing the
		// kind filter through here skips the per-GVR ListDynamic call
		// entirely. Match in lowercase (same as applyFilters) so
		// "Pod"/"pod" and CRD-typed "MyResource"/"myresource" both
		// compare equal.
		if len(wantKind) > 0 && !wantKind[strings.ToLower(kind)] {
			continue
		}
		clusterScoped, _, _ := classifyDynamicScope(p, gvr, kind)
		if clusterScoped && f.CanReadClusterScoped != nil && !f.CanReadClusterScoped(kind, gvr.Group) {
			continue
		}
		// Per-namespace iteration when scope is set; cluster-wide list
		// otherwise. List with empty namespace returns all namespaces.
		queryNs := []string{""}
		if !clusterScoped && len(f.Namespaces) > 0 {
			queryNs = f.Namespaces
		}
		for _, ns := range queryNs {
			items, err := p.ListDynamic(gvr, ns)
			if err != nil {
				continue
			}
			for _, u := range items {
				condType, reason, msg, since, ok := FindFalseCondition(u)
				if !ok {
					continue
				}
				// Noise-floor suppression (GA-blocker #5): a False
				// Ready/Available on an object that is suspended, still
				// reconciling, or whose controller hasn't yet observed the
				// current spec is NOT a failure — it's in-flight. Emitting a
				// warning for it is the canonical alert-fatigue trap, since
				// auto-refresh keeps it permanently lit. Skip those; keep
				// genuinely-failed objects.
				if isTransientCRDCondition(u, reason) {
					continue
				}
				lastSeen := time.Now().Add(-since)
				iss := Issue{
					Severity:  SeverityWarning,
					Source:    SourceCondition,
					Kind:      kind,
					Group:     gvr.Group,
					Namespace: u.GetNamespace(),
					Name:      u.GetName(),
					Reason:    condTypeReason(condType, reason),
					Message:   msg,
					FirstSeen: lastSeen,
					LastSeen:  lastSeen,
					Count:     1,
				}
				classifyIssue(&iss)
				enrichIdentity(&iss)
				out = append(out, iss)
			}
		}
	}
	return out
}

// isTransientCRDCondition reports whether a False Ready/Available condition on
// a CRD object should be suppressed as in-flight rather than emitted as a
// failure. Three independent signals, any of which means "not a real problem":
//
//  1. The condition reason is an in-progress reason (Progressing / Reconciling
//     / Pending / Issuing / DependencyNotReady / …) — shared with the GitOps
//     health mapping via packages.IsTransientConditionReason so the two paths
//     can't drift.
//  2. spec.suspend == true — the object is intentionally paused (Flux
//     Kustomization/HelmRelease, Argo with suspend, suspended CronJob-style
//     CRDs); a paused object reporting not-Ready is expected.
//  3. status.observedGeneration < metadata.generation — the controller has not
//     yet reconciled the current spec, so the stale condition reflects the old
//     generation, not the live state.
func isTransientCRDCondition(u *unstructured.Unstructured, reason string) bool {
	if packages.IsTransientConditionReason(reason) {
		return true
	}
	if suspend, ok, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); ok && suspend {
		return true
	}
	// observedGeneration lags generation → controller hasn't caught up yet.
	gen := u.GetGeneration()
	if gen > 0 {
		if observed, ok, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration"); ok && observed > 0 && observed < gen {
			return true
		}
	}
	return false
}

func classifyDynamicScope(p Provider, gvr schema.GroupVersionResource, kind string) (bool, string, string) {
	if sp, ok := p.(dynamicScopeProvider); ok {
		if namespaced, known := sp.NamespacedForGVR(gvr); known {
			return !namespaced, gvr.Group, gvr.Resource
		}
	}
	return k8s.ClassifyKindScope(kind, gvr.Group)
}

// isCuratedCRDGroup returns true for groups that have their own
// dedicated checker upstream — generic fallback skips them so we
// don't emit duplicate issues with shallower context. Add to this
// list whenever a curated checker is wired into Compose.
func isCuratedCRDGroup(group string) bool {
	switch group {
	case "cluster.x-k8s.io",
		"controlplane.cluster.x-k8s.io",
		"infrastructure.cluster.x-k8s.io",
		"bootstrap.cluster.x-k8s.io":
		return true
	}
	return false
}

// condTypeReason combines the condition type (e.g. "Ready") and the
// optional reason ("CrashLoopBackOff") into one display string. When
// reason is empty, falls back to "<Type>=False".
func condTypeReason(condType, reason string) string {
	if reason != "" {
		return condType + ": " + reason
	}
	return condType + "=False"
}

// ---------------------------------------------------------------------------
// Source-specific normalization
// ---------------------------------------------------------------------------

// resolveGroup returns the explicit group if set, else falls back to the
// built-in (Kind→Group) table. Some legacy Problem emission sites in
// k8s.DetectProblems still leave Group="" for built-in workloads
// (Deployment, StatefulSet, etc.) — without this fallback, the
// group-aware consumer (computeIssueSummaryForResource) would silently
// drop those rows when looking up by canonical group like "apps".
// Centralised here so the (Kind→Group) map lives in one place across
// packages (pkg/audit owns the table; this is a pass-through).
func resolveGroup(group, kind string) string {
	if group != "" {
		return group
	}
	return bp.GroupForBuiltinKind(kind)
}

func fromProblem(p k8s.Problem, now time.Time, source Source) Issue {
	sev := SeverityWarning
	if p.Severity == "critical" {
		sev = SeverityCritical
	}
	since := now.Add(-time.Duration(p.DurationSeconds) * time.Second)
	iss := Issue{
		Severity:             sev,
		Source:               source,
		Kind:                 p.Kind,
		Group:                resolveGroup(p.Group, p.Kind),
		Namespace:            p.Namespace,
		Name:                 p.Name,
		Reason:               p.Reason,
		Message:              p.Message,
		FirstSeen:            since,
		LastSeen:             now,
		Count:                1,
		RestartCount:         p.RestartCount,
		LastTerminatedReason: p.LastTerminatedReason,
	}
	if p.OwnerKind != "" {
		iss.Owner = Ref{
			Group:     resolveGroup("", p.OwnerKind),
			Kind:      p.OwnerKind,
			Namespace: p.Namespace,
			Name:      p.OwnerName,
		}
	}
	classifyIssue(&iss)
	enrichIdentity(&iss)
	return iss
}

// classifyIssue derives the user-facing Category + its CategoryGroup rollup
// from the row's detection signal. Pure: same inputs always yield the same
// labels, so the category stays stable across recomposes (a prerequisite for
// the future category-in-issue-id contract).
func classifyIssue(i *Issue) {
	i.Category = Classify(classifyInput{
		Source:               i.Source,
		APIGroup:             i.Group,
		Kind:                 i.Kind,
		Reason:               i.Reason,
		LastTerminatedReason: i.LastTerminatedReason,
	})
	i.CategoryGroup = GroupOf(i.Category)
}

// ---------------------------------------------------------------------------
// Filter + sort helpers
// ---------------------------------------------------------------------------

// dedupePodSchedulingOverProblem drops the generic problem-source row for a
// Pod when the scheduling source emitted one for the same Pod. A pod stuck
// post-bind (ContainerCreating on a CNI/volume stall) trips both: DetectProblems
// flags it Pending>5m and DetectPostBindProblems names the actual blocker. The
// scheduling row is strictly richer, so it wins. (Bind-time unschedulable pods
// are already skipped in DetectProblems, so this only fires on the post-bind
// overlap.) A plain DetectProblems skip can't replace this — the problem
// threshold is 5m but the post-bind event window is 10m, so a pod stuck >10m
// would lose its only row.
func dedupePodSchedulingOverProblem(in []Issue) []Issue {
	schedPods := map[string]bool{}
	for _, i := range in {
		if i.Source == SourceScheduling && i.Kind == "Pod" {
			schedPods[i.Namespace+"/"+i.Name] = true
		}
	}
	if len(schedPods) == 0 {
		return in
	}
	out := in[:0]
	for _, i := range in {
		if i.Source == SourceProblem && i.Kind == "Pod" && schedPods[i.Namespace+"/"+i.Name] {
			continue
		}
		out = append(out, i)
	}
	return out
}

// subjectRef returns the issue's grouping subject — the topmost owner when one
// was resolved (member pods collapse under their workload), otherwise the
// resource itself. Mirrors enrichIdentity so dedup keys on the same subject the
// ID is built from.
func subjectRef(i Issue) Ref {
	if i.Owner.Kind != "" {
		return i.Owner
	}
	return Ref{Group: i.Group, Kind: i.Kind, Namespace: i.Namespace, Name: i.Name}
}

// childCategories are the specific, root-cause symptoms that, when present for a
// subject, make the parent workload-level rollup (workload_degraded /
// rollout_stalled) redundant. A degraded Deployment with crashlooping pods is
// ONE incident — the crashloop — not two; keeping both is the inverse of
// "50 pods = 1 row".
var childCategories = map[Category]bool{
	CategoryCrashLoop:           true,
	CategoryImagePullFailed:     true,
	CategoryOOMKilled:           true,
	CategoryContainerWaiting:    true,
	CategoryInitContainerFailed: true,
	CategoryLivenessProbeFail:   true,
	CategoryReadinessFailed:     true,
	CategoryUnschedulable:       true,
	CategoryQuotaExceeded:       true,
	CategoryMissingConfigRef:    true,
	CategoryVolumeMountFailed:   true,
	CategoryPVCPending:          true,
}

// parentRollupCategories are the workload-level summaries that should be
// suppressed when a more-specific child symptom exists for the same subject.
var parentRollupCategories = map[Category]bool{
	CategoryWorkloadDegraded: true,
	CategoryRolloutStalled:   true,
}

// dedupeWorkloadDegradedOverChild drops the parent workload rollup row
// (workload_degraded / rollout_stalled) for a subject when a more-specific
// child symptom (crashloop, image_pull_failed, …) of AT LEAST the parent's
// severity was classified for the SAME subject. A degraded Deployment whose
// pods are crashlooping is one incident, not two rows; the child names the
// actual root cause, so it wins.
//
// The severity gate is load-bearing: a critical "0/N available" rollup whose
// only child symptom is a warning (e.g. pods stuck Pending → container_waiting)
// must NOT be suppressed, or dropping the parent would silently downgrade the
// incident critical→warning. So the parent survives when it is strictly more
// severe than every child for the subject, and (as before) when no specific
// child symptom exists at all — a real degraded-without-visible-cause case is
// never dropped.
//
// Keys on subjectRef (owner-collapsed identity) so a parent row emitted on the
// Deployment matches child rows emitted on its member Pods, which carry the
// Deployment as their owner. Mirrors dedupePodSchedulingOverProblem's
// "richer row wins for the same subject" shape.
func dedupeWorkloadDegradedOverChild(in []Issue) []Issue {
	// Per subject, the worst severity among its specific child-symptom rows.
	maxChildSev := map[string]int{}
	for _, i := range in {
		if childCategories[i.Category] {
			k := subjectKeyOf(subjectRef(i))
			if r := severityRank(i.Severity); r > maxChildSev[k] {
				maxChildSev[k] = r
			}
		}
	}
	if len(maxChildSev) == 0 {
		return in
	}
	out := in[:0]
	for _, i := range in {
		if parentRollupCategories[i.Category] {
			// Suppress only when a child at least as severe exists — never
			// downgrade a critical rollup to a warning child.
			if r, ok := maxChildSev[subjectKeyOf(subjectRef(i))]; ok && r >= severityRank(i.Severity) {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

// subjectKeyOf is the canonical string key for a subject Ref — the same
// group|kind|namespace|name key the ID hash and audit deep-links use, so dedup
// can't drift from grouping.
func subjectKeyOf(r Ref) string {
	return resourceKey(r.Group, r.Kind, r.Namespace, r.Name)
}

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

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	}
	return 0
}
