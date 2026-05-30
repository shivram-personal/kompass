// Package conditions holds the neutral controller-condition-state vocabulary
// shared across the platform: which condition reasons mean "still reconciling"
// (transient) vs. which look transient but are genuine stuck failures. It is a
// dependency-free leaf — package inventory (pkg/packages), the GitOps detector
// (internal/k8s), and the issues classifier (internal/issues) all depend on it
// rather than on each other, so the two interpretations can't drift.
package conditions

// transientConditionReasons is the canonical set of controller condition reasons
// that mean "still reconciling / not done yet", NOT "failed". A False
// Ready/Available condition carrying one of these is in-progress, not broken —
// this is the CRD-condition noise floor. Curated across the controller families
// Radar integrates with:
//
//   - Flux:         Progressing, DependencyNotReady, ReconciliationInProgress,
//     ChartNotReady, ArtifactFailed
//   - Argo/Crossplane: Reconciling, Creating
//   - cert-manager: Issuing, Pending
//   - generic:      InProgress, Initializing, Waiting (NOT "Unknown" — ambiguous,
//     not in-progress; it stays loud/unhealthy)
var transientConditionReasons = map[string]bool{
	"Progressing":              true,
	"DependencyNotReady":       true,
	"ReconciliationInProgress": true,
	"ChartNotReady":            true,
	"ArtifactFailed":           true,
	"Reconciling":              true,
	"Creating":                 true,
	"Issuing":                  true,
	"Pending":                  true,
	"InProgress":               true,
	"Initializing":             true,
	"Waiting":                  true,
}

// genuineFailureReason holds reasons that APPEAR in transientConditionReasons
// (the health-display path softens them to "degraded", still visible) but are
// actually persistent stuck failures the live issue queue must surface, never
// suppress: a Flux source reporting ArtifactFailed can't produce an artifact;
// ChartNotReady can't resolve a chart. The issue detectors subtract these from
// the transient set; the health badge may keep them transient.
var genuineFailureReason = map[string]bool{
	"ArtifactFailed": true,
	"ChartNotReady":  true,
}

// IsTransientConditionReason reports whether a reason denotes an in-progress /
// not-yet-settled state rather than a genuine failure. Used by the GitOps
// health mapping and (minus the genuine-failure carve-out) the issue detectors.
func IsTransientConditionReason(r string) bool {
	return transientConditionReasons[r]
}

// IsGenuineFailureReason reports whether a reason is a stuck failure that looks
// transient but must NOT be suppressed from the live issue stream.
func IsGenuineFailureReason(r string) bool {
	return genuineFailureReason[r]
}

// IsInProgressForIssues is the NARROW "still reconciling" predicate the issue
// detectors use: transient MINUS the genuine-failure reasons, so ArtifactFailed/
// ChartNotReady surface as issues instead of being softened away.
func IsInProgressForIssues(r string) bool {
	return transientConditionReasons[r] && !genuineFailureReason[r]
}
