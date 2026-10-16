// Package docker runs the agent in a Docker container that bind-mounts the worktree.
package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/e11en/agentgopher/internal/container"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the Docker provider.
type Options struct {
	// ImageName defaults to agentgopher:<repo dir name>.
	ImageName string
	// ContainerUID and ContainerGID must match the UID and GID baked into the
	// image. Zero means the host UID and GID (1000 on Windows).
	ContainerUID int
	ContainerGID int
	// SelinuxLabel is "z" (shared, default), "Z" (private) or "none".
	SelinuxLabel string
	// Mounts are extra host directories to bind-mount.
	Mounts []sandbox.MountConfig
	// Env is injected into the container. Merged at launch time.
	Env map[string]string
	// Network, Groups and Devices map to --network, --group-add and --device.
	Network []string
	Groups  []string
	Devices []string
	// MaxOutputTailChars bounds streamed exec output. Default: stream.MaxTailChars.
	MaxOutputTailChars int
	// CPUs maps to --cpus. Zero leaves the container unconstrained.
	CPUs float64
}

const binary = "docker"

// New returns a Docker provider.
func New(opts *Options) sandbox.BindMountProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
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

func hostID(configured int, get func() int) int {
	if configured != 0 {
		return configured
	}
	if runtime.GOOS == "windows" {
		return 1000
	}
	return get()
}

func containerName() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "agentgopher-" + hex.EncodeToString(b)
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
	uid := hostID(o.ContainerUID, os.Getuid)
	gid := hostID(o.ContainerGID, os.Getgid)

	user, err := container.Run(ctx, binary, "image", "inspect", image, "--format", "{{.Config.User}}")
	if err != nil {
		return nil, fmt.Errorf("image '%s' not found locally. Build it first with 'agentgopher docker build-image'", image)
	}
	if err := CheckImageUID(user, uid, image); err != nil {
		return nil, err
	}

	name := containerName()
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	env["HOME"] = mount.SandboxHomedir
	userSpec := fmt.Sprintf("%d:%d", uid, gid)
	if _, err := container.Run(ctx, binary, RunArgs(name, image, workdir, userSpec, env, append(append([]sandbox.Mount{}, c.Mounts...), userMounts...), o)...); err != nil {
		return nil, err
	}
	for _, dir := range parentDirs {
		if _, err := container.Run(ctx, binary, "exec", "--user", "0:0", name, "sh", "-c", `mkdir -p "$1" && chown "$2" "$1"`, "sh", dir, userSpec); err != nil {
			_, _ = container.Run(ctx, binary, "rm", "-f", name)
			return nil, fmt.Errorf("failed to create parent directory '%s' in container: %w", dir, err)
		}
	}
	return container.NewHandle(binary, name, workdir, o.MaxOutputTailChars), nil
}

// RunArgs builds the `docker run` argument list.
func RunArgs(name, image, workdir, user string, env map[string]string, mounts []sandbox.Mount, o Options) []string {
	label := o.SelinuxLabel
	switch label {
	case "":
		label = "z"
	case "none":
		label = ""
	}
	args := []string{"run", "-d", "--name", name}
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
	if workdir != "" {
		args = append(args, "-w", workdir)
	}
	if user != "" {
		args = append(args, "--user", user)
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
	return append(args, image)
}

// CheckImageUID compares the image's numeric USER with the expected UID.
// Images without USER or with a named user skip the check.
func CheckImageUID(user string, expected int, image string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil
	}
	uid, err := strconv.Atoi(strings.SplitN(user, ":", 2)[0])
	if err != nil {
		return nil
	}
	if uid != expected {
		return fmt.Errorf("UID mismatch: image '%s' was built with UID %d, but the expected UID is %d. Rebuild the image with 'agentgopher docker build-image', or pass containerUid: %d to docker.New to match the image", image, uid, expected, uid)
	}
	return nil
}
