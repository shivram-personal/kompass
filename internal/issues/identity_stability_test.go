package issues

import (
	"testing"
	"time"

	"github.com/skyhook-io/radar/pkg/issuesapi"
)

// The Radar Hub alert worker keys an issue's open/resolved lifecycle on
// issue.ID: a matching issue that keeps the same ID across polls is one
// continuous alert, a new ID is a new alert. So if the SAME underlying problem
// yields a DIFFERENT ID across polls, the worker sees spurious resolve→reopen
// churn and fires false notifications. These tests pin ID stability for the
// high-volume categories alerting fires on by default, across the field
// variation a detector emits poll-to-poll.

func classifiedIssue(i Issue) Issue {
	classifyIssue(&i)
	enrichIdentity(&i)
	return i
}

// assertSameID asserts every variant resolves to one ID and one category — the
// poll-to-poll stability the lifecycle keys on. Returns the shared category.
func assertSameID(t *testing.T, variants ...Issue) issuesapi.Category {
	t.Helper()
	id, cat := variants[0].ID, variants[0].Category
	for _, v := range variants {
		if v.ID != id {
			t.Errorf("ID drift (reason %q): got %q, want %q", v.Reason, v.ID, id)
		}
		if v.Category != cat {
			t.Errorf("category drift (reason %q): got %q, want %q", v.Reason, v.Category, cat)
		}
	}
	return cat
}

// A crashlooping container cycles its reason across polls — CrashLoopBackOff
// while backing off, Error/Failed at the instant it exits. All three are the
// same crash and must keep one ID, or every backoff cycle reads as a new alert.
func TestIDStable_CrashLoopReasonOscillation(t *testing.T) {
	owner := Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"}
	mk := func(reason string) Issue {
		return classifiedIssue(Issue{Source: SourceProblem, Kind: "Pod", Namespace: "prod", Name: "api-7d9-xyz", Reason: reason, Owner: owner})
	}
	if cat := assertSameID(t, mk("CrashLoopBackOff"), mk("Error"), mk("Failed")); cat != issuesapi.CategoryCrashLoop {
		t.Errorf("category = %q, want crashloop", cat)
	}
}

// The kubelet retries an image pull, flapping the reason across the pull-error
// family. One image problem, one ID.
func TestIDStable_ImagePullReasonFamily(t *testing.T) {
	owner := Ref{Group: "apps", Kind: "StatefulSet", Namespace: "prod", Name: "db"}
	mk := func(reason string) Issue {
		return classifiedIssue(Issue{Source: SourceProblem, Kind: "Pod", Namespace: "prod", Name: "db-0", Reason: reason, Owner: owner})
	}
	if cat := assertSameID(t, mk("ImagePullBackOff"), mk("ErrImagePull"), mk("ImageInspectError"), mk("InvalidImageName")); cat != issuesapi.CategoryImagePullFailed {
		t.Errorf("category = %q, want image_pull_failed", cat)
	}
}

// Symptom fields (count, message, restart count, first-seen) churn every poll
// but MUST NOT re-key — the ID is identity, not symptom. This encodes the
// "count/message changes don't re-key" invariant the alerting design relies on.
func TestIDStable_SymptomFieldsDoNotRekey(t *testing.T) {
	base := Issue{
		Source: SourceProblem, Kind: "Pod", Namespace: "prod", Name: "api-1", Reason: "CrashLoopBackOff",
		Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"},
	}
	v1 := base
	v1.Count, v1.Message, v1.RestartCount, v1.FirstSeen = 1, "back-off 10s restarting", 3, time.Unix(1700000000, 0)
	v2 := base
	v2.Count, v2.Message, v2.RestartCount, v2.FirstSeen = 47, "back-off 5m0s restarting", 219, time.Unix(1700009999, 0)
	assertSameID(t, classifiedIssue(v1), classifiedIssue(v2))
}

// The "survives a Radar restart" property: the ID is a pure function of
// (scope, resource, cause) with no wall-clock or run-local input, so rebuilding
// from scratch yields the identical ID for every high-volume category.
func TestIDStable_DeterministicAcrossRebuild(t *testing.T) {
	cases := []Issue{
		{Source: SourceProblem, Kind: "Pod", Namespace: "p", Name: "a-1", Reason: "CrashLoopBackOff", Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "p", Name: "a"}},
		{Source: SourceProblem, Kind: "Pod", Namespace: "p", Name: "b-1", Reason: "ImagePullBackOff", Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "p", Name: "b"}},
		{Source: SourceProblem, Kind: "Pod", Namespace: "p", Name: "c-1", Reason: "OOMKilled"},
		{Source: SourceScheduling, Kind: "Pod", Namespace: "p", Name: "d-1", Reason: "Unschedulable"},
		{Source: SourceMissingRef, Kind: "Pod", Namespace: "p", Name: "e-1", Reason: "Missing ConfigMap", Fingerprint: `Missing ConfigMap|cfg "x"`, Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "p", Name: "e"}},
		{Source: SourceCondition, Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "store", Reason: "HealthDegraded"},
	}
	for _, c := range cases {
		first := classifiedIssue(c)
		again := classifiedIssue(c)
		if first.ID != again.ID {
			t.Errorf("non-deterministic ID for %s/%s reason %q: %q vs %q", c.Kind, c.Name, c.Reason, first.ID, again.ID)
		}
		if first.ID == "" || first.Category == issuesapi.CategoryUnknown {
			t.Errorf("expected a classified, non-empty ID for %s reason %q, got id=%q cat=%q", c.Kind, c.Reason, first.ID, first.Category)
		}
	}
}

// Missing ConfigMap / Secret / PVC on one workload are distinct dangling refs:
// each keeps its own ID via the detector fingerprint, so resolving the Secret
// doesn't collapse the still-open ConfigMap alert — and each is itself stable
// on re-detection.
func TestIDStable_MissingRefFingerprint(t *testing.T) {
	owner := Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"}
	mk := func(reason, fp string) Issue {
		return classifiedIssue(Issue{Source: SourceMissingRef, Kind: "Pod", Namespace: "prod", Name: "api-x", Reason: reason, Fingerprint: fp, Owner: owner})
	}
	cm := mk("Missing ConfigMap", `Missing ConfigMap|cfg "app-config"`)
	sec := mk("Missing Secret", `Missing Secret|secret "db-creds"`)
	pvc := mk("Missing PVC", `Missing PVC|pvc "data"`)
	if cm.Category != issuesapi.CategoryMissingConfigRef || sec.Category != issuesapi.CategoryMissingConfigRef || pvc.Category != issuesapi.CategoryMissingConfigRef {
		t.Fatalf("precondition: all missing_config_ref, got %q/%q/%q", cm.Category, sec.Category, pvc.Category)
	}
	if ids := map[string]bool{cm.ID: true, sec.ID: true, pvc.ID: true}; len(ids) != 3 {
		t.Errorf("distinct dangling refs must get distinct IDs: cm=%q sec=%q pvc=%q", cm.ID, sec.ID, pvc.ID)
	}
	if again := mk("Missing PVC", `Missing PVC|pvc "data"`); again.ID != pvc.ID {
		t.Errorf("same dangling ref must fold to one ID: %q vs %q", again.ID, pvc.ID)
	}
}

// Documented oscillation BOUNDARY: a crashlooping pod momentarily in
// ContainerCreating/Pending classifies as container_waiting — a DIFFERENT ID
// than its crashloop identity. This is intentional (genuinely distinct
// categories) and is precisely why the alert worker's resolve grace must absorb
// a brief flap rather than treat it as resolve+reopen. Pinned so a classify
// change that merges these (or splits crashloop further) is a conscious
// decision, not a silent alerting regression.
func TestID_CrashLoopVsContainerWaitingIsADistinctIdentity(t *testing.T) {
	owner := Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"}
	mk := func(reason string) Issue {
		return classifiedIssue(Issue{Source: SourceProblem, Kind: "Pod", Namespace: "prod", Name: "api-1", Reason: reason, Owner: owner})
	}
	crash := mk("CrashLoopBackOff")
	waiting := mk("ContainerCreating")
	if waiting.Category != issuesapi.CategoryContainerWaiting {
		t.Fatalf("precondition: ContainerCreating → container_waiting, got %q", waiting.Category)
	}
	if crash.ID == waiting.ID {
		t.Error("crashloop and container_waiting now share an ID — the discriminator no longer separates them; alert lifecycle would silently merge two distinct phases")
	}
}
