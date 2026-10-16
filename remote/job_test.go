package remote

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var fixedNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestDefaultsAndValidate(t *testing.T) {
	s := RunSpec{RepoURL: "https://github.com/e11en/demo.git", Image: "img"}.Defaults(fixedNow)
	if s.Namespace != "agentgopher" || s.ServiceAccount != "agentgopher-runner" || s.Script != ".agentgopher" || s.BaseBranch != "main" || s.Timeout != 2*time.Hour {
		t.Fatalf("spec = %+v", s)
	}
	if s.Branch != "agentgopher/20261015-120000" {
		t.Fatalf("Branch = %q", s.Branch)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (RunSpec{Image: "img"}.Defaults(fixedNow)).Validate(); err == nil || !strings.Contains(err.Error(), "RepoURL") {
		t.Fatalf("err = %v", err)
	}
	if err := (RunSpec{RepoURL: "x"}.Defaults(fixedNow)).Validate(); err == nil || !strings.Contains(err.Error(), "Image") {
		t.Fatalf("err = %v", err)
	}
	custom := RunSpec{RepoURL: "x", Image: "i", Branch: "feature/keep"}.Defaults(fixedNow)
	if custom.Branch != "feature/keep" {
		t.Fatalf("explicit branch overwritten: %q", custom.Branch)
	}
}

func TestJobName(t *testing.T) {
	n := JobName(fixedNow)
	if !strings.HasPrefix(n, "agentgopher-run-20261015-120000-") || len(n) != len("agentgopher-run-20261015-120000-")+4 {
		t.Fatalf("name = %q", n)
	}
}

func TestBuildJobShape(t *testing.T) {
	s := RunSpec{RepoURL: "https://github.com/e11en/demo.git", Image: "img:1", Env: map[string]string{"ZZ": "1", "AA": "2"}}.Defaults(fixedNow)
	job := BuildJob(s, "agentgopher-run-x")
	if job.Name != "agentgopher-run-x" || job.Namespace != "agentgopher" || *job.Spec.BackoffLimit != 0 || *job.Spec.TTLSecondsAfterFinished != 86400 || *job.Spec.ActiveDeadlineSeconds != 7200 {
		t.Fatalf("job = %+v", job.Spec)
	}
	pt := job.Spec.Template
	if pt.Labels[LabelRun] != "agentgopher-run-x" || job.Labels[LabelRun] != "agentgopher-run-x" || pt.Spec.ServiceAccountName != "agentgopher-runner" {
		t.Fatalf("labels/sa = %v %v %q", job.Labels, pt.Labels, pt.Spec.ServiceAccountName)
	}
	c := pt.Spec.Containers[0]
	if c.Name != "runner" || c.Image != "img:1" || strings.Join(c.Command, " ") != "agentgopher remote entrypoint" {
		t.Fatalf("container = %+v", c)
	}
	env := map[string]string{}
	var order []string
	for _, e := range c.Env {
		env[e.Name] = e.Value
		order = append(order, e.Name)
	}
	if env["AGENTGOPHER_REPO_URL"] != s.RepoURL || env["AGENTGOPHER_SCRIPT"] != ".agentgopher" || env["AGENTGOPHER_BRANCH"] != s.Branch || env["AGENTGOPHER_BASE_BRANCH"] != "main" || env["AGENTGOPHER_SECRETS_DIR"] != "/var/secrets" || env["AGENTGOPHER_IMAGE"] != s.Image {
		t.Fatalf("env = %v", env)
	}
	if order[len(order)-2] != "AA" || order[len(order)-1] != "ZZ" {
		t.Fatalf("extra env not sorted: %v", order)
	}
	var csi, work bool
	for _, v := range pt.Spec.Volumes {
		if v.CSI != nil && v.CSI.Driver == "secrets-store-gke.csi.k8s.io" && v.CSI.VolumeAttributes["secretProviderClass"] == "agentgopher-secrets" {
			csi = true
		}
		if v.Name == "work" && v.EmptyDir != nil {
			work = true
		}
	}
	if !csi || !work {
		t.Fatalf("volumes = %+v", pt.Spec.Volumes)
	}
	var mounted bool
	for _, m := range c.VolumeMounts {
		if m.Name == "secrets" && m.MountPath == "/var/secrets" && m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("mounts = %+v", c.VolumeMounts)
	}
}

func TestSubmitCreatesJob(t *testing.T) {
	cs := fake.NewClientset()
	s := RunSpec{RepoURL: "https://x", Image: "img"}.Defaults(fixedNow)
	name, err := Submit(context.Background(), cs, s)
	if err != nil || !strings.HasPrefix(name, "agentgopher-run-") {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := cs.BatchV1().Jobs("agentgopher").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(context.Background(), cs, RunSpec{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestJobExampleMatchesBuilder(t *testing.T) {
	b, err := os.ReadFile("../deploy/k8s/job-example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agentgopher.dev/run", "secrets-store-gke.csi.k8s.io", "secretProviderClass: agentgopher-secrets", "AGENTGOPHER_SECRETS_DIR", "ttlSecondsAfterFinished: 86400", "serviceAccountName: agentgopher-runner"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("job-example.yaml lacks %q", want)
		}
	}
}
