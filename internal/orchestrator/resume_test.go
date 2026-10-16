package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// storageProvider wraps fakeProvider with a scripted SessionStorage.
type storageProvider struct {
	fakeProvider
	st *fakeStorage
}

func (p storageProvider) SessionStorage() agent.SessionStorage { return p.st }
func (p storageProvider) CaptureSessions() bool                { return true }
func (p storageProvider) ParseSessionUsage(content string) (agent.Usage, bool) {
	return agent.ClaudeCode("m", nil).(agent.SessionUsageParser).ParseSessionUsage(content)
}

type fakeStorage struct {
	hostFiles map[string]string // id -> path
	captured  []string
	resumed   []string
	hostDir   string
}

func (s *fakeStorage) CaptureToHost(_ context.Context, t agent.SessionTransfer) error {
	s.captured = append(s.captured, t.SessionID)
	p := filepath.Join(s.hostDir, t.SessionID+".jsonl")
	os.WriteFile(p, []byte(`{"type":"assistant","message":{"usage":{"input_tokens":5,"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"output_tokens":3}}}`), 0o644)
	s.hostFiles[t.SessionID] = p
	return nil
}
func (s *fakeStorage) ResumeIntoSandbox(_ context.Context, t agent.SessionTransfer) error {
	s.resumed = append(s.resumed, t.SessionID)
	return nil
}
func (s *fakeStorage) ReadHostSession(_, id string) (string, bool, error) {
	p, ok := s.hostFiles[id]
	if !ok {
		return "", false, nil
	}
	b, err := os.ReadFile(p)
	return string(b), true, err
}
func (s *fakeStorage) ExistsOnHost(_, id string) (bool, error) {
	_, ok := s.hostFiles[id]
	return ok, nil
}
func (s *fakeStorage) HostSessionFilePath(_, id string) string { return s.hostFiles[id] }
func (s *fakeStorage) FindByIDOnHost(id string) (agent.HostSessionLookup, error) {
	return agent.HostSessionLookup{Path: s.hostFiles[id], SearchedRoot: s.hostDir}, nil
}

func newStorageProvider(t *testing.T) storageProvider {
	t.Helper()
	return storageProvider{st: &fakeStorage{hostFiles: map[string]string{}, hostDir: t.TempDir()}}
}

func TestAssertResumeSessionExists(t *testing.T) {
	p := newStorageProvider(t)
	if err := AssertResumeSessionExists(fakeProvider{}, sandbox.KindBindMount, "/r", "x"); err == nil || !strings.Contains(err.Error(), "does not support ResumeSession") {
		t.Fatalf("no storage: %v", err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindBindMount, "/r", "x"); err == nil || !strings.Contains(err.Error(), `"x" not found`) {
		t.Fatalf("bind-mount missing: %v", err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindNone, "/r", "x"); err == nil || !strings.Contains(err.Error(), p.st.hostDir) {
		t.Fatalf("none missing: %v", err)
	}
	p.st.hostFiles["x"] = filepath.Join(p.st.hostDir, "x.jsonl")
	if err := AssertResumeSessionExists(p, sandbox.KindBindMount, "/r", "x"); err != nil {
		t.Fatal(err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindNone, "/r", "x"); err != nil {
		t.Fatal(err)
	}
}
