package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func recordExec(t *testing.T, fail bool) *[][]string {
	t.Helper()
	var calls [][]string
	old := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		if fail {
			return exec.CommandContext(ctx, "sh", "-c", "echo build broke 1>&2; exit 1")
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { execCommand = old })
	return &calls
}

func TestBuildImageDefaultContext(t *testing.T) {
	calls := recordExec(t, false)
	err := BuildImage(context.Background(), BuildImageOptions{ImageName: "agentgopher:x", ContextDir: "/repo/.agentgopher", BuildArgs: map[string]string{"AGENT_UID": "501", "AGENT_GID": "20"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "docker build -t agentgopher:x --build-arg AGENT_GID=20 --build-arg AGENT_UID=501 /repo/.agentgopher"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestBuildImageExplicitFileUsesCwdAsContext(t *testing.T) {
	calls := recordExec(t, false)
	if err := BuildImage(context.Background(), BuildImageOptions{ImageName: "i", ContextDir: "/repo/.agentgopher", File: "/elsewhere/Dockerfile"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join((*calls)[0], " ")
	if !strings.HasPrefix(got, "docker build -t i -f /elsewhere/Dockerfile ") || strings.HasSuffix(got, "/repo/.agentgopher") {
		t.Fatalf("got %q", got)
	}
}

func TestBuildImageSurfacesStderr(t *testing.T) {
	recordExec(t, true)
	err := BuildImage(context.Background(), BuildImageOptions{ImageName: "i", ContextDir: "."})
	if err == nil || !strings.Contains(err.Error(), "docker build failed: build broke") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveImage(t *testing.T) {
	calls := recordExec(t, false)
	if err := RemoveImage(context.Background(), "agentgopher:x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[0], " "); got != "docker rmi agentgopher:x" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultImageName(t *testing.T) {
	cases := map[string]string{"/Users/me/My Repo/": "agentgopher:my-repo", "/x/app_v2.0": "agentgopher:app_v2.0", "": "agentgopher:local"}
	for in, want := range cases {
		if got := DefaultImageName(in); got != want {
			t.Errorf("DefaultImageName(%q) = %q, want %q", in, got, want)
		}
	}
}
