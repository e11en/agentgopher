package session

import (
	"encoding/json"
	"strings"
)

// rewriteCwd replaces top-level "cwd" and session_meta.payload.cwd fields that
// equal fromCwd. A line that is not valid JSON (a torn final line) is kept
// verbatim so the rest of the session survives.
func rewriteCwd(jsonl, fromCwd, toCwd string) string {
	if jsonl == "" {
		return ""
	}
	lines := strings.Split(jsonl, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		changed := false
		if cwd, ok := entry["cwd"].(string); ok && cwd == fromCwd {
			entry["cwd"] = toCwd
			changed = true
		}
		if entry["type"] == "session_meta" {
			if payload, ok := entry["payload"].(map[string]any); ok {
				if cwd, ok := payload["cwd"].(string); ok && cwd == fromCwd {
					payload["cwd"] = toCwd
					changed = true
				}
			}
		}
		if changed {
			b, _ := json.Marshal(entry)
			lines[i] = string(b)
		}
	}
	return strings.Join(lines, "\n")
}

// TransferClaude rewrites cwd fields of a Claude Code session JSONL.
func TransferClaude(jsonl, fromCwd, toCwd string) string { return rewriteCwd(jsonl, fromCwd, toCwd) }

// TransferCodex rewrites cwd fields of a Codex rollout JSONL.
func TransferCodex(jsonl, fromCwd, toCwd string) string { return rewriteCwd(jsonl, fromCwd, toCwd) }

// TransferPi rewrites the cwd of the "session" header entry only. Pi refuses
// to load a session whose cwd does not exist, so this rewrite is load-bearing.
func TransferPi(jsonl, fromCwd, toCwd string) string {
	if jsonl == "" {
		return ""
	}
	lines := strings.Split(jsonl, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if cwd, ok := entry["cwd"].(string); entry["type"] == "session" && ok && cwd == fromCwd {
			entry["cwd"] = toCwd
			b, _ := json.Marshal(entry)
			lines[i] = string(b)
		}
	}
	return strings.Join(lines, "\n")
}
