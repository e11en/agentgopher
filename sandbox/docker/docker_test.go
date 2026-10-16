package docker

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestRunArgs(t *testing.T) {
	got := RunArgs("c1", "img", "/home/agent/workspace", "1000:1000",
		map[string]string{"A": "1"},
		[]sandbox.Mount{{HostPath: "/h", SandboxPath: "/s"}, {HostPath: "/r", SandboxPath: "/t", ReadOnly: true}},
		Options{Network: []string{"n1"}, Groups: []string{"docker", "999"}, Devices: []string{"/dev/kvm"}, CPUs: 1.5})
	want := []string{"run", "-d", "--name", "c1", "-e", "A=1", "-v", "/h:/s:z", "-v", "/r:/t:ro,z",
		"-w", "/home/agent/workspace", "--user", "1000:1000", "--network", "n1",
		"--group-add", "docker", "--group-add", "999", "--device", "/dev/kvm", "--cpus", "1.5", "img"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	got = RunArgs("c1", "img", "/w", "1:1", nil, nil, Options{SelinuxLabel: "none"})
	if strings.Contains(strings.Join(got, " "), ":z") {
		t.Fatalf("label not disabled: %v", got)
	}
}

func TestCheckImageUID(t *testing.T) {
	if err := CheckImageUID("", 1000, "img"); err != nil {
		t.Fatal("no USER must pass")
	}
	if err := CheckImageUID("agent", 1000, "img"); err != nil {
		t.Fatal("non-numeric USER must pass")
	}
	if err := CheckImageUID("1000:1000", 1000, "img"); err != nil {
		t.Fatal(err)
	}
	err := CheckImageUID("1001:1001", 1000, "img")
	if err == nil || !strings.Contains(err.Error(), "UID mismatch") || !strings.Contains(err.Error(), "containerUid: 1001") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindBindMount || p.Name() != "docker" || p.SandboxHomedir() != "/home/agent" {
		t.Fatalf("kind=%q name=%q home=%q", p.Kind(), p.Name(), p.SandboxHomedir())
	}
}

func TestCreateRejectsMissingMountHostPath(t *testing.T) {
	p := New(&Options{Mounts: []sandbox.MountConfig{{HostPath: "/definitely/missing", SandboxPath: "/x"}}})
	_, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{WorktreePath: "/w", HostRepoPath: "/r"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

// needs daemon
func TestCreateExecCloseAgainstDaemon(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker daemon not available")
	}
	if exec.Command("docker", "image", "inspect", "alpine:3.20").Run() != nil {
		if exec.Command("docker", "pull", "alpine:3.20").Run() != nil {
			t.Skip("cannot pull alpine:3.20")
		}
	}
	// Built-in images carry ENTRYPOINT ["sleep","infinity"]; give the test image the same.
	build := exec.Command("docker", "build", "-q", "-t", "agentgopher-test:alpine", "-")
	build.Stdin = strings.NewReader("FROM alpine:3.20\nENTRYPOINT [\"sleep\", \"infinity\"]\n")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build test image: %v\n%s", err, out)
	}
	wt := t.TempDir()
	p := New(&Options{ImageName: "agentgopher-test:alpine", ContainerUID: 0, ContainerGID: 0})
	h, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{
		WorktreePath: wt, HostRepoPath: wt,
		Mounts: []sandbox.Mount{{HostPath: wt, SandboxPath: "/home/agent/workspace"}},
		Env:    map[string]string{"HELLO": "world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "echo $HELLO; pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "world\n/home/agent/workspace\n" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
