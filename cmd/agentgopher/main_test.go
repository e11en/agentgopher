package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/version"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
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
