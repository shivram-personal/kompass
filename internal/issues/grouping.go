package issues

import "sort"

// maxInlineMembers bounds the member refs carried inline on a grouped issue.
// Enough for a human or agent to see what folded without a second call;
// full member state stays lazy (evidence). Past this, membersTruncated is
// set and the slice is capped.
const maxInlineMembers = 10

// Affected counts the underlying resources folded into a grouped issue, by
// kind bucket. Empty for single-resource issues (no fan-out) — there the
// subject row already says everything.
type Affected struct {
	Pods      int `json:"pods,omitempty"`
	Workloads int `json:"workloads,omitempty"`
	Services  int `json:"services,omitempty"`
	PVCs      int `json:"pvcs,omitempty"`
	Nodes     int `json:"nodes,omitempty"`
}

// GroupIssues folds flat issue rows into the public grouped model: one row
// per shared ID (subject + category). The flat rows are the evidence; a
// grouped row is the operational issue an operator or agent triages.
//
// Deterministic by construction — the representative member and member
// ordering are chosen by total comparators, so the same input always
// yields the same output regardless of input order. Input is not mutated.
func GroupIssues(flat []Issue) []Issue {
	buckets := make(map[string][]Issue)
	for _, r := range flat {
		buckets[r.ID] = append(buckets[r.ID], r)
	}
	out := make([]Issue, 0, len(buckets))
	for _, members := range buckets {
		out = append(out, foldGroup(members))
	}
	sort.SliceStable(out, func(i, j int) bool { return lessIssue(out[i], out[j]) })
	return out
}

// foldGroup collapses one group's member rows into a single grouped issue,
// applying the representative rules: the worst member drives severity +
// reason/message/crash context; age is the oldest onset; last_seen the
// newest. members are the folded underlying resources (the fan-out),
// excluding the subject itself.
func foldGroup(members []Issue) Issue {
	rep := members[0]
	for _, m := range members[1:] {
		if betterRepresentative(m, rep) {
			rep = m
		}
	}

	subject := Ref{Group: rep.Group, Kind: rep.Kind, Namespace: rep.Namespace, Name: rep.Name}
	if rep.Owner.Kind != "" {
		subject = rep.Owner
	}

	g := Issue{
		Severity:             rep.Severity,
		Source:               rep.Source,
		Category:             rep.Category,
		CategoryGroup:        rep.CategoryGroup,
		ID:                   rep.ID,
		GroupingScope:        rep.GroupingScope,
		Kind:                 subject.Kind,
		Group:                subject.Group,
		Namespace:            subject.Namespace,
		Name:                 subject.Name,
		Reason:               rep.Reason,
		Message:              rep.Message,
		RestartCount:         rep.RestartCount,
		LastTerminatedReason: rep.LastTerminatedReason,
		FirstSeen:            rep.FirstSeen,
		LastSeen:             rep.LastSeen,
	}

	var refs []Ref
	for _, m := range members {
		if !m.FirstSeen.IsZero() && (g.FirstSeen.IsZero() || m.FirstSeen.Before(g.FirstSeen)) {
			g.FirstSeen = m.FirstSeen
		}
		if m.LastSeen.After(g.LastSeen) {
			g.LastSeen = m.LastSeen
		}
		own := Ref{Group: m.Group, Kind: m.Kind, Namespace: m.Namespace, Name: m.Name}
		if own != subject {
			refs = append(refs, own)
		}
	}
	sortRefs(refs)
	// Count is the affected-resource fan-out — the non-subject members under
	// this subject (the subject is shown separately as the header, not under
	// "Affected resources"). Matches the UI/TS contract; captured before the
	// inline-member truncation below so "Showing X of N" stays honest.
	g.Count = len(refs)
	g.Affected = affectedOf(refs)
	if len(refs) > maxInlineMembers {
		g.MembersTruncated = true
		refs = refs[:maxInlineMembers]
	}
	g.Members = refs
	return g
}

// betterRepresentative reports whether cand should replace cur as a group's
// representative: worst severity wins, then newest last_seen, then name
// (member names are unique within a group, so the order is total →
// deterministic regardless of iteration order).
func betterRepresentative(cand, cur Issue) bool {
	if c, r := SeverityRank(cand.Severity), SeverityRank(cur.Severity); c != r {
		return c > r
	}
	if !cand.LastSeen.Equal(cur.LastSeen) {
		return cand.LastSeen.After(cur.LastSeen)
	}
	return cand.Name < cur.Name
}

func affectedOf(refs []Ref) Affected {
	var a Affected
	for _, r := range refs {
		switch r.Kind {
		case "Pod":
			a.Pods++
		case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
			a.Workloads++
		case "Service":
			a.Services++
		case "PersistentVolumeClaim":
			a.PVCs++
		case "Node":
			a.Nodes++
		}
	}
	return a
}

func sortRefs(refs []Ref) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Namespace != refs[j].Namespace {
			return refs[i].Namespace < refs[j].Namespace
		}
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Group < refs[j].Group
	})
}

// lessIssue is the canonical issue sort: severity desc, then ONSET (first_seen
// desc) — deliberately NOT last_seen, which bumps to compose-time on every poll
// and would reshuffle same-severity rows on each refetch. Then category / kind /
// namespace / name for a stable total order. Matches the shared UI comparator
// (k8s-ui issues/types.ts:compareIssues) so /api/issues, MCP, and the UI all
// return one stable queue.
func lessIssue(a, b Issue) bool {
	if a.Severity != b.Severity {
		return SeverityRank(a.Severity) > SeverityRank(b.Severity)
	}
	if !a.FirstSeen.Equal(b.FirstSeen) {
		return a.FirstSeen.After(b.FirstSeen)
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	// Final tiebreak: two grouped rows can share a subject (kind/ns/name)
	// and differ only by category. Keeps the order total + deterministic.
	return a.Category < b.Category
}
