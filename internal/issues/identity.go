package issues

import (
	"github.com/skyhook-io/radar/pkg/resourceid"
	"github.com/skyhook-io/radar/pkg/subject"
)

// Scope aliases the shared subject.Scope — issues consumes the unified resolver's
// scope enum so issues, topology, and checks can't drift on it. The constants are
// re-exported so existing issues.Scope* references keep working.
type Scope = subject.Scope

const (
	ScopeUnknown  = subject.ScopeUnknown
	ScopeWorkload = subject.ScopeWorkload
	ScopeService  = subject.ScopeService
	ScopeIngress  = subject.ScopeIngress
	ScopePVC      = subject.ScopePVC
	ScopeNode     = subject.ScopeNode
)

// resourceKey is the canonical group|kind|namespace|name key, shared with
// pkg/resourceid so issue grouping and audit deep-links never drift apart.
func resourceKey(group, kind, namespace, name string) string {
	return resourceid.ResourceKey(group, kind, namespace, name)
}

// enrichIdentity derives the grouping subject, scope, and deterministic ID for a
// classified issue via the shared pkg/subject resolver. The subject is the
// topmost owner when one was resolved (member pods collapse under their
// workload), otherwise the resource itself — issues' owner is pre-resolved by
// the detectors, so this is a depth-0 use of the shared Subject identity. Must
// run after classifyIssue (the category is part of the ID). subject.StableID is
// byte-identical to the previous local hash, so no existing issue re-keys.
func enrichIdentity(i *Issue) {
	subjRef := Ref{Group: i.Group, Kind: i.Kind, Namespace: i.Namespace, Name: i.Name}
	if i.Owner.Kind != "" {
		subjRef = i.Owner
	}
	i.GroupingScope = subject.ScopeForKind(subjRef.Kind)
	i.ID = subject.StableID(i.GroupingScope, resourceKey(subjRef.Group, subjRef.Kind, subjRef.Namespace, subjRef.Name), string(i.Category))
}
