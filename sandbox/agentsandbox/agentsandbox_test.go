package agentsandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	"github.com/e11en/agentgopher/sandbox"
)

func TestProviderMetadataAndDefaults(t *testing.T) {
	p := New(&Options{Env: map[string]string{"A": "1"}})
	if p.Kind() != sandbox.KindIsolated || p.Name() != "agent-sandbox" || p.Env()["A"] != "1" {
		t.Fatalf("kind=%q name=%q env=%v", p.Kind(), p.Name(), p.Env())
	}
	t.Setenv("AGENTGOPHER_WARM_POOL", "")
	o := withDefaults(Options{})
	if o.Namespace != "agentgopher" || o.WarmPool != "agentgopher-sandboxes" || o.ShutdownAfter == 0 || o.ReadyTimeout == 0 {
		t.Fatalf("defaults = %+v", o)
	}
	t.Setenv("AGENTGOPHER_WARM_POOL", "pool-b")
	if got := withDefaults(Options{}).WarmPool; got != "pool-b" {
		t.Fatalf("WarmPool = %q", got)
	}
}

func TestSDKOptionsInClusterAndEnvSorted(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	so := sdkOptions(withDefaults(Options{}), map[string]string{"B": "2", "A": "1"})
	if so.Runtime != sandboxsdk.RuntimeSandboxd || so.Connectivity != "" || !so.Quiet || so.MaxUploadSize != maxTransferSize || so.RequestTimeout == 0 {
		t.Fatalf("options = %+v", so)
	}
	if len(so.Env) != 2 || so.Env[0].Name != "A" || so.Env[1].Name != "B" || so.Env[1].Value != "2" {
		t.Fatalf("env = %+v", so.Env)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if got := sdkOptions(withDefaults(Options{}), nil).Connectivity; got != sandboxsdk.ConnectivityInClusterPodIP {
		t.Fatalf("Connectivity = %q", got)
	}
}

func TestCreateMakesRepoDirAndFailsOnError(t *testing.T) {
	f := newFakeAPI()
	p := New(&Options{Open: func(context.Context, map[string]string) (API, error) { return f, nil }})
	h, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if h.WorktreePath() != sandbox.RepoDir || f.lastCommand() != "mkdir -p '"+sandbox.RepoDir+"'" {
		t.Fatalf("path=%q cmd=%q", h.WorktreePath(), f.lastCommand())
	}
	failing := newFakeAPI()
	failing.script["mkdir"] = scripted{exitCode: 1, stderr: "ro"}
	p = New(&Options{Open: func(context.Context, map[string]string) (API, error) { return failing, nil }})
	if _, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{}); err == nil || !failing.closed {
		t.Fatalf("err=%v closed=%v", err, failing.closed)
	}
	boom := errors.New("no pool")
	p = New(&Options{Open: func(context.Context, map[string]string) (API, error) { return nil, boom }})
	if _, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecDefaultsToRepoDirAndRespectsCwdAndSudo(t *testing.T) {
	f := newFakeAPI()
	// Keyed on " ls" so the later "false" command (which contains "ls") does not match.
	f.script[" ls"] = scripted{stdout: "a\n", exitCode: 0}
	h := newTestHandle(f)
	res, err := h.Exec(context.Background(), "ls", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "a\n" || res.ExitCode != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := f.lastCommand(); got != "cd '"+sandbox.RepoDir+"' && ls" {
		t.Fatalf("command = %q", got)
	}
	f.script["false"] = scripted{exitCode: 3, stderr: "nope"}
	res, _ = h.Exec(context.Background(), "false", sandbox.ExecOptions{Cwd: "/tmp", Sudo: true})
	if res.ExitCode != 3 || res.Stderr != "nope" || f.lastCommand() != "cd '/tmp' && sudo sh -c 'false'" {
		t.Fatalf("res=%+v cmd=%q", res, f.lastCommand())
	}
}

func TestExecDeliversStdinThroughFile(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	in := "hello"
	if _, err := h.Exec(context.Background(), "cat", sandbox.ExecOptions{Stdin: &in}); err != nil {
		t.Fatal(err)
	}
	cmd := f.lastCommand()
	if !strings.Contains(cmd, "(cat) < /tmp/agentgopher-stdin-") || !strings.Contains(cmd, "rm -f /tmp/agentgopher-stdin-") {
		t.Fatalf("command = %q", cmd)
	}
	var found bool
	for p, b := range f.files {
		if strings.HasPrefix(p, "tmp/agentgopher-stdin-") && string(b) == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stdin file not written under tmp/: %v", f.files)
	}
}

func TestExecStreamsWholeLinesAndExitCode(t *testing.T) {
	f := newFakeAPI()
	f.script["work"] = scripted{stdout: "ab|c\nd|e", stderr: "warn", exitCode: 4}
	h := newTestHandle(f)
	var lines []string
	res, err := h.Exec(context.Background(), "work", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "abc" || lines[1] != "de" {
		t.Fatalf("lines = %q", lines)
	}
	if res.ExitCode != 4 || res.Stderr != "warn" || !strings.Contains(res.Stdout, "abc") {
		t.Fatalf("res = %+v", res)
	}
}

func TestExecWithoutExitEventIsStartError(t *testing.T) {
	f := newFakeAPI()
	f.script["work"] = scripted{stdout: "x", noExit: true}
	h := newTestHandle(f)
	_, err := h.Exec(context.Background(), "work", sandbox.ExecOptions{OnLine: func(string) {}})
	var se *sandbox.StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecCancelledContextIsReported(t *testing.T) {
	f := newFakeAPI()
	f.script["sleep"] = scripted{stdout: "a|b"}
	h := newTestHandle(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Exec(ctx, "sleep 9", sandbox.ExecOptions{OnLine: func(string) {}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyInFileUsesSDKPathWithoutLeadingSlash(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	src := filepath.Join(t.TempDir(), "repo.bundle")
	os.WriteFile(src, []byte("bundle"), 0o644)
	if err := h.CopyIn(context.Background(), src, "/tmp/agentgopher-x/repo.bundle"); err != nil {
		t.Fatal(err)
	}
	if string(f.files["tmp/agentgopher-x/repo.bundle"]) != "bundle" {
		t.Fatalf("files = %v", f.files)
	}
}

func TestCopyInDirectoryTarsAndExtracts(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	dir := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "index.js"), []byte("1"), 0o644)
	if err := h.CopyIn(context.Background(), dir, sandbox.RepoDir+"/node_modules"); err != nil {
		t.Fatal(err)
	}
	var tarBytes []byte
	for p, b := range f.files {
		if strings.HasPrefix(p, "tmp/agentgopher-copy-") {
			tarBytes = b
		}
	}
	hdr, err := tar.NewReader(bytes.NewReader(tarBytes)).Next()
	if err != nil || hdr.Name != "node_modules" {
		t.Fatalf("hdr = %+v, %v", hdr, err)
	}
	cmd := f.lastCommand()
	if !strings.HasPrefix(cmd, "mkdir -p '"+sandbox.RepoDir+"' && tar -xf '/tmp/agentgopher-copy-") || !strings.Contains(cmd, "-C '"+sandbox.RepoDir+"'") {
		t.Fatalf("command = %q", cmd)
	}
	f.script["tar -xf"] = scripted{exitCode: 2, stderr: "bad"}
	if err := h.CopyIn(context.Background(), dir, sandbox.RepoDir+"/node_modules"); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyFileOutReadsSDKPath(t *testing.T) {
	f := newFakeAPI()
	f.reads["tmp/agentgopher-p/0001.patch"] = []byte("patch")
	h := newTestHandle(f)
	dst := filepath.Join(t.TempDir(), "out", "0001.patch")
	if err := h.CopyFileOut(context.Background(), "/tmp/agentgopher-p/0001.patch", dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "patch" {
		t.Fatalf("content = %q", b)
	}
	if err := h.CopyFileOut(context.Background(), "/tmp/missing", dst); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

func TestCloseUsesDetachedContext(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Close(ctx); err != nil || !f.closed || f.closeErr != nil {
		t.Fatalf("err=%v closed=%v ctxErr=%v", err, f.closed, f.closeErr)
	}
}
