package server

import "testing"

// TestNamespaceAllowedForManagedResources pins the nil-vs-populated
// semantics of the allowed-namespace check used by
// handleGitOpsManagedResources. The handler reads
// `allowedNamespaces := s.getUserNamespaces(r, nil)` which returns:
//
//   nil  → admin / auth disabled — every namespace passes
//   []   → fail-closed (handled by the noNamespaceAccess() check
//          UPSTREAM of this filter, which returns an empty tree + warning
//          before any list call fires)
//   list → restrict to these namespaces
//
// The original handler implementation built `allowedSet` from the slice
// unconditionally — meaning `nil` was converted to an empty map, the
// "ns in set" check then rejected EVERY namespaced resource, and the
// admin/auth-off path returned 0 declared resources against a cluster
// that obviously had them. This test pins the fix.
func TestNamespaceAllowedForManagedResources(t *testing.T) {
	t.Run("nil set: every namespace passes (admin or auth-off)", func(t *testing.T) {
		if !namespaceAllowedForManagedResources("prod", nil) {
			t.Errorf("nil set should allow ns=prod but didn't")
		}
		if !namespaceAllowedForManagedResources("staging", nil) {
			t.Errorf("nil set should allow ns=staging but didn't")
		}
		if !namespaceAllowedForManagedResources("", nil) {
			t.Errorf("nil set should allow cluster-scoped (ns='') but didn't")
		}
	})

	t.Run("populated set: only members allowed; non-members denied", func(t *testing.T) {
		set := map[string]struct{}{"prod": {}}
		if !namespaceAllowedForManagedResources("prod", set) {
			t.Errorf("populated set with ns=prod should allow prod resource")
		}
		if namespaceAllowedForManagedResources("staging", set) {
			t.Errorf("populated set with ns=prod must NOT allow staging resource")
		}
	})

	t.Run("cluster-scoped resources pass any populated set", func(t *testing.T) {
		// Cluster-scoped resources (Namespace, ClusterRole, ClusterRoleBinding,
		// CustomResourceDefinition) have ns="" and must pass any allowedSet
		// regardless of contents — mirrors canAccessGitOpsRef's existing
		// behavior for cluster-scoped kinds (the caller has cluster-level
		// list permission OR they wouldn't have reached this code path).
		set := map[string]struct{}{"prod": {}}
		if !namespaceAllowedForManagedResources("", set) {
			t.Errorf("cluster-scoped resource (ns='') should pass any populated set")
		}
	})

	t.Run("empty set: nothing namespaced passes (defense in depth)", func(t *testing.T) {
		// This case shouldn't actually be reachable in the handler — the
		// noNamespaceAccess check at the top of handleGitOpsManagedResources
		// short-circuits with an empty tree + warning before any list runs.
		// Test it anyway because the helper has no upstream guarantee
		// and a future caller might hand an empty set.
		set := map[string]struct{}{}
		if namespaceAllowedForManagedResources("prod", set) {
			t.Errorf("empty set must deny namespaced resource")
		}
		if !namespaceAllowedForManagedResources("", set) {
			t.Errorf("empty set should still allow cluster-scoped resource")
		}
	})
}
