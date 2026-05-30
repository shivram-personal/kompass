package issues

import (
	"fmt"
	"testing"
	"time"

	"github.com/skyhook-io/radar/internal/k8s"
)

// flatPod builds a flat pod issue the way Compose would — classified +
// identity-enriched — so grouping tests exercise the real id/owner/scope.
func flatPod(name, reason string, sev Severity, owner Ref, first, last time.Time) Issue {
	i := Issue{
		Source: SourceProblem, Kind: "Pod", Namespace: "ns", Name: name,
		Reason: reason, Severity: sev, Owner: owner,
		FirstSeen: first, LastSeen: last, Count: 1,
	}
	classifyIssue(&i)
	enrichIdentity(&i)
	return i
}

func TestGroupIssues_FoldsMembersUnderOwner(t *testing.T) {
	dep := Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"}
	t0, t1, t2 := time.Unix(1000, 0), time.Unix(2000, 0), time.Unix(3000, 0)
	// web-b is the worst member: critical, oldest onset, newest last_seen,
	// and a distinct (same-category) reason — it must drive the rep fields.
	flat := []Issue{
		flatPod("web-a", "ImagePullBackOff", SeverityWarning, dep, t1, t1),
		flatPod("web-b", "ErrImagePull", SeverityCritical, dep, t0, t2),
		flatPod("web-c", "ImagePullBackOff", SeverityWarning, dep, t1, t1),
	}
	got := GroupIssues(flat)
	if len(got) != 1 {
		t.Fatalf("want 1 grouped row, got %d", len(got))
	}
	g := got[0]
	if g.Group != "apps" || g.Kind != "Deployment" || g.Name != "web" {
		t.Errorf("subject = %s/%s/%s, want apps/Deployment/web", g.Group, g.Kind, g.Name)
	}
	if g.Count != 3 || g.Affected.Pods != 3 || len(g.Members) != 3 {
		t.Errorf("count=%d affected.pods=%d members=%d, want 3/3/3", g.Count, g.Affected.Pods, len(g.Members))
	}
	if g.Severity != SeverityCritical {
		t.Errorf("severity = %q, want critical (max of members)", g.Severity)
	}
	if g.Reason != "ErrImagePull" {
		t.Errorf("reason = %q, want the worst member's ErrImagePull", g.Reason)
	}
	if !g.FirstSeen.Equal(t0) {
		t.Errorf("first_seen = %v, want oldest %v", g.FirstSeen, t0)
	}
	if !g.LastSeen.Equal(t2) {
		t.Errorf("last_seen = %v, want newest %v", g.LastSeen, t2)
	}
	if g.Members[0].Name != "web-a" || g.Members[2].Name != "web-c" {
		t.Errorf("members not sorted by name: %+v", g.Members)
	}
	if g.Owner.Kind != "" {
		t.Errorf("grouped row should not carry Owner (subject is top-level): %+v", g.Owner)
	}
}

func TestGroupIssues_StandalonePodIsOwnSubject(t *testing.T) {
	flat := []Issue{flatPod("solo", "CrashLoopBackOff", SeverityCritical, Ref{}, time.Unix(1, 0), time.Unix(1, 0))}
	got := GroupIssues(flat)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	g := got[0]
	if g.Kind != "Pod" || g.Name != "solo" {
		t.Errorf("subject = %s/%s, want Pod/solo", g.Kind, g.Name)
	}
	// No fan-out: the subject is the only resource, so the affected-resource
	// count (non-subject members) is 0.
	if g.Count != 0 || len(g.Members) != 0 || g.Affected.Pods != 0 {
		t.Errorf("single-resource issue: count=%d members=%d affected.pods=%d, want 0/0/0", g.Count, len(g.Members), g.Affected.Pods)
	}
}

func TestGroupIssues_DistinctCategoriesStaySeparate(t *testing.T) {
	dep := Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"}
	flat := []Issue{
		flatPod("web-a", "ImagePullBackOff", SeverityCritical, dep, time.Unix(1, 0), time.Unix(1, 0)),
		flatPod("web-b", "CrashLoopBackOff", SeverityCritical, dep, time.Unix(1, 0), time.Unix(1, 0)),
	}
	got := GroupIssues(flat)
	if len(got) != 2 {
		t.Fatalf("same owner, different categories must stay separate: got %d rows", len(got))
	}
}

func TestGroupIssues_MemberTruncation(t *testing.T) {
	dep := Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"}
	var flat []Issue
	for i := 0; i < 12; i++ {
		flat = append(flat, flatPod(fmt.Sprintf("web-%02d", i), "ImagePullBackOff", SeverityCritical, dep, time.Unix(1, 0), time.Unix(1, 0)))
	}
	got := GroupIssues(flat)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	g := got[0]
	// Counts reflect all 12; the inline member slice is capped + flagged.
	if g.Count != 12 || g.Affected.Pods != 12 {
		t.Errorf("count=%d affected.pods=%d, want 12/12", g.Count, g.Affected.Pods)
	}
	if !g.MembersTruncated || len(g.Members) != maxInlineMembers {
		t.Errorf("members len=%d truncated=%v, want %d/true", len(g.Members), g.MembersTruncated, maxInlineMembers)
	}
}

func TestGroupIssues_Deterministic(t *testing.T) {
	dep := Ref{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "web"}
	sts := Ref{Group: "apps", Kind: "StatefulSet", Namespace: "ns", Name: "db"}
	mk := func() []Issue {
		return []Issue{
			flatPod("web-a", "ImagePullBackOff", SeverityWarning, dep, time.Unix(1, 0), time.Unix(1, 0)),
			flatPod("web-b", "ErrImagePull", SeverityCritical, dep, time.Unix(1, 0), time.Unix(2, 0)),
			flatPod("db-x", "CrashLoopBackOff", SeverityCritical, sts, time.Unix(1, 0), time.Unix(3, 0)),
		}
	}
	a := GroupIssues(mk())
	in := mk()
	in[0], in[2] = in[2], in[0] // reorder input
	b := GroupIssues(in)
	if len(a) != len(b) {
		t.Fatalf("len differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Reason != b[i].Reason || a[i].Name != b[i].Name {
			t.Errorf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestComposeWithStats_GroupedCapsOnGroups(t *testing.T) {
	var probs []k8s.Detection
	for i := 0; i < 5; i++ {
		probs = append(probs, k8s.Detection{
			Kind: "Pod", Namespace: "ns", Name: fmt.Sprintf("web-%d", i),
			Severity: "critical", Reason: "ImagePullBackOff",
			OwnerKind: "Deployment", OwnerName: "web",
		})
	}
	p := &fakeProvider{problems: probs}

	flat, fstats := ComposeWithStats(p, Filters{})
	if len(flat) != 5 || fstats.TotalMatched != 5 {
		t.Fatalf("flat: want 5 rows / matched 5, got %d / %d", len(flat), fstats.TotalMatched)
	}

	g, gstats := ComposeWithStats(p, Filters{Grouped: true})
	if len(g) != 1 || gstats.TotalMatched != 1 {
		t.Fatalf("grouped: want 1 row / matched 1 (cap counts groups), got %d / %d", len(g), gstats.TotalMatched)
	}
	if g[0].Affected.Pods != 5 || g[0].Count != 5 {
		t.Errorf("grouped row should reflect 5 members: affected.pods=%d count=%d", g[0].Affected.Pods, g[0].Count)
	}
}
