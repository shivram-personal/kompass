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
	var tokens int
	diag := parseStream(strings.NewReader(stream), func(ev StreamEvent) {
		switch ev.Type {
		case "token":
			tokens++
		case "step":
			if ev.Step != nil && ev.Step.Status == "running" {
				running = true
				if ev.Step.Tool != "diagnose" {
					t.Errorf("tool prefix not stripped: %q", ev.Step.Tool)
				}
			}
			if ev.Step != nil && ev.Step.Status == "done" {
				done = true
			}
		}
	})
	if !running || !done {
		t.Errorf("expected running+done steps; running=%v done=%v", running, done)
	}
	if tokens < 1 {
		t.Errorf("expected thinking token, got %d", tokens)
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
	for _, a := range DetectAgents(context.Background()) {
		if !known[a.Name] {
			t.Errorf("detected unknown agent name %q (would mean we ran an unexpected binary)", a.Name)
		}
	}
}
