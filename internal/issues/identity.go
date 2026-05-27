package issues

import (
	"crypto/sha256"
	"encoding/hex"

	bp "github.com/skyhook-io/radar/pkg/audit"
)

// Scope is the kind of subject an issue groups under — the coarse bucket
// for the UI section and part of the deterministic ID. `unknown` is
// first-class (CRDs, HPAs, PVs, and anything outside the core set), never
// an error.
type Scope string

const (
	ScopeUnknown  Scope = "unknown"
	ScopeWorkload Scope = "workload"
	ScopeService  Scope = "service"
	ScopeIngress  Scope = "ingress"
	ScopePVC      Scope = "pvc"
	ScopeNode     Scope = "node"
)

// scopeForKind maps a grouping-subject Kind to its scope.
func scopeForKind(kind string) Scope {
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

// resourceKey is the canonical group|kind|namespace|name key, shared with
// pkg/audit so issue grouping and audit deep-links never drift apart.
func resourceKey(group, kind, namespace, name string) string {
	return bp.ResourceKey(group, kind, namespace, name)
}

// issueID is the deterministic, cluster-local identity for an issue:
// sha256(scope, grouping key, category), truncated to 8 bytes. Pure inputs
// keep it stable across recomposes (no oscillation), and identical for
// every member row of the same subject+category group. The hub namespaces
// it by cluster_id for global uniqueness.
func issueID(scope Scope, groupingKey string, cat Category) string {
	sum := sha256.Sum256([]byte(string(scope) + "\x00" + groupingKey + "\x00" + string(cat)))
	return hex.EncodeToString(sum[:8])
}

// enrichIdentity derives the grouping subject, scope, and deterministic ID
// for a classified issue. The subject is the topmost owner when one was
// resolved (member pods collapse under their workload), otherwise the
// resource itself. Must run after classifyIssue — the category is part of
// the ID.
func enrichIdentity(i *Issue) {
	subject := Ref{Group: i.Group, Kind: i.Kind, Namespace: i.Namespace, Name: i.Name}
	if i.Owner.Kind != "" {
		subject = i.Owner
	}
	i.GroupingScope = scopeForKind(subject.Kind)
	i.ID = issueID(i.GroupingScope, resourceKey(subject.Group, subject.Kind, subject.Namespace, subject.Name), i.Category)
}
