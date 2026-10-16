package podman

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestRunArgsKeepIDDefault(t *testing.T) {
	got := RunArgs("c1", "img", "/w", map[string]string{"A": "1"}, []sandbox.Mount{{HostPath: "/h", SandboxPath: "/s"}}, Options{CPUs: 2})
	want := []string{"run", "-d", "--name", "c1", "--user", "1000:1000", "--userns=keep-id:uid=1000,gid=1000",
		"--cpus", "2", "-w", "/w", "-e", "A=1", "-v", "/h:/s:z", "--entrypoint", "sleep", "img", "infinity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestRunArgsRootful(t *testing.T) {
	f := false
	got := RunArgs("c1", "img", "/w", nil, nil, Options{KeepID: &f, ContainerUID: 2000, ContainerGID: 2000, SelinuxLabel: "none"})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "userns") || !strings.Contains(joined, "--user 2000:2000") {
		t.Fatalf("got %v", got)
	}
}

func TestMachineRunning(t *testing.T) {
	if !MachineRunning(`[{"Name":"m","Running":true}]`) {
		t.Fatal("running machine not detected")
	}
	if MachineRunning(`[{"Name":"m","Running":false}]`) || MachineRunning("garbage") || MachineRunning("[]") {
		t.Fatal("non-running machine reported as running")
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindBindMount || p.Name() != "podman" || p.SandboxHomedir() != "/home/agent" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

// needs daemon
func TestCreateExecCloseAgainstPodman(t *testing.T) {
	if exec.Command("podman", "info").Run() != nil {
		t.Skip("podman not available")
	}
	if exec.Command("podman", "image", "inspect", "alpine:3.20").Run() != nil && exec.Command("podman", "pull", "alpine:3.20").Run() != nil {
		t.Skip("cannot pull alpine:3.20")
	}
	wt := t.TempDir()
	f := false
	p := New(&Options{ImageName: "alpine:3.20", KeepID: &f})
	h, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{
		WorktreePath: wt, HostRepoPath: wt, Mounts: []sandbox.Mount{{HostPath: wt, SandboxPath: "/home/agent/workspace"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "/home/agent/workspace\n" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
