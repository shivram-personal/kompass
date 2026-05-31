package issues

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func rolloutWithConditions(conds []map[string]any) *unstructured.Unstructured {
	raw := make([]any, len(conds))
	for i, c := range conds {
		raw[i] = c
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"conditions": raw},
	}}
}

// TestArgoRolloutFailure pins that the Rollout reader prefers the definitive
// root cause (InvalidSpec, then ProgressDeadlineExceeded) over the generic
// Healthy=False/RolloutHealthy that FindFalseCondition surfaces first.
func TestArgoRolloutFailure(t *testing.T) {
	// The real-cluster shape: Healthy=False appears first, but InvalidSpec=True
	// is the actionable cause and must win.
	ro := rolloutWithConditions([]map[string]any{
		{"type": "Healthy", "status": "False", "reason": "RolloutHealthy"},
		{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded", "message": "deadline"},
		{"type": "InvalidSpec", "status": "True", "reason": "InvalidSpec", "message": "bad stableService"},
	})
	if r, m, ok := argoRolloutFailure(ro); !ok || r != "InvalidSpec" || m != "bad stableService" {
		t.Errorf("InvalidSpec must win: got (%q,%q,%v)", r, m, ok)
	}

	// No InvalidSpec → fall to the progress-deadline stall.
	stalled := rolloutWithConditions([]map[string]any{
		{"type": "Healthy", "status": "False", "reason": "RolloutHealthy"},
		{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded", "message": "timed out"},
	})
	if r, _, ok := argoRolloutFailure(stalled); !ok || r != "ProgressDeadlineExceeded" {
		t.Errorf("ProgressDeadlineExceeded fallback: got (%q,%v)", r, ok)
	}

	// A rollout that's merely mid-progress (no definitive failure) must NOT be
	// overridden — leave the generic reason/severity alone.
	progressing := rolloutWithConditions([]map[string]any{
		{"type": "Healthy", "status": "False", "reason": "RolloutHealthy"},
		{"type": "Progressing", "status": "True", "reason": "ReplicaSetUpdated"},
	})
	if _, _, ok := argoRolloutFailure(progressing); ok {
		t.Error("a mid-progress rollout must not be flagged as a definitive failure")
	}
}
