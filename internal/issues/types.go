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
// tool emit. Severity is normalized to a 3-tier vocabulary
// (critical/warning/info) so consumers don't need to translate
// between the parallel severity scales the underlying sources use.
package issues

import (
	"time"

	"github.com/skyhook-io/radar/internal/filter"
)

// CELFilter aliased so callers don't need a separate import to set
// Filters.Filter.
type CELFilter = filter.Filter

// Severity is the normalized 3-tier severity. Mapping rules:
//
//	critical = problem.critical
//	warning  = problem.<any non-critical> | CRD-condition False
//	info     = reserved (currently unused)
//
// problem severities other than "critical" all collapse to warning — see
// fromProblem. Today that's "high"/"medium", but the mapping is non-critical
// by exclusion, not by an explicit allow-list.
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
// Flat (pre-group) rows are snapshot-derived with Count = 1; GroupIssues folds
// them and sets Count to the member total. For problem / missing_ref /
// scheduling, LastSeen is the compose time and FirstSeen backs off by the
// observed problem duration; for condition rows, both timestamps are the
// condition's lastTransitionTime.
type Issue struct {
	Severity Severity `json:"severity"`
	Source   Source   `json:"source"`
	// Category is the user-facing symptom taxonomy (image_pull_failed,
	// crashloop, …), derived from Source+Kind+Reason by Classify;
	// CategoryGroup is its coarse rollup (GroupOf). Both are server-emitted
	// labels so the UI renders them without its own category→group map.
	// Distinct from Group below, which is the resource's API group.
	Category      Category `json:"category,omitempty"`
	CategoryGroup Group    `json:"category_group,omitempty"`
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
	Owner         Ref       `json:"owner,omitzero"`
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
	// Cluster is left empty here; the hub injects it when emitting
	// cross-cluster envelopes via fleet_issues.
	Cluster string `json:"cluster,omitempty"`
}

// Filters narrows a Compose call. Empty fields are unconstrained.
type Filters struct {
	Namespaces []string
	Severities []Severity
	Kinds      []string
	// Limit caps the returned slice. Zero means default (200).
	Limit int
	// Filter is an optional compiled CEL predicate evaluated against
	// each composed Issue's row bindings (source is exposed there, so a
	// power user can still slice by detection method). Compile happens in
	// the handler (and is cached); this layer just runs the program.
	Filter *CELFilter
	// CanReadClusterScoped authorizes cluster-scoped Issue rows before
	// they are returned. Handlers provide a per-user SAR-backed predicate;
	// nil preserves auth-mode=none and tests where the provider's own
	// permissions are the only gate.
	CanReadClusterScoped func(kind, group string) bool
	// Grouped folds the flat rows into the public grouped model
	// (GroupIssues) before the cap, so the limit counts issue groups, not
	// replica fan-out. The public /api/issues + MCP issues set this; flat
	// callers (summarycontext per-resource index, /api/issues?view=flat)
	// leave it false.
	Grouped bool
}

const (
	DefaultLimit = 200
	MaxLimit     = 1000
	// NoLimit disables the result cap. Pass as Filters.Limit when the
	// caller needs the full matched set (e.g. building a per-resource
	// issue index for summaryContext — capping there would silently zero
	// out counts for resources whose issues fall in the tail beyond
	// MaxLimit on large clusters). Stats.TotalMatched is reliable
	// regardless; this just turns off the post-sort slice.
	NoLimit = -1
)
