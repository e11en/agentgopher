package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// EntrypointConfig configures the in-cluster run.
type EntrypointConfig struct {
	RepoURL    string
	Script     string
	Branch     string
	BaseBranch string
	SecretsDir string
	WorkDir    string
	Stdout     io.Writer
}

// ConfigFromEnv reads the AGENTGOPHER_* variables set by BuildJob.
func ConfigFromEnv() (EntrypointConfig, error) {
	cfg := EntrypointConfig{
		RepoURL:    os.Getenv("AGENTGOPHER_REPO_URL"),
		Script:     os.Getenv("AGENTGOPHER_SCRIPT"),
		Branch:     os.Getenv("AGENTGOPHER_BRANCH"),
		BaseBranch: os.Getenv("AGENTGOPHER_BASE_BRANCH"),
		SecretsDir: os.Getenv("AGENTGOPHER_SECRETS_DIR"),
		WorkDir:    "/work/repo",
		Stdout:     os.Stdout,
	}
	if cfg.RepoURL == "" || cfg.Branch == "" || cfg.BaseBranch == "" || cfg.Script == "" {
		return cfg, errors.New("AGENTGOPHER_REPO_URL, AGENTGOPHER_SCRIPT, AGENTGOPHER_BRANCH and AGENTGOPHER_BASE_BRANCH must be set")
	}
	return cfg, nil
}

// LoadSecrets reads every regular file in dir as NAME=content (trimmed).
// A missing dir yields an empty map.
func LoadSecrets(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// The CSI driver uses a ..data symlink and ..<timestamp> dirs; skip them.
		if strings.HasPrefix(e.Name(), "..") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = strings.TrimSpace(string(b))
	}
	return out, nil
}

// Entrypoint is the Job's main: clone, run the script, push, open a PR.
func Entrypoint(ctx context.Context) error {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	return run(ctx, cfg)
}

func run(ctx context.Context, cfg EntrypointConfig) error {
	secrets, err := LoadSecrets(cfg.SecretsDir)
	if err != nil {
		return err
	}
	for k, v := range secrets {
		_ = os.Setenv(k, v)
	}
	_ = os.Setenv("AGENTGOPHER_BRANCH", cfg.Branch)
	_ = os.Setenv("AGENTGOPHER_BASE_BRANCH", cfg.BaseBranch)

	// stdout and stderr are copied by two goroutines; serialise their writes.
	log := &lockedWriter{w: cfg.Stdout}
	cmd := func(dir string, name string, args ...string) (string, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		var out bytes.Buffer
		c.Stdout = io.MultiWriter(log, &out)
		c.Stderr = log
		if err := c.Run(); err != nil {
			return out.String(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return strings.TrimSpace(out.String()), nil
	}

	if os.Getenv("GH_TOKEN") != "" {
		if _, err := cmd("", "gh", "auth", "setup-git"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.WorkDir), 0o755); err != nil {
		return err
	}
	if _, err := cmd("", "git", "clone", "--branch", cfg.BaseBranch, cfg.RepoURL, cfg.WorkDir); err != nil {
		return err
	}
	if _, err := cmd(cfg.WorkDir, "git", "config", "user.name"); err != nil {
		if _, err := cmd(cfg.WorkDir, "git", "config", "user.name", "agentgopher"); err != nil {
			return err
		}
	}
	if _, err := cmd(cfg.WorkDir, "git", "config", "user.email"); err != nil {
		if _, err := cmd(cfg.WorkDir, "git", "config", "user.email", "agentgopher@users.noreply.github.com"); err != nil {
			return err
		}
	}

	if _, err := cmd(cfg.WorkDir, "go", "run", "./"+strings.TrimPrefix(cfg.Script, "./")); err != nil {
		return err
	}

	// A script using the named strategy (AGENTGOPHER_BRANCH) has created the
	// branch in a worktree; a merge-to-head script left its commits on the
	// checked-out base branch, so the branch is created from there afterwards.
	if _, err := cmd(cfg.WorkDir, "git", "rev-parse", "--verify", "--quiet", cfg.Branch); err != nil {
		if _, err := cmd(cfg.WorkDir, "git", "checkout", "-b", cfg.Branch); err != nil {
			return err
		}
	}
	countOut, err := cmd(cfg.WorkDir, "git", "rev-list", "origin/"+cfg.BaseBranch+".."+cfg.Branch, "--count")
	if err != nil {
		return err
	}
	count, _ := strconv.Atoi(strings.TrimSpace(countOut))
	if count == 0 {
		_, _ = fmt.Fprintln(cfg.Stdout, "No commits produced, no PR")
		return nil
	}
	if _, err := cmd(cfg.WorkDir, "git", "push", "-u", "origin", cfg.Branch); err != nil {
		return err
	}
	url, err := cmd(cfg.WorkDir, "gh", "pr", "create", "--base", cfg.BaseBranch, "--head", cfg.Branch,
		"--title", "agentgopher: "+cfg.Branch, "--body", "Opened by agentgopher remote run.")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cfg.Stdout, "PR: %s\n", strings.TrimSpace(url))
	return nil
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
