package ai

import (
	"context"
	"strings"
	"testing"
)

func TestDiagnosisFromText_ParsesJSONBlock(t *testing.T) {
	text := "The pod crashloops.\n\n```json\n" +
		`{"root_cause": "bad image tag", "remediation": ["roll back"], "confidence": 0.9}` +
		"\n```"
	d := diagnosisFromText(text)
	if d.RootCause != "bad image tag" {
		t.Errorf("root cause = %q", d.RootCause)
	}
	if len(d.Remediation) != 1 || d.Remediation[0] != "roll back" {
		t.Errorf("remediation = %v", d.Remediation)
	}
	if d.Confidence == nil || *d.Confidence != 0.9 {
		t.Errorf("confidence = %v", d.Confidence)
	}
	if strings.Contains(d.Report, "```json") {
		t.Errorf("report still has the json block: %q", d.Report)
	}
}

func TestDiagnosisFromText_RecommendedIndex(t *testing.T) {
	valid := "x\n\n```json\n" +
		`{"root_cause":"r","remediation":["a","b"],"recommended_index":2}` + "\n```"
	if d := diagnosisFromText(valid); d.RecommendedIndex == nil || *d.RecommendedIndex != 2 {
		t.Errorf("recommended_index = %v, want 2", d.RecommendedIndex)
	}
	// Out of range (and the 0 = "no safe fix" sentinel) must be dropped, so the UI
	// never points Apply at a non-existent step.
	for _, bad := range []string{"0", "3", "-1"} {
		text := "x\n\n```json\n" +
			`{"root_cause":"r","remediation":["a","b"],"recommended_index":` + bad + "}\n```"
		if d := diagnosisFromText(text); d.RecommendedIndex != nil {
			t.Errorf("recommended_index %s should be dropped, got %v", bad, *d.RecommendedIndex)
		}
	}
}

func TestApplyPrompt_BindsConfirmedFix(t *testing.T) {
	fix := "Set `spec.replicas` to `3` on Deployment `x`"
	if p := applyPrompt(fix); !strings.Contains(p, fix) {
		t.Errorf("apply prompt should embed the confirmed fix; got %q", p)
	}
	if p := applyPrompt(""); strings.Contains(p, "EXACTLY this fix") {
		t.Errorf("empty fix should use the fallback prompt; got %q", p)
	}
}

func TestDiagnosisFromText_FallsBackToText(t *testing.T) {
	d := diagnosisFromText("The deployment is unschedulable due to insufficient CPU.")
	if d.RootCause == "" || d.Report == "" {
		t.Fatalf("expected text fallback, got root=%q report=%q", d.RootCause, d.Report)
	}
}

// TestParseStream_FormatPin locks the claude stream-json schema we depend on,
// including the cost/turns fields on the terminal result event.
func TestParseStream_FormatPin(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__radar__diagnose","input":{"name":"x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"crashloop"}]}}`,
		`{"type":"result","result":"bad tag.\n\n` + "```json\\n" + `{\"root_cause\":\"bad tag\"}` + "\\n```" + `","num_turns":2,"total_cost_usd":0.42}`,
	}, "\n")

	var running, done bool
	var thinking, doneResult string
	diag := parseStream(strings.NewReader(stream), func(ev StreamEvent) {
		switch ev.Type {
		case "thinking":
			thinking += ev.Token
		case "step":
			if ev.Step != nil && ev.Step.Status == "running" {
				running = true
				if ev.Step.Tool != "diagnose" {
					t.Errorf("tool prefix not stripped: %q", ev.Step.Tool)
				}
			}
			if ev.Step != nil && ev.Step.Status == "done" {
				done = true
				doneResult = ev.Step.Result
			}
		}
	})
	if !running || !done {
		t.Errorf("expected running+done steps; running=%v done=%v", running, done)
	}
	if thinking != "hmm" {
		t.Errorf("expected thinking event %q, got %q", "hmm", thinking)
	}
	if doneResult == "" {
		t.Errorf("expected tool result preview on done step")
	}
	if diag.RootCause != "bad tag" {
		t.Errorf("root cause not parsed: %q", diag.RootCause)
	}
	if diag.CostUSD == nil || *diag.CostUSD != 0.42 || diag.Turns != 2 {
		t.Errorf("usage not parsed: cost=%v turns=%d", diag.CostUSD, diag.Turns)
	}
}

// TestReadTools_ExcludeWrites is the fail-closed guard: the read allowlist must
// never contain a Radar write tool.
func TestReadTools_ExcludeWrites(t *testing.T) {
	writes := map[string]bool{
		"apply_resource": true, "patch_resource": true, "manage_workload": true,
		"manage_cronjob": true, "manage_node": true, "manage_gitops": true,
	}
	for _, rt := range radarReadTools {
		if writes[rt] {
			t.Errorf("write tool %q must not be in the read allowlist", rt)
		}
	}
}

// TestDetectAgents_OnlyKnownNames ensures detection never reports a binary
// outside the fixed known list (we only ever exec literal known names).
func TestDetectAgents_OnlyKnownNames(t *testing.T) {
	known := map[string]bool{}
	for _, n := range knownAgents {
		known[n] = true
	}
	for _, a := range DetectAgents(context.Background(), false) {
		if !known[a.Name] {
			t.Errorf("detected unknown agent name %q (would mean we ran an unexpected binary)", a.Name)
		}
	}
}
