package ai

import (
	"context"
	"io"
	"os/exec"
)

// Agent abstracts a coding CLI radar drives for AI diagnosis. Each backend knows
// how to spawn its CLI for one turn (flags, MCP wiring, env, cwd) and how to parse
// that CLI's event stream into radar's normalized StreamEvents + final Diagnosis.
// The generic run loop (process group, stdout pipe, lifecycle) lives in Diagnoser.
type Agent interface {
	// Name is the stable backend identifier ("claude", "codex").
	Name() string
	// command builds the fully-configured *exec.Cmd for one turn (bin, args, env,
	// cwd) plus a cleanup for any temp files it created.
	command(ctx context.Context, s turnSpec) (*exec.Cmd, func(), error)
	// parseStream consumes the CLI's stdout, emits normalized events, and returns
	// the assembled Diagnosis (including the resumable session id).
	parseStream(r io.Reader, onEvent func(StreamEvent)) Diagnosis
}

// turnSpec is everything an Agent needs to build one turn, independent of CLI.
type turnSpec struct {
	mcpURL       string // radar MCP endpoint (read-only or full) to point the agent at
	prompt       string // the user/turn prompt
	systemPrompt string // SRE+security framing; set only on the first turn (empty on resume)
	sessionID    string // resume target; empty means a fresh session
	apply        bool   // user-confirmed remediation turn (write tools allowed)
	maxTurns     int
}

// resolveAgent picks a backend from the CLI binary name. Codex is added in a
// follow-up (it switches on filepath.Base(bin)); until then everything resolves
// to the Claude backend.
func resolveAgent(bin string) Agent {
	return &claudeAgent{bin: bin}
}
