package issues

import (
	"strings"
	"time"

	"github.com/skyhook-io/radar/internal/k8s"
	"github.com/skyhook-io/radar/pkg/conditions"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

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
		kind := p.KindForGVR(gvr)
		if kind == "" {
			continue
		}
		if isCuratedCRDKind(gvr.Group, kind) {
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
		// Gather candidate objects RBAC-safely:
		//  - cluster-scoped CRD → one cluster-wide list (already access-gated above).
		//  - namespaced CRD with an explicit namespace set → list each (the set is
		//    auth-filtered upstream by the handler).
		//  - namespaced CRD with NO namespace set → the caller is cluster-wide
		//    authorized (restricted users always have their set injected), so union
		//    across every watched scope. A plain ListDynamic(gvr,"") would read only
		//    a cluster-wide informer and silently miss namespace-scoped ones.
		var items []*unstructured.Unstructured
		switch {
		case clusterScoped:
			its, err := p.ListDynamic(gvr, "")
			if err != nil {
				continue
			}
			items = its
		case len(f.Namespaces) > 0:
			for _, ns := range f.Namespaces {
				its, err := p.ListDynamic(gvr, ns)
				if err != nil {
					continue
				}
				items = append(items, its...)
			}
		default:
			its, err := p.ListDynamicAllNamespaces(gvr)
			if err != nil {
				continue
			}
			items = its
		}
		for _, u := range items {
			condType, reason, msg, since, ok := conditions.FindFalseCondition(u)
			if !ok {
				continue
			}
			// Noise-floor suppression: a False Ready/Available on an object that
			// is suspended, still reconciling, or whose controller hasn't yet
			// observed the current spec is NOT a failure — it's in-flight.
			// Emitting a warning for it is the canonical alert-fatigue trap,
			// since auto-refresh keeps it permanently lit. Skip those; keep
			// genuinely-failed objects.
			if isTransientCRDCondition(u, reason) {
				continue
			}
			severity := SeverityWarning
			issReason := condTypeReason(condType, reason)
			issMsg := msg
			// Argo Rollout: FindFalseCondition picks Healthy=False/RolloutHealthy
			// first (Healthy precedes Available in the Rollout's condition list),
			// which reads as "healthy" and buries the real cause. When a
			// definitive failure condition is present, surface it as critical.
			if kind == "Rollout" && strings.Contains(strings.ToLower(gvr.Group), "argoproj.io") {
				if r, m, found := argoRolloutFailure(u); found {
					issReason, issMsg, severity = r, m, SeverityCritical
				}
			}
			lastSeen := time.Now().Add(-since)
			iss := Issue{
				Severity:  severity,
				Source:    SourceCondition,
				Kind:      kind,
				Group:     gvr.Group,
				Namespace: u.GetNamespace(),
				Name:      u.GetName(),
				Reason:    issReason,
				Message:   issMsg,
				FirstSeen: lastSeen,
				LastSeen:  lastSeen,
				Count:     1,
			}
			classifyIssue(&iss)
			enrichIdentity(&iss)
			out = append(out, iss)
		}
	}
	return out
}

// isTransientCRDCondition reports whether a False Ready/Available condition on
// a CRD object should be suppressed as in-flight rather than emitted as a
// failure. Three independent signals, any of which means "not a real problem":
//
//  1. The condition reason is in-progress per conditions.IsInProgressForIssues
//     — the shared transient set MINUS the genuine-failure reasons
//     (ArtifactFailed / ChartNotReady): the health badge may soften those to
//     "degraded" (still visible), but the Issues queue must surface them, not
//     drop them. This is the one place the Issues noise-floor deliberately
//     diverges from the health-display transient set.
//  2. spec.suspend == true — the object is intentionally paused (Flux
//     Kustomization/HelmRelease, Argo with suspend, suspended CronJob-style
//     CRDs); a paused object reporting not-Ready is expected.
//  3. status.observedGeneration < metadata.generation — the controller has not
//     yet reconciled the current spec, so the stale condition reflects the old
//     generation, not the live state.
func isTransientCRDCondition(u *unstructured.Unstructured, reason string) bool {
	if conditions.IsInProgressForIssues(reason) {
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

// isCuratedCRDKind reports whether a curated detector already owns this
// (group, kind), so the generic CRD fallback must skip it to avoid a
// double-report. CAPI groups are curated wholesale; the GitOps groups are
// curated only for the specific kinds DetectGitOpsProblems handles —
// sibling kinds (e.g. argoproj.io Rollout, fluxcd GitRepository) still flow
// through the generic path, which is their only coverage.
func isCuratedCRDKind(group, kind string) bool {
	if isCuratedCRDGroup(group) {
		return true
	}
	switch group {
	case "argoproj.io":
		return kind == "Application"
	case "kustomize.toolkit.fluxcd.io":
		return kind == "Kustomization"
	case "helm.toolkit.fluxcd.io":
		return kind == "HelmRelease"
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

// argoRolloutFailure returns the definitive failing condition of an Argo
// Rollout, in root-cause-first order: an invalid spec, then a progress-deadline
// stall. Both are unambiguous failures (no in-progress ambiguity), so the
// caller promotes them to critical and uses their reason instead of the generic
// Healthy=False/RolloutHealthy the condition reader would otherwise surface.
// ok=false leaves the generic reason untouched.
func argoRolloutFailure(u *unstructured.Unstructured) (reason, message string, ok bool) {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found {
		return "", "", false
	}
	cond := func(condType string) (status, reason, message string) {
		for _, c := range conds {
			cm, isMap := c.(map[string]any)
			if !isMap {
				continue
			}
			if ct, _ := cm["type"].(string); ct == condType {
				status, _ = cm["status"].(string)
				reason, _ = cm["reason"].(string)
				message, _ = cm["message"].(string)
				return
			}
		}
		return
	}
	if s, r, m := cond("InvalidSpec"); s == "True" {
		if r == "" {
			r = "InvalidSpec"
		}
		return r, m, true
	}
	if s, r, m := cond("Progressing"); s == "False" && r == "ProgressDeadlineExceeded" {
		return r, m, true
	}
	return "", "", false
}

// ---------------------------------------------------------------------------
// Source-specific normalization
// ---------------------------------------------------------------------------
