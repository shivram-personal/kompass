package ai

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// AgentInfo describes a detected local agent CLI for the OSS BYO-agent picker.
type AgentInfo struct {
	Name    string `json:"name"`    // "claude"
	Path    string `json:"path"`    // absolute path from LookPath
	Version string `json:"version"` // best-effort `--version`, "" if it failed
	Present bool   `json:"present"`
}

// knownAgents are the CLI names we probe for — a FIXED list. We never exec a
// user-supplied name/path: only these literals, resolved through PATH, are run.
var knownAgents = []string{"claude", "codex", "gemini", "cursor-agent"}

// DetectAgents probes for known agent CLIs on PATH. Safe by construction: only
// the fixed knownAgents names are resolved + run, each `--version` is hard
// timeout-bounded, and we never trigger auth/network side effects (no login
// probes — just `--version`). Returns one entry per present CLI.
func DetectAgents(ctx context.Context) []AgentInfo {
	var out []AgentInfo
	for _, name := range knownAgents {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out = append(out, AgentInfo{
			Name:    name,
			Path:    path,
			Version: probeVersion(ctx, path),
			Present: true,
		})
	}
	return out
}

// probeVersion runs `<path> --version` with a hard 3s timeout. Best-effort: a
// hang or error yields "" rather than blocking the request.
func probeVersion(ctx context.Context, path string) string {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
