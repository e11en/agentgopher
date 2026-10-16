// Package podman runs the agent in a Podman container that bind-mounts the worktree.
package podman

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"

	"github.com/e11en/agentgopher/internal/container"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the Podman provider.
type Options struct {
	// ImageName defaults to agentgopher:<repo dir name>.
	ImageName string
	// SelinuxLabel is "z" (shared, default), "Z" (private) or "none".
	SelinuxLabel string
	// KeepID maps the host user to ContainerUID with --userns=keep-id. nil means true.
	KeepID *bool
	// ContainerUID and ContainerGID must match the Containerfile. Zero means 1000.
	ContainerUID int
	ContainerGID int
	Mounts       []sandbox.MountConfig
	Env          map[string]string
	Network      []string
	Groups       []string
	Devices      []string
	// MaxOutputTailChars bounds streamed exec output. Default: stream.MaxTailChars.
	MaxOutputTailChars int
	// CPUs maps to --cpus. Zero leaves the container unconstrained.
	CPUs float64
}

const binary = "podman"

// New returns a Podman provider.
func New(opts *Options) sandbox.BindMountProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
	}
	if o.ContainerUID == 0 {
		o.ContainerUID = 1000
	}
	if o.ContainerGID == 0 {
		o.ContainerGID = 1000
	}
	return sandbox.NewBindMountProvider(sandbox.BindMountProviderConfig{
		Name:           binary,
		Env:            o.Env,
		SandboxHomedir: mount.SandboxHomedir,
		Create: func(ctx context.Context, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
			return create(ctx, o, c)
		},
	})
}

func containerName() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "agentgopher-" + hex.EncodeToString(b)
}

// MachineRunning parses `podman machine list --format json`.
func MachineRunning(listJSON string) bool {
	var machines []struct {
		Running bool `json:"Running"`
	}
	if json.Unmarshal([]byte(listJSON), &machines) != nil {
		return false
	}
	for _, m := range machines {
		if m.Running {
			return true
		}
	}
	return false
}

func create(ctx context.Context, o Options, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
	userMounts, err := mount.ResolveUserMounts(o.Mounts, mount.SandboxHomedir)
	if err != nil {
		return nil, err
	}
	parentDirs, err := mount.FileMountParents(userMounts, mount.SandboxHomedir, nil)
	if err != nil {
		return nil, err
	}
	workdir := mount.SandboxRepoDir
	for _, m := range c.Mounts {
		if m.HostPath == c.WorktreePath {
			workdir = m.SandboxPath
		}
	}
	image := o.ImageName
	if image == "" {
		image = mount.DefaultImageName(c.HostRepoPath)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		list, err := container.Run(ctx, binary, "machine", "list", "--format", "json")
		if err != nil || !MachineRunning(list) {
			return nil, fmt.Errorf("podman machine is not running. Run 'podman machine init && podman machine start' first")
		}
	}
	if _, err := container.Run(ctx, binary, "image", "inspect", image); err != nil {
		return nil, fmt.Errorf("image '%s' not found locally. Build it first with 'podman build -t %s .'", image, image)
	}
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	env["HOME"] = mount.SandboxHomedir
	name := containerName()
	if _, err := container.Run(ctx, binary, RunArgs(name, image, workdir, env, append(append([]sandbox.Mount{}, c.Mounts...), userMounts...), o)...); err != nil {
		return nil, err
	}
	userSpec := fmt.Sprintf("%d:%d", o.ContainerUID, o.ContainerGID)
	for _, dir := range parentDirs {
		if _, err := container.Run(ctx, binary, "exec", "--user", "0:0", name, "sh", "-c", `mkdir -p "$1" && chown "$2" "$1"`, "sh", dir, userSpec); err != nil {
			_, _ = container.Run(ctx, binary, "rm", "-f", name)
			return nil, fmt.Errorf("failed to create parent directory '%s' in container: %w", dir, err)
		}
	}
	return container.NewHandle(binary, name, workdir, o.MaxOutputTailChars), nil
}

// RunArgs builds the `podman run` argument list.
func RunArgs(name, image, workdir string, env map[string]string, mounts []sandbox.Mount, o Options) []string {
	uid, gid := o.ContainerUID, o.ContainerGID
	if uid == 0 {
		uid = 1000
	}
	if gid == 0 {
		gid = 1000
	}
	label := o.SelinuxLabel
	switch label {
	case "":
		label = "z"
	case "none":
		label = ""
	}
	args := []string{"run", "-d", "--name", name, "--user", fmt.Sprintf("%d:%d", uid, gid)}
	if o.KeepID == nil || *o.KeepID {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, n := range o.Network {
		args = append(args, "--network", n)
	}
	for _, g := range o.Groups {
		args = append(args, "--group-add", g)
	}
	for _, d := range o.Devices {
		args = append(args, "--device", d)
	}
	if o.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(o.CPUs, 'f', -1, 64))
	}
	args = append(args, "-w", workdir)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	for _, m := range mounts {
		args = append(args, "-v", mount.FormatVolumeMount(m, label))
	}
	return append(args, "--entrypoint", "sleep", image, "infinity")
}

// BuildImage builds imageName from containerfileDir, or from containerfile
// with the working directory as context when containerfile is set.
func BuildImage(ctx context.Context, imageName, containerfileDir, containerfile string) error {
	args := []string{"build", "-t", imageName}
	if containerfile != "" {
		abs, _ := filepath.Abs(containerfile)
		wd, _ := os.Getwd()
		args = append(args, "-f", abs, wd)
	} else {
		abs, _ := filepath.Abs(containerfileDir)
		args = append(args, abs)
	}
	_, err := container.Run(ctx, binary, args...)
	return err
}

// RemoveImage removes imageName.
func RemoveImage(ctx context.Context, imageName string) error {
	_, err := container.Run(ctx, binary, "rmi", imageName)
	return err
}
