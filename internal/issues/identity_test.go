package issues

import "testing"

func TestScopeForKind(t *testing.T) {
	cases := map[string]Scope{
		"Pod":                     ScopeWorkload,
		"Deployment":              ScopeWorkload,
		"StatefulSet":             ScopeWorkload,
		"DaemonSet":               ScopeWorkload,
		"Job":                     ScopeWorkload,
		"CronJob":                 ScopeWorkload,
		"Service":                 ScopeService,
		"Ingress":                 ScopeIngress,
		"PersistentVolumeClaim":   ScopePVC,
		"Node":                    ScopeNode,
		"HorizontalPodAutoscaler": ScopeUnknown,
		"Certificate":             ScopeUnknown,
	}
	for kind, want := range cases {
		if got := scopeForKind(kind); got != want {
			t.Errorf("scopeForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestIssueID_DeterministicAndDistinct(t *testing.T) {
	key := resourceKey("apps", "Deployment", "ns", "web")
	base := issueID(ScopeWorkload, key, CategoryImagePullFailed)

	if base != issueID(ScopeWorkload, key, CategoryImagePullFailed) {
		t.Error("issueID not deterministic for identical inputs")
	}
	// Each component must change the ID.
	if base == issueID(ScopeWorkload, key, CategoryCrashLoop) {
		t.Error("category must change the ID")
	}
	if base == issueID(ScopeWorkload, resourceKey("apps", "Deployment", "ns", "other"), CategoryImagePullFailed) {
		t.Error("subject must change the ID")
	}
	if base == issueID(ScopeService, key, CategoryImagePullFailed) {
		t.Error("scope must change the ID")
	}
}

func TestEnrichIdentity_SubjectIsOwnerElseSelf(t *testing.T) {
	// A pod with a resolved owner groups under the owner — the ID is keyed
	// on the workload, not the pod.
	pod := Issue{
		Source: SourceProblem, Kind: "Pod", Namespace: "ns", Name: "web-abc-1", Reason: "ImagePullBackOff",
		Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"},
	}
	classifyIssue(&pod)
	enrichIdentity(&pod)
	if pod.GroupingScope != ScopeWorkload {
		t.Errorf("scope = %q, want workload", pod.GroupingScope)
	}
	if want := issueID(ScopeWorkload, resourceKey("apps", "Deployment", "ns", "web"), CategoryImagePullFailed); pod.ID != want {
		t.Errorf("ID = %q, want owner-keyed %q", pod.ID, want)
	}

	// A standalone pod (no owner) is its own subject.
	solo := Issue{Source: SourceProblem, Kind: "Pod", Namespace: "ns", Name: "solo", Reason: "CrashLoopBackOff"}
	classifyIssue(&solo)
	enrichIdentity(&solo)
	if want := issueID(ScopeWorkload, resourceKey("", "Pod", "ns", "solo"), CategoryCrashLoop); solo.ID != want {
		t.Errorf("standalone pod ID = %q, want self-keyed %q", solo.ID, want)
	}
}
