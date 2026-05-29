package issues

import (
	"testing"

	"github.com/skyhook-io/radar/pkg/subject"
)

// ScopeForKind + IssueID determinism/keying now live in (and are tested by)
// pkg/subject — issues consumes them via enrichIdentity. This test pins the
// issues-level behavior: enrichIdentity derives the owner-else-self subject and
// keys the ID off it, using the shared resolver.
func TestEnrichIdentity_SubjectIsOwnerElseSelf(t *testing.T) {
	// A pod with a resolved owner groups under the owner — the ID is keyed on
	// the workload, not the pod. The expected value uses subject.IssueID (what
	// enrichIdentity now calls), confirming the migration re-keys nothing.
	pod := Issue{
		Source: SourceProblem, Kind: "Pod", Namespace: "ns", Name: "web-abc-1", Reason: "ImagePullBackOff",
		Owner: Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"},
	}
	classifyIssue(&pod)
	enrichIdentity(&pod)
	if pod.GroupingScope != ScopeWorkload {
		t.Errorf("scope = %q, want workload", pod.GroupingScope)
	}
	if want := subject.IssueID(ScopeWorkload, resourceKey("apps", "Deployment", "ns", "web"), string(CategoryImagePullFailed)); pod.ID != want {
		t.Errorf("ID = %q, want owner-keyed %q", pod.ID, want)
	}

	// A standalone pod (no owner) is its own subject.
	solo := Issue{Source: SourceProblem, Kind: "Pod", Namespace: "ns", Name: "solo", Reason: "CrashLoopBackOff"}
	classifyIssue(&solo)
	enrichIdentity(&solo)
	if want := subject.IssueID(ScopeWorkload, resourceKey("", "Pod", "ns", "solo"), string(CategoryCrashLoop)); solo.ID != want {
		t.Errorf("standalone pod ID = %q, want self-keyed %q", solo.ID, want)
	}
}
