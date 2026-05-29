// Package subject is the ONE unified resolver consumed by internal/issues AND
// pkg/topology. It folds the upstream owner-walk (internal/k8s/top_metrics.go:
// topOwnerForPod), the owner-else-self collapse (internal/issues/identity.go:
// enrichIdentity + grouping.go:foldGroup), the label-first grouping
// (pkg/topology/pod_grouping.go:determineGroupKey), and the GitOps/Helm
// precedence engine (pkg/topology/managedby.go) into a single API:
//
//	Tier-1 Subject (owner-collapsed root controller, deterministic, label-free)
//	Tier-2 AppOverlay (declared-key 8-tier precedence, provenance/confidence/conflicts)
//
// PLACEMENT NOTE: pkg/subject imports only the canonical-key helper from
// pkg/audit (ResourceKey). It does NOT import internal/* or pkg/topology (the
// plan's layering rule). The Tier-1 owner walk is parameterized over an
// OwnerResolver interface that pkg/topology satisfies via walkTopmostOwner (it
// injects topo/dp/idx); a pure single-pod walk is built in. Tier-2 takes only a
// metav1.Object's labels/annotations, so it has zero topology dependency.
package subject

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	bp "github.com/skyhook-io/radar/pkg/audit"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================= TIER 1: SUBJECT =============================

// Scope is the coarse bucket of a Subject — the UI section AND a hash input to
// the issue/check ID. Verbatim from internal/issues/identity.go:Scope (moved
// here so issues + topology share one enum). `unknown` is first-class.
type Scope string

const (
	ScopeUnknown  Scope = "unknown"
	ScopeWorkload Scope = "workload"
	ScopeService  Scope = "service"
	ScopeIngress  Scope = "ingress"
	ScopePVC      Scope = "pvc"
	ScopeNode     Scope = "node"
)

// Ref is the canonical {group,kind,namespace,name} identity. Field-identical to
// issues.Ref and topology.ResourceRef so both call sites convert with a struct
// copy. Group empty => core group.
type Ref struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Anchor names the explicit Tier-1 terminal bucket a Subject resolved into. It
// makes the two edges the plan demands first-class instead of hiding them under
// "Pod", and records the operator-CR hop.
type Anchor string

const (
	AnchorOwnerCollapsed Anchor = "owner_collapsed" // Pod->RS->Deployment, Pod->Job->CronJob
	AnchorBare           Anchor = "bare"            // owner=nil  (was pod_grouping "standalone")
	AnchorNode           Anchor = "node"            // owner=Node (static/mirror pod)
	AnchorOperatorCR     Anchor = "operator_cr"     // CNPG Cluster / Strimzi Kafka / Crossplane XR
	AnchorSelf           Anchor = "self"            // non-pod resource is its own subject (CRDs, Service, PVC…)
)

// Subject is the deterministic Tier-1 identity spine. Ref is the owner-collapsed
// root controller; Scope = ScopeForKind(Ref.Kind); Anchor records HOW it
// resolved. ~100% derivable from ownerReferences + the EdgeManages chain.
type Subject struct {
	Ref    Ref
	Scope  Scope
	Anchor Anchor
}

// Key is the canonical group|kind|namespace|name string for s.Ref — a thin
// pass-through to pkg/audit.ResourceKey (NOT reinvented). This is the
// groupingKey fed to IssueID/CheckID; sharing it keeps issue grouping and audit
// deep-links from drifting.
func (s Subject) Key() string {
	return bp.ResourceKey(s.Ref.Group, s.Ref.Kind, s.Ref.Namespace, s.Ref.Name)
}

// ScopeForKind maps a Kind to its Scope. Verbatim move of
// internal/issues/identity.go:scopeForKind — same kind set, same unknown-default
// (NOT an error). Exported so checks can reuse it.
func ScopeForKind(kind string) Scope {
	switch kind {
	case "Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
		return ScopeWorkload
	case "Service":
		return ScopeService
	case "Ingress":
		return ScopeIngress
	case "PersistentVolumeClaim":
		return ScopePVC
	case "Node":
		return ScopeNode
	}
	return ScopeUnknown
}

// OwnerResolver lets Tier-1 walk to the topmost controller WITHOUT pkg/subject
// importing pkg/topology. pkg/topology implements it by wrapping walkTopmostOwner
// (cycle-safe, O(depth) via the inverted index). internal/issues implements it
// trivially (its owner is pre-resolved on k8s.Problem) or passes the built-in
// PodOwnerResolver. Returns the immediate parent of the given ref, or
// (zero, false) when there is no further owner. names already RS-hash-stripped
// per impl.
type OwnerResolver interface {
	// ParentOf returns the next controller up the chain, or (zero, false) at root.
	ParentOf(child Ref) (parent Ref, ok bool)
}

// OperatorRootHook lets the resolver hop ONE level above an operator-generated
// workload to the operator CR (CNPG Cluster owns the StatefulSet, etc.). V1
// ships a curated allowlist impl (DefaultOperatorRoots); unknown operator CRs
// return false and degrade to the workload (raw-always).
type OperatorRootHook interface {
	// RootFor returns the operator-CR root owning `workload`, if recognized.
	RootFor(workload Ref) (root Ref, ok bool)
}

// maxOwnerWalkDepth bounds the multi-hop owner walk. Real K8s ownership chains
// are shallow (Pod->RS->Deployment is depth 2); a depth this high can only be
// reached by a corrupted/cyclic topology, and the built-in visited-set already
// stops cycles — this is belt-and-suspenders.
const maxOwnerWalkDepth = 16

// ResolveSubject is the Tier-1 entrypoint. It collapses ownerReferences to the
// root controller and classifies the terminal Anchor. It SUBSUMES:
//   - topOwnerForPod's single-hop RS->Deployment + name-strip (generalized to a
//     multi-hop walk via OwnerResolver), AND extends it with Job->CronJob,
//     owner=nil -> AnchorBare, owner=Node -> AnchorNode, operator-CR root hop.
//   - enrichIdentity / foldGroup's owner-else-self collapse (the single hop is
//     just a depth-1 walk with no further parent).
//
// Pass owners=nil for the pure "resource is its own subject" path. The
// owner-walk inputs (topo/dp/idx) are bound inside the OwnerResolver the caller
// injects, so this signature stays layering-clean.
func ResolveSubject(start Ref, podMeta metav1.Object, owners OwnerResolver, ops OperatorRootHook) Subject {
	cur := start
	anchor := AnchorSelf

	if owners != nil {
		visited := map[string]bool{refKey(cur): true}
		for i := 0; i < maxOwnerWalkDepth; i++ {
			parent, ok := owners.ParentOf(cur)
			if !ok {
				break
			}
			// owner=Node is a terminal bucket for static/mirror pods — never
			// collapse "up into" the Node as if it owned a workload.
			if parent.Kind == "Node" {
				anchor = AnchorNode
				break
			}
			if visited[refKey(parent)] {
				break
			}
			visited[refKey(parent)] = true
			cur = parent
			anchor = AnchorOwnerCollapsed
		}
	}

	// A pod with no owner (and no owner=Node terminal) is bare/standalone.
	if anchor == AnchorSelf && start.Kind == "Pod" {
		anchor = AnchorBare
	}

	// Operator-CR root hop: one level ABOVE the generated workload. Only when
	// the resolved controller is itself owned by a recognized operator CR.
	if ops != nil && anchor != AnchorNode {
		if root, ok := ops.RootFor(cur); ok {
			cur = root
			anchor = AnchorOperatorCR
		}
	}

	return Subject{
		Ref:    cur,
		Scope:  ScopeForKind(cur.Kind),
		Anchor: anchor,
	}
}

func refKey(r Ref) string {
	return bp.ResourceKey(r.Group, r.Kind, r.Namespace, r.Name)
}

// PodOwnerResolver is the built-in single-pod OwnerResolver: walks
// pod.OwnerReferences (controller==true else [0]), RS->Deployment +
// StripReplicaSetHash, Job stays Job (CronJob hop handled by the multi-hop loop
// when a topology OwnerResolver provides the Job->CronJob parent). Exact move of
// topOwnerForPod's logic so the upstream k8s walk has ONE home.
//
// It resolves a parent ONLY for the Pod itself: it returns the Pod's controller
// once, then (zero, false) for anything else, since a bare Pod's
// OwnerReferences only describe one hop. Job->CronJob and deeper chains require
// a topology-backed OwnerResolver.
type PodOwnerResolver struct{ Pod metav1.Object }

func (p PodOwnerResolver) ParentOf(child Ref) (Ref, bool) {
	if p.Pod == nil {
		return Ref{}, false
	}
	// Only the pod itself has a known parent in this single-pod resolver.
	if child.Kind != "Pod" || child.Name != p.Pod.GetName() || child.Namespace != p.Pod.GetNamespace() {
		return Ref{}, false
	}
	refs := p.Pod.GetOwnerReferences()
	pick := func(ref metav1.OwnerReference) (Ref, bool) {
		if ref.Kind == "ReplicaSet" {
			return Ref{Group: "apps", Kind: "Deployment", Namespace: p.Pod.GetNamespace(), Name: StripReplicaSetHash(ref.Name)}, true
		}
		return Ref{Group: groupFromAPIVersion(ref.APIVersion), Kind: ref.Kind, Namespace: p.Pod.GetNamespace(), Name: ref.Name}, true
	}
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller {
			return pick(ref)
		}
	}
	if len(refs) > 0 {
		return pick(refs[0])
	}
	return Ref{}, false
}

// groupFromAPIVersion extracts the API group from an ownerReference APIVersion
// ("apps/v1" -> "apps", "v1" -> ""). Pods own no native group-bearing
// references beyond apps/batch, but operator CRs (CNPG, Strimzi) carry their
// group here, which the operator-root hook keys on.
func groupFromAPIVersion(apiVersion string) string {
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		return apiVersion[:i]
	}
	return ""
}

// StripReplicaSetHash moves verbatim from top_metrics.go (idx<=0 guard preserved).
func StripReplicaSetHash(name string) string {
	idx := strings.LastIndex(name, "-")
	if idx <= 0 {
		return name
	}
	return name[:idx]
}

// ============================ DETERMINISTIC IDs ============================

// IssueID is the EXACT move of internal/issues/identity.go:issueID — sha256 of
// scope + "\x00" + groupingKey + "\x00" + category, truncated [:8], hex.
// PRESERVED BYTE-FOR-BYTE (any change re-keys every issue). category passed as
// string.
func IssueID(scope Scope, groupingKey, category string) string {
	sum := sha256.Sum256([]byte(string(scope) + "\x00" + groupingKey + "\x00" + category))
	return hex.EncodeToString(sum[:8])
}

// CheckID is the parallel derivation the plan specs (hash(scope,subject_key,
// checkID)); identical body, different discriminator.
func CheckID(scope Scope, groupingKey, checkID string) string {
	sum := sha256.Sum256([]byte(string(scope) + "\x00" + groupingKey + "\x00" + checkID))
	return hex.EncodeToString(sum[:8])
}

// ============================ UNIFIED ENTRYPOINT ============================

// Resolved is what every surface keys off: the deterministic Subject + optional
// overlay. App is nil whenever raw wins.
type Resolved struct {
	Subject Subject
	App     *AppOverlay
}

// Resolve is the single front door. Tier-1 always populates Subject (never
// fails, never needs a label); Tier-2 is best-effort. obj supplies BOTH the pod
// meta for the owner walk AND the labels/annotations for the overlay.
func Resolve(start Ref, obj metav1.Object, owners OwnerResolver, ops OperatorRootHook, allowBareApp bool) Resolved {
	return Resolved{
		Subject: ResolveSubject(start, obj, owners, ops),
		App:     ResolveOverlay(obj, allowBareApp),
	}
}
