package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultCodexSandboxSessionsDir = "/home/agent/.codex/sessions"

// CodexOptions configures the Codex provider.
type CodexOptions struct {
	Effort          string
	Env             map[string]string
	CaptureSessions *bool
	SessionStorage  *SessionDirs
	// ApprovalsReviewer "auto_review" swaps the bypass flag for an interactive
	// approval policy reviewed by Codex's own reviewer agent.
	ApprovalsReviewer string
}

type codex struct {
	model   string
	opts    CodexOptions
	storage *codexStorage
}

// Codex returns a provider for the `codex` CLI.
func Codex(model string, opts *CodexOptions) Provider {
	p := &codex{model: model}
	if opts != nil {
		p.opts = *opts
	}
	p.storage = newCodexStorage(p.opts.SessionStorage)
	return p
}

func (p *codex) Name() string           { return "codex" }
func (p *codex) Env() map[string]string { return p.opts.Env }
func (p *codex) CaptureSessions() bool {
	return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions
}
func (p *codex) SessionStorage() SessionStorage { return p.storage }

func (p *codex) BuildPrintCommand(o CommandOptions) PrintCommand {
	var base string
	switch {
	case o.ResumeSession != "" && o.ForkSession:
		// Codex separates fork from resume at the verb level (ADR 0018).
		base = "codex exec fork " + ShellEscape(o.ResumeSession)
	case o.ResumeSession != "":
		base = "codex exec resume " + ShellEscape(o.ResumeSession)
	default:
		base = "codex exec"
	}
	approvals := " --dangerously-bypass-approvals-and-sandbox"
	if p.opts.ApprovalsReviewer == "auto_review" {
		approvals = " -a on-request -s danger-full-access -c " + ShellEscape(`approvals_reviewer="auto_review"`)
	}
	effort := ""
	if p.opts.Effort != "" {
		effort = " -c " + ShellEscape(fmt.Sprintf(`model_reasoning_effort="%s"`, p.opts.Effort))
	}
	stdinArg := ""
	if o.ResumeSession != "" {
		stdinArg = " -"
	}
	prompt := o.Prompt
	return PrintCommand{Command: base + " --json" + approvals + " -m " + ShellEscape(p.model) + effort + stdinArg, Stdin: &prompt}
}

func (p *codex) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"codex", "--model", p.model}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

// extractErrorMessage reads { error: "s" }, { error: { message } },
// { error: { data: { message } } } or { message }.
func extractErrorMessage(obj map[string]any) string {
	switch e := obj["error"].(type) {
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return m
		}
		if d, ok := e["data"].(map[string]any); ok {
			if m, ok := d["message"].(string); ok {
				return m
			}
		}
	}
	if m, ok := obj["message"].(string); ok {
		return m
	}
	return ""
}

func parseJSONObject(line string) (map[string]any, bool) {
	if !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil, false
	}
	return obj, true
}

func (p *codex) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	item, _ := obj["item"].(map[string]any)
	switch obj["type"] {
	case "thread.started":
		if id, ok := obj["thread_id"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "item.completed":
		if text, ok := item["text"].(string); ok && item["type"] == "agent_message" {
			return []StreamEvent{{Type: EventText, Text: text}, {Type: EventResult, Text: text}}
		}
	case "item.started":
		if cmd, ok := item["command"].(string); ok && item["type"] == "command_execution" {
			return []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: cmd}}
		}
	case "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	case "turn.completed":
		if u, ok := parseCodexUsage(obj["usage"]); ok {
			return []StreamEvent{{Type: EventUsage, Usage: &u}}
		}
	}
	return nil
}

// parseCodexUsage maps { input_tokens, cached_input_tokens, output_tokens } to
// Usage. input_tokens is the total, so the cached part moves to
// CacheReadInputTokens and the remainder to InputTokens.
func parseCodexUsage(v any) (Usage, bool) {
	u, ok := v.(map[string]any)
	if !ok {
		return Usage{}, false
	}
	in, ok1 := u["input_tokens"].(float64)
	cached, ok2 := u["cached_input_tokens"].(float64)
	out, ok3 := u["output_tokens"].(float64)
	if !ok1 || !ok2 || !ok3 {
		return Usage{}, false
	}
	return Usage{InputTokens: int(in - cached), CacheReadInputTokens: int(cached), OutputTokens: int(out)}, true
}

// codexStorage: sessions live at YYYY/MM/DD/rollout-*-<id>.jsonl, so the host
// path is not derivable from (cwd, id). Captured paths are cached per id.
type codexStorage struct {
	hostDir    string
	sandboxDir string
	mu         sync.Mutex
	captured   map[string]string
}

func newCodexStorage(dirs *SessionDirs) *codexStorage {
	s := &codexStorage{sandboxDir: defaultCodexSandboxSessionsDir, captured: map[string]string{}}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *codexStorage) hostRoot() string {
	if s.hostDir != "" {
		return s.hostDir
	}
	return session.DefaultCodexSessionsDir()
}

func (s *codexStorage) HostSessionFilePath(_, id string) string {
	s.mu.Lock()
	p := s.captured[id]
	s.mu.Unlock()
	if p != "" {
		return p
	}
	// A fresh provider instance has no cache; the file may still be on the host.
	return session.FindCodexOnHost(id, s.hostDir).Path
}

func (s *codexStorage) ExistsOnHost(_, id string) (bool, error) {
	return session.FindCodexOnHost(id, s.hostDir).Path != "", nil
}

func (s *codexStorage) ReadHostSession(_, id string) (string, bool, error) {
	found := session.FindCodexOnHost(id, s.hostDir)
	if found.Path == "" {
		return "", false, nil
	}
	return readHostFile(found.Path)
}

func (s *codexStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindCodexOnHost(id, s.hostDir), nil
}

// LocateCodexSandbox finds rollout-*-<id>.jsonl inside the sandbox.
func LocateCodexSandbox(ctx context.Context, h sandbox.BindMountHandle, id, sessionsDir string) (session.Location, error) {
	res, err := h.Exec(ctx, fmt.Sprintf("find %s -type f -name %s -print -quit", ShellEscape(sessionsDir), ShellEscape("rollout-*-"+id+".jsonl")), sandbox.ExecOptions{})
	if err != nil {
		return session.Location{}, err
	}
	p := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	if res.ExitCode != 0 || p == "" {
		return session.Location{}, fmt.Errorf("session %s not found in %s", id, sessionsDir)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(p, sessionsDir), "/")
	return session.Location{Path: p, RelativePath: rel}, nil
}

func (s *codexStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	loc, err := LocateCodexSandbox(ctx, t.Handle, t.SessionID, s.sandboxDir)
	if err != nil {
		return err
	}
	jsonl, err := readSandboxFile(ctx, t.Handle, loc.Path)
	if err != nil {
		return err
	}
	target := filepath.Join(s.hostRoot(), filepath.FromSlash(loc.RelativePath))
	if err := writeHostFile(target, session.TransferCodex(jsonl, t.SandboxCwd, t.HostCwd)); err != nil {
		return err
	}
	s.mu.Lock()
	s.captured[t.SessionID] = target
	s.mu.Unlock()
	return nil
}

func (s *codexStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	loc, err := session.LocateCodexOnHost(t.SessionID, s.hostDir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(loc.Path)
	if err != nil {
		return err
	}
	target := path.Join(s.sandboxDir, filepath.ToSlash(loc.RelativePath))
	return writeSandboxFile(ctx, t.Handle, target, session.TransferCodex(string(b), t.HostCwd, t.SandboxCwd))
}
