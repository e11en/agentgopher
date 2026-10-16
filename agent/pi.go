package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultPiSandboxSessionsDir = "/home/agent/.pi/agent/sessions"

// PiOptions configures the pi provider.
type PiOptions struct {
	// Thinking maps to --thinking: off, minimal, low, medium, high, xhigh.
	Thinking        string
	Env             map[string]string
	CaptureSessions *bool
	SessionStorage  *SessionDirs
}

type pi struct {
	model   string
	opts    PiOptions
	storage *piStorage
}

// Pi returns a provider for the `pi` CLI.
func Pi(model string, opts *PiOptions) Provider {
	p := &pi{model: model}
	if opts != nil {
		p.opts = *opts
	}
	p.storage = newPiStorage(p.opts.SessionStorage)
	return p
}

func (p *pi) Name() string                   { return "pi" }
func (p *pi) Env() map[string]string         { return p.opts.Env }
func (p *pi) CaptureSessions() bool          { return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions }
func (p *pi) SessionStorage() SessionStorage { return p.storage }

func (p *pi) BuildPrintCommand(o CommandOptions) PrintCommand {
	cmd := "pi -p --mode json --model " + ShellEscape(p.model)
	if p.opts.Thinking != "" {
		cmd += " --thinking " + p.opts.Thinking
	}
	// --session appends to an existing session in place; pi has no fork verb.
	if o.ResumeSession != "" {
		cmd += " --session " + ShellEscape(o.ResumeSession)
	}
	prompt := o.Prompt
	return PrintCommand{Command: cmd, Stdin: &prompt}
}

func (p *pi) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"pi", "--model", p.model}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

func (p *pi) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	switch obj["type"] {
	case "session":
		if id, ok := obj["id"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "message_update":
		if evt, ok := obj["assistantMessageEvent"].(map[string]any); ok && evt["type"] == "text_delta" {
			if delta, ok := evt["delta"].(string); ok {
				return []StreamEvent{{Type: EventText, Text: delta}}
			}
		}
	case "tool_execution_start":
		name, _ := obj["toolName"].(string)
		field, ok := toolArgFields[name]
		if !ok {
			return nil
		}
		if args, ok := obj["args"].(map[string]any); ok {
			if v, ok := args[field].(string); ok {
				return []StreamEvent{{Type: EventToolCall, Name: name, Args: v}}
			}
		}
	case "agent_error", "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	case "agent_end":
		msgs, _ := obj["messages"].([]any)
		for i := len(msgs) - 1; i >= 0; i-- {
			m, _ := msgs[i].(map[string]any)
			if m["role"] != "assistant" {
				continue
			}
			var b strings.Builder
			blocks, _ := m["content"].([]any)
			for _, blk := range blocks {
				if bm, ok := blk.(map[string]any); ok && bm["type"] == "text" {
					if s, ok := bm["text"].(string); ok {
						b.WriteString(s)
					}
				}
			}
			if b.Len() > 0 {
				return []StreamEvent{{Type: EventResult, Text: b.String()}}
			}
			break
		}
	}
	return nil
}

type piStorage struct {
	hostDir    string
	sandboxDir string
}

func newPiStorage(dirs *SessionDirs) *piStorage {
	s := &piStorage{sandboxDir: defaultPiSandboxSessionsDir}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *piStorage) HostSessionFilePath(cwd, _ string) string {
	return session.PiSessionDirPath(cwd, s.hostDir)
}

func (s *piStorage) ExistsOnHost(_, id string) (bool, error) {
	return session.FindPiOnHost(id, s.hostDir).Path != "", nil
}

func (s *piStorage) ReadHostSession(_, id string) (string, bool, error) {
	found := session.FindPiOnHost(id, s.hostDir)
	if found.Path == "" {
		return "", false, nil
	}
	return readHostFile(found.Path)
}

func (s *piStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindPiOnHost(id, s.hostDir), nil
}

func locatePiSandbox(ctx context.Context, h sandbox.BindMountHandle, id, sessionsDir string) (string, error) {
	res, err := h.Exec(ctx, fmt.Sprintf("find %s -type f -name %s -print -quit", ShellEscape(sessionsDir), ShellEscape("*_"+id+".jsonl")), sandbox.ExecOptions{})
	if err != nil {
		return "", err
	}
	p := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	if res.ExitCode != 0 || p == "" {
		return "", fmt.Errorf("session %s not found in %s", id, sessionsDir)
	}
	return p, nil
}

func (s *piStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	src, err := locatePiSandbox(ctx, t.Handle, t.SessionID, s.sandboxDir)
	if err != nil {
		return err
	}
	jsonl, err := readSandboxFile(ctx, t.Handle, src)
	if err != nil {
		return err
	}
	// Pi resolves --session against the current project's encoded dir first,
	// so the file lands under the host cwd's dir, not the sandbox's.
	target := filepath.Join(session.PiSessionDirPath(t.HostCwd, s.hostDir), path.Base(src))
	return writeHostFile(target, session.TransferPi(jsonl, t.SandboxCwd, t.HostCwd))
}

func (s *piStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	loc, err := session.LocatePiOnHost(t.SessionID, s.hostDir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(loc.Path)
	if err != nil {
		return err
	}
	target := path.Join(s.sandboxDir, session.EncodePiSessionDir(t.SandboxCwd), filepath.Base(loc.Path))
	return writeSandboxFile(ctx, t.Handle, target, session.TransferPi(string(b), t.HostCwd, t.SandboxCwd))
}
