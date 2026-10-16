package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

// BaseRef is a sandbox-owned ref pinning the last commit shipped to the host.
// `git am` rewrites SHAs on the host, so after the first sync-out the host
// HEAD is unknown inside the sandbox; the ref keeps a base that exists there.
// Absent exactly when no sync-out has shipped commits yet. See ADR 0017.
const BaseRef = "refs/agentgopher/sync-base"

func resolveBase(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) string {
	res, err := h.Exec(ctx, "git rev-parse --verify --quiet "+BaseRef, sandbox.ExecOptions{Cwd: sandboxRepoDir})
	if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != "" {
		return strings.TrimSpace(res.Stdout)
	}
	return hostHead
}

// CountCommitsToSync returns the number of sandbox commits Out would ship.
// It degrades to 0 when git fails, so a status label never breaks a run.
func CountCommitsToSync(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) (int, error) {
	base := resolveBase(ctx, h, sandboxRepoDir, hostHead)
	res, err := h.Exec(ctx, `git rev-list "`+base+`..HEAD" --count`, sandbox.ExecOptions{Cwd: sandboxRepoDir})
	if err != nil || res.ExitCode != 0 {
		return 0, nil
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if convErr != nil {
		return 0, nil
	}
	return n, nil
}

// PatchesDir holds preserved sync-out artifacts, relative to the host repo.
const PatchesDir = ".agentgopher/patches"

// outState is what Out found in the sandbox before applying anything.
type outState struct {
	base, sandboxHead string
	hasCommits        bool
	diff              string
	untracked         []string
}

func inspect(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir string) (outState, error) {
	hostHead, err := hostGit(ctx, hostWorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return outState{}, err
	}
	res, err := execOK(ctx, h, "git rev-parse HEAD", sandboxRepoDir)
	if err != nil {
		return outState{}, err
	}
	s := outState{sandboxHead: strings.TrimSpace(res.Stdout)}
	s.base = resolveBase(ctx, h, sandboxRepoDir, hostHead)
	s.hasCommits = s.base != s.sandboxHead

	if d, err := h.Exec(ctx, "git diff HEAD", sandbox.ExecOptions{Cwd: sandboxRepoDir}); err == nil && d.ExitCode == 0 && strings.TrimSpace(d.Stdout) != "" {
		s.diff = d.Stdout
	}
	if u, err := h.Exec(ctx, "git ls-files --others --exclude-standard", sandbox.ExecOptions{Cwd: sandboxRepoDir}); err == nil && u.ExitCode == 0 {
		for _, f := range strings.Split(strings.TrimSpace(u.Stdout), "\n") {
			if f != "" {
				s.untracked = append(s.untracked, f)
			}
		}
	}
	return s, nil
}

// createPatchDir makes <host>/.agentgopher/patches/YYYYMMDD-HHMMSS[-n].
func createPatchDir(hostWorktreePath string) (string, error) {
	root := filepath.Join(hostWorktreePath, PatchesDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", &Error{Message: "failed to create patch directory: " + err.Error()}
	}
	base := time.Now().Format("20060102-150405")
	name := base
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(root, name)); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &Error{Message: "failed to create patch directory: " + err.Error()}
	}
	return dir, nil
}

// savePatches runs format-patch in the sandbox and copies the non-empty
// patches into patchDir, returning their host paths in order.
func savePatches(ctx context.Context, h sandbox.IsolatedHandle, sandboxRepoDir, base, patchDir string) ([]string, error) {
	tmp, err := execOK(ctx, h, "mktemp -d -t agentgopher-patches-XXXXXX", "")
	if err != nil {
		return nil, err
	}
	sandboxPatchDir := strings.TrimSpace(tmp.Stdout)
	defer func() { _, _ = h.Exec(ctx, fmt.Sprintf(`rm -rf "%s"`, sandboxPatchDir), sandbox.ExecOptions{}) }()

	if _, err := execOK(ctx, h, fmt.Sprintf(`git format-patch "%s..HEAD" -o "%s"`, base, sandboxPatchDir), sandboxRepoDir); err != nil {
		return nil, err
	}
	ls, err := execOK(ctx, h, fmt.Sprintf(`ls -1 "%s"`, sandboxPatchDir), "")
	if err != nil {
		return nil, err
	}
	var patches []string
	for _, name := range strings.Split(strings.TrimSpace(ls.Stdout), "\n") {
		if name == "" {
			continue
		}
		hostPath := filepath.Join(patchDir, name)
		if err := h.CopyFileOut(ctx, sandboxPatchDir+"/"+name, hostPath); err != nil {
			return nil, &Error{Message: fmt.Sprintf("failed to copy patch %s: %v", name, err)}
		}
		b, err := os.ReadFile(hostPath)
		if err != nil {
			return nil, &Error{Message: fmt.Sprintf("failed to read patch %s: %v", name, err)}
		}
		// A merge commit yields a header-only patch that git am rejects.
		if strings.Contains(string(b), "diff --git") {
			patches = append(patches, hostPath)
		}
	}
	return patches, nil
}

// Out ships the sandbox's commits, uncommitted diff and untracked files to
// hostWorktreePath. Artifacts are saved under PatchesDir first, applied
// second; on an apply failure they are preserved and a recovery message is
// written to stderr. See ADR 0017 for the base ref.
func Out(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir string) error {
	s, err := inspect(ctx, h, hostWorktreePath, sandboxRepoDir)
	if err != nil {
		return err
	}
	if !s.hasCommits && s.diff == "" && len(s.untracked) == 0 {
		return nil
	}

	patchDir, err := createPatchDir(hostWorktreePath)
	if err != nil {
		return err
	}
	relPatchDir := filepath.ToSlash(filepath.Join(PatchesDir, filepath.Base(patchDir)))

	var patches []string
	if s.hasCommits {
		if patches, err = savePatches(ctx, h, sandboxRepoDir, s.base, patchDir); err != nil {
			return err
		}
	}
	if err := saveDiffAndUntracked(ctx, h, sandboxRepoDir, patchDir, s); err != nil {
		return err
	}

	var failed FailedStep
	if len(patches) > 0 {
		_, _ = gitx.Run(ctx, hostWorktreePath, "am", "--abort")
		if _, err := gitx.Run(ctx, hostWorktreePath, append([]string{"am", "--3way"}, patches...)...); err != nil {
			failed = StepCommits
		}
	}
	if failed == "" {
		failed = applyDiffAndUntracked(hostWorktreePath, patchDir, s)
	}

	// Commits that landed must not be re-emitted next run, even when a later
	// step failed. Only a failed `git am` leaves the ref where it was.
	if s.hasCommits && failed != StepCommits {
		if _, err := execOK(ctx, h, fmt.Sprintf("git update-ref %s %s", BaseRef, s.sandboxHead), sandboxRepoDir); err != nil {
			return err
		}
	}

	if failed != "" {
		fmt.Fprintln(os.Stderr, "\n"+RecoveryMessage(RecoveryInput{
			PatchDir: relPatchDir, FailedStep: failed,
			HasCommits: len(patches) > 0, HasDiff: s.diff != "", HasUntracked: len(s.untracked) > 0,
		}))
		return nil
	}
	_ = os.RemoveAll(patchDir)
	if entries, err := os.ReadDir(filepath.Join(hostWorktreePath, PatchesDir)); err == nil && len(entries) == 0 {
		_ = os.RemoveAll(filepath.Join(hostWorktreePath, PatchesDir))
	}
	return nil
}

func saveDiffAndUntracked(ctx context.Context, h sandbox.IsolatedHandle, sandboxRepoDir, patchDir string, s outState) error {
	if s.diff != "" {
		if err := os.WriteFile(filepath.Join(patchDir, "changes.patch"), []byte(s.diff), 0o644); err != nil {
			return &Error{Message: "failed to write diff patch: " + err.Error()}
		}
	}
	for _, rel := range s.untracked {
		if err := h.CopyFileOut(ctx, sandboxRepoDir+"/"+rel, filepath.Join(patchDir, "untracked", filepath.FromSlash(rel))); err != nil {
			return &Error{Message: fmt.Sprintf("failed to save untracked file %s: %v", rel, err)}
		}
	}
	return nil
}

func applyDiffAndUntracked(hostWorktreePath, patchDir string, s outState) FailedStep {
	ctx := context.Background()
	if s.diff != "" {
		if _, err := gitx.Run(ctx, hostWorktreePath, "apply", filepath.Join(patchDir, "changes.patch")); err != nil {
			return StepDiff
		}
	}
	for _, rel := range s.untracked {
		src := filepath.Join(patchDir, "untracked", filepath.FromSlash(rel))
		dst := filepath.Join(hostWorktreePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return StepUntracked
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return StepUntracked
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return StepUntracked
		}
	}
	return ""
}
