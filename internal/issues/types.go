// Package issues unifies cluster health signals into a single
// normalized envelope. It composes:
//   - problem    — radar's hardcoded per-kind live-state detection
//     (failing Deployments, NotReady Nodes, pending PVCs…)
//   - missing_ref — direct by-name references to objects that do not exist
//     (missing PVCs, ConfigMaps, Secrets, backend Services, roleRefs…)
//   - scheduling — why a Pod can't run: unschedulable (arch/taint/resources/
//     affinity, with the offending node label named), rejected at admission
//     (quota/LimitRange/PodSecurity/webhook — no Pod is even created), or
//     stuck post-bind (CNI IP exhaustion, volume attach/mount)
//   - condition  — generic CRD .status.conditions[].status=False fallback
//     (Argo/Flux/Knative/Crossplane/cert-manager/KEDA)
//
// All four describe LIVE OPERATIONAL STATE — "what is failing right
// now". Two adjacent signals are deliberately NOT composed here, each
// with its own home: raw K8s Warning events (get_events + the timeline)
// and policy/posture — Kyverno PolicyReports + static best-practice
// findings (runAsRoot, missing probes, no PDB, deprecated APIs, …) which
// live in pkg/audit + /api/audit + MCP get_cluster_audit. A healthy pod
// can have many audit findings, a crashing pod can have zero. Combining
// them would force consumers to disambiguate "is this critical
// operational or critical posture?" at every callsite.
//
// The Issue type is what /api/issues and the hub's fleet_issues MCP
// tool emit. Severity is normalized to a 2-tier vocabulary
// (critical/warning) so consumers don't need to translate between the
// parallel severity scales the underlying sources use. Info-level
// detections are posture/inert noise and are dropped at compose (see
// compose.go) — the issue stream is "what's broken now", not an audit.
package issues

import (
	"time"
)

// Severity is the normalized issue severity. The public Issues contract is
// critical|warning only:
//
//	critical = problem.critical
//	warning  = problem.<any non-critical except info> | CRD-condition False
//
// problem severities other than "critical" collapse to warning — see fromProblem
// (the mapping is non-critical by exclusion, not an explicit allow-list). The one
// exception is problem.info: inert/posture findings (deprecated-RBAC residue,
// singleton-StatefulSet headless-DNS trivia) are DROPPED at the Problem→Issue
// boundary in Compose and never become Issues — they belong to audit/posture,
// not the live "what's broken now" stream.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// Source records which underlying detection channel emitted this issue.
// It is an OUTPUT label (for SPA copy that explains why a row appeared,
// and as a CEL filter binding), not an input filter — issues composes all
// four sources unconditionally; detection provenance is not a triage axis.
type Source string

const (
	SourceProblem    Source = "problem"     // radar's hardcoded per-kind detection
	SourceMissingRef Source = "missing_ref" // dangling-ref detection (Pod→missing PVC/CM/Secret/SA, HPA→missing target, Ingress→missing backend, etc.)
	SourceScheduling Source = "scheduling"  // placement/admission/post-bind failures (unschedulable, quota/PodSecurity/webhook, CNI/volume)
	SourceCondition  Source = "condition"   // generic CRD .status.conditions[].status=False fallback
)

// Ref is a lightweight resource reference for the grouping subject and
// owner pointers. Group is the API group (empty for core) — carried so
// owner/affected deep-links can disambiguate CRDs from core kinds.
type Ref struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Issue is the unified cluster-health record.
//
// Flat (pre-group) rows are snapshot-derived. GroupIssues folds them and sets
// Count to the affected-resource fan-out EXCLUDING the subject (the subject is
// the row header, surfaced separately) — so a single-resource issue has
// Count = 0 (omitted on the wire), and a 50-pod crashloop under one Deployment
// has Count = 50. For problem / missing_ref / scheduling, LastSeen is the
// compose time and FirstSeen backs off by the observed problem duration; for
// condition rows, both timestamps are the condition's lastTransitionTime.
type Issue struct {
	Severity Severity `json:"severity"`
	Source   Source   `json:"source"`
	// Category is the user-facing symptom taxonomy (image_pull_failed,
	// crashloop, …), derived from Source+Kind+Reason by Classify;
	// CategoryGroup is its coarse rollup (GroupOf). Both are server-emitted
	// labels so the UI renders them without its own category→group map.
	// Distinct from Group below, which is the resource's API group.
	Category      Category      `json:"category,omitempty"`
	CategoryGroup CategoryGroup `json:"category_group,omitempty"`
	// ID is the deterministic, cluster-local issue identity —
	// hash(grouping_scope, subject key, category). Shared by every row that
	// rolls up to the same subject+category, so consumers can group on it;
	// the hub namespaces it by cluster_id for global uniqueness.
	ID string `json:"id,omitempty"`
	// GroupingScope is the kind of subject this issue groups under
	// (workload|service|pvc|ingress|node|unknown) — drives the UI section
	// and is part of ID.
	GroupingScope Scope     `json:"grouping_scope,omitempty"`
	Kind          string    `json:"kind"`
	Group         string    `json:"group,omitempty"`
	Namespace     string    `json:"namespace,omitempty"`
	Name          string    `json:"name"`
	Reason        string    `json:"reason"`
	Message       string    `json:"message,omitempty"`
	FirstSeen     time.Time `json:"first_seen,omitzero"`
	LastSeen      time.Time `json:"last_seen,omitzero"`
	Count         int       `json:"count,omitempty"`
	// Owner is flat-only: it's present on ?view=flat / pre-fold MCP rows so a
	// consumer can see the resolved top-owner. Grouped rows hoist the owner
	// into the subject (Kind/Group/Namespace/Name) and leave Owner zero, so
	// the TS Issue type (which only consumes grouped output) doesn't model it.
	Owner Ref `json:"owner,omitzero"`
	// Fingerprint is an internal, stable per-cause key (NOT on the wire): it
	// feeds the ID discriminator so distinct causes on the same subject+category
	// (e.g. two different missing refs) don't collapse into one row. Empty for
	// single-cause categories, which fold by category as before.
	Fingerprint string `json:"-"`
	// RestartCount + LastTerminatedReason carry Pod crash-debugging
	// context from k8s.Problem through to issues consumers (MCP `issues`
	// tool + /api/issues + hub fleet_issues). Populated only for Pod
	// problem rows where the kubelet has recorded crash data. Together
	// they answer "is this chronic or acute?" (RestartCount) and "what
	// kind of failure?" (LastTerminatedReason: OOMKilled / Error /
	// Completed) without the agent needing a follow-up get_resource call.
	RestartCount         int32  `json:"restart_count,omitempty"`
	LastTerminatedReason string `json:"last_terminated_reason,omitempty"`
	// Affected, Members, and MembersTruncated are populated only on grouped
	// rows (GroupIssues). Affected counts the folded underlying resources by
	// kind; Members lists them (bounded by maxInlineMembers, with
	// MembersTruncated set past the cap). Empty on flat rows and on
	// single-resource grouped issues (no fan-out).
	Affected         Affected `json:"affected,omitzero"`
	Members          []Ref    `json:"members,omitempty"`
	MembersTruncated bool     `json:"members_truncated,omitempty"`
	// Cluster identity is NOT an OSS-issue concept (a Radar is one cluster). The
	// hub adds cluster_id/cluster_name in its own fleet DTO post-fan-out; cross-
	// cluster scoping is the hub's clusters=/target mechanism, not a per-issue
	// field or CEL predicate.
}
