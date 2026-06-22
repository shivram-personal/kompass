package ai

import (
	"strings"
	"testing"
)

// TestCodexParseStream_FormatPin locks the Codex `exec --json` JSONL schema we
// depend on, captured from a live MCP tool call: thread.started carries the
// resumable session id, mcp_tool_call items drive running/done steps (bare tool
// name, no prefix to strip), and the final agent_message is the report body.
func TestCodexParseStream_FormatPin(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"thread.started","thread_id":"019eef06-e99b-70f1-a25f-aba70f3ea57e"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"r0","type":"reasoning","text":"checking pods"}}`,
		`{"type":"item.started","item":{"id":"item_0","type":"mcp_tool_call","server":"radar","tool":"diagnose","arguments":{"name":"x"},"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"mcp_tool_call","server":"radar","tool":"diagnose","arguments":{"name":"x"},"result":{"content":[{"type":"text","text":"crashloop detail"}]},"status":"completed"}}`,
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_1\",\"type\":\"agent_message\",\"text\":\"bad tag.\\n\\n```json\\n{\\\"root_cause\\\":\\\"bad tag\\\"}\\n```\"}}",
		`{"type":"turn.completed","usage":{"input_tokens":41789,"output_tokens":23}}`,
	}, "\n")

	var running, done bool
	var thinking, doneResult, runningSummary string
	agent := &codexAgent{bin: "codex"}
	diag := agent.parseStream(strings.NewReader(stream), func(ev StreamEvent) {
		switch ev.Type {
		case "thinking":
			thinking += ev.Token
		case "step":
			if ev.Step == nil {
				return
			}
			switch ev.Step.Status {
			case "running":
				running = true
				runningSummary = ev.Step.Summary
				if ev.Step.Tool != "diagnose" {
					t.Errorf("unexpected tool name: %q", ev.Step.Tool)
				}
			case "done":
				done = true
				doneResult = ev.Step.Result
			}
		}
	})

	if !running || !done {
		t.Errorf("expected running+done steps; running=%v done=%v", running, done)
	}
	if thinking != "checking pods" {
		t.Errorf("expected reasoning→thinking %q, got %q", "checking pods", thinking)
	}
	if runningSummary == "" {
		t.Errorf("expected args preview on running step")
	}
	if !strings.Contains(doneResult, "crashloop detail") {
		t.Errorf("expected tool result preview on done step, got %q", doneResult)
	}
	if diag.RootCause != "bad tag" {
		t.Errorf("root cause not parsed from agent_message: %q", diag.RootCause)
	}
	if diag.SessionID != "019eef06-e99b-70f1-a25f-aba70f3ea57e" {
		t.Errorf("session id (thread_id) not captured: %q", diag.SessionID)
	}
}

// TestResolveAgentCodex pins binary-name routing to the Codex backend.
func TestResolveAgentCodex(t *testing.T) {
	cases := map[string]string{
		"codex":                   "codex",
		"/usr/local/bin/codex":    "codex",
		"Codex":                   "codex",
		"claude":                  "claude",
		"/opt/claude-code/claude": "claude",
	}
	for bin, want := range cases {
		if got := resolveAgent(bin).Name(); got != want {
			t.Errorf("resolveAgent(%q).Name() = %q, want %q", bin, got, want)
		}
	}
}
