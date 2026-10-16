package vercel

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestCreateArgs(t *testing.T) {
	got := strings.Join(CreateArgs("agentgopher-1", Options{Token: "tok", Project: "p", Team: "t", VCPUs: 2, Image: "img"}, map[string]string{"A": "1"}), " ")
	want := "create --silent --non-persistent --name agentgopher-1 --timeout 1h --token tok --project p --scope t --vcpus 2 --image img --env A=1"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestExecArgs(t *testing.T) {
	got := strings.Join(ExecArgs("n", Options{Token: "tok"}, "echo hi", "/w", true), " ")
	want := "exec --token tok --workdir /w --sudo n -- sh -c echo hi"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindIsolated || p.Name() != "vercel" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("VERCEL_TOKEN") == "" {
		t.Skip("VERCEL_TOKEN not set")
	}
	h, err := New(&Options{Token: os.Getenv("VERCEL_TOKEN"), Timeout: "10m"}).Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "echo hello; exit 3", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	src := t.TempDir() + "/in.txt"
	os.WriteFile(src, []byte("ping"), 0o644)
	if err := h.CopyIn(context.Background(), src, RepoPath+"/in.txt"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/out.txt"
	if err := h.CopyFileOut(context.Background(), RepoPath+"/in.txt", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ping" {
		t.Fatalf("got %q", b)
	}
}
