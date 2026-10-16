package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/version"
	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	// go test's stdin is a character device (/dev/null); the CLI must treat it as non-interactive.
	old := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = old })
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestRootPrintsVersion(t *testing.T) {
	version.Version = "v9.9.9"
	out, err := runCLI(t, "--version")
	if err != nil || !strings.Contains(out, "agentgopher version v9.9.9") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestRootWithoutArgsShowsHint(t *testing.T) {
	out, err := runCLI(t)
	if err != nil || !strings.Contains(out, "Use --help to see available commands.") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestInitNonInteractiveScaffolds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var built []docker.BuildImageOptions
	old := cliBuildDocker
	cliBuildDocker = func(_ context.Context, o docker.BuildImageOptions) error { built = append(built, o); return nil }
	t.Cleanup(func() { cliBuildDocker = old })

	out, err := runCLI(t, "init", "--template", "blank", "--agent", "claude-code", "--sandbox", "docker", "--issue-tracker", "windshift",
		"--answer", "WS_READY_STATUS=Ready", "--create-label", "false", "--build-image", "true", "--image-name", "custom:tag")
	if err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if len(built) != 1 || built[0].ImageName != "custom:tag" {
		t.Fatalf("built = %+v", built)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".agentgopher", "prompt.md"))
	if !strings.Contains(string(prompt), "--status 'Ready'") {
		t.Fatalf("answer not applied:\n%s", prompt)
	}
}

func TestInitMissingFlagWithoutTTY(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCLI(t, "init", "--template", "blank")
	if err == nil || !strings.Contains(err.Error(), "--agent is required in non-interactive mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitRejectsBadAnswerFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCLI(t, "init", "--answer", "novalue")
	if err == nil || !strings.Contains(err.Error(), "--answer expects KEY=VALUE") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildImageRequiresConfigDir(t *testing.T) {
	t.Chdir(t.TempDir())
	called := false
	old := cliBuildDocker
	cliBuildDocker = func(context.Context, docker.BuildImageOptions) error { called = true; return nil }
	t.Cleanup(func() { cliBuildDocker = old })
	_, err := runCLI(t, "docker", "build-image")
	if err == nil || !strings.Contains(err.Error(), "no .agentgopher/ found; run agentgopher init first") || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestBuildAndRemoveImageDefaults(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".agentgopher"), 0o755)
	t.Chdir(dir)
	var built docker.BuildImageOptions
	var removed string
	oldB, oldR, oldP := cliBuildDocker, cliRemoveDocker, cliBuildPodman
	cliBuildDocker = func(_ context.Context, o docker.BuildImageOptions) error { built = o; return nil }
	cliRemoveDocker = func(_ context.Context, name string) error { removed = name; return nil }
	var podmanBuilt podman.BuildImageOptions
	cliBuildPodman = func(_ context.Context, o podman.BuildImageOptions) error { podmanBuilt = o; return nil }
	t.Cleanup(func() { cliBuildDocker, cliRemoveDocker, cliBuildPodman = oldB, oldR, oldP })

	out, err := runCLI(t, "docker", "build-image", "--dockerfile", "Other.Dockerfile")
	if err != nil || !strings.Contains(out, "Build complete!") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if built.ImageName != docker.DefaultImageName(dir) || built.File != "Other.Dockerfile" || built.ContextDir != filepath.Join(dir, ".agentgopher") || built.BuildArgs["AGENT_UID"] == "" {
		t.Fatalf("built = %+v", built)
	}
	if _, err := runCLI(t, "docker", "remove-image", "--image-name", "x:y"); err != nil || removed != "x:y" {
		t.Fatalf("removed=%q err=%v", removed, err)
	}
	if _, err := runCLI(t, "podman", "build-image", "--containerfile", "CF"); err != nil || podmanBuilt.File != "CF" || len(podmanBuilt.BuildArgs) != 0 {
		t.Fatalf("podman = %+v err=%v", podmanBuilt, err)
	}
}
