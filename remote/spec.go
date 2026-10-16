// Package remote runs agentgopher as a Kubernetes Job and follows it.
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const (
	DefaultNamespace      = "agentgopher"
	DefaultServiceAccount = "agentgopher-runner"
	DefaultBranchPrefix   = "agentgopher/"
	DefaultBaseBranch     = "main"
	DefaultScript         = ".agentgopher"
	DefaultTimeout        = 2 * time.Hour
	SecretsMountPath      = "/var/secrets"
	SecretProviderClass   = "agentgopher-secrets"
	// LabelRun carries the Job name on the Job and its Pod.
	LabelRun = "agentgopher.dev/run"
)

// RunSpec describes one remote run.
type RunSpec struct {
	RepoURL        string
	Script         string
	Branch         string
	BranchPrefix   string
	BaseBranch     string
	Namespace      string
	Image          string
	ServiceAccount string
	Timeout        time.Duration
	Env            map[string]string
}

// Defaults fills unset fields. Branch becomes BranchPrefix plus a UTC timestamp.
func (s RunSpec) Defaults(now time.Time) RunSpec {
	if s.Script == "" {
		s.Script = DefaultScript
	}
	if s.BranchPrefix == "" {
		s.BranchPrefix = DefaultBranchPrefix
	}
	if s.Branch == "" {
		s.Branch = s.BranchPrefix + now.UTC().Format("20060102-150405")
	}
	if s.BaseBranch == "" {
		s.BaseBranch = DefaultBaseBranch
	}
	if s.Namespace == "" {
		s.Namespace = DefaultNamespace
	}
	if s.ServiceAccount == "" {
		s.ServiceAccount = DefaultServiceAccount
	}
	if s.Timeout == 0 {
		s.Timeout = DefaultTimeout
	}
	return s
}

// Validate checks the required fields.
func (s RunSpec) Validate() error {
	switch {
	case s.RepoURL == "":
		return errors.New("remote: RepoURL is required")
	case s.Image == "":
		return errors.New("remote: Image is required")
	case s.Branch == "":
		return errors.New("remote: Branch is required")
	}
	return nil
}

// JobName returns a unique Job name for now.
func JobName(now time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return "agentgopher-run-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
