package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/e11en/agentgopher/internal/mount"
)

// execCommand is replaced in tests.
var execCommand = exec.CommandContext

// BuildImageOptions configure BuildImage.
type BuildImageOptions struct {
	ImageName string
	// ContextDir is the build context and holds the Dockerfile unless File is set.
	ContextDir string
	// File is an explicit Dockerfile; the build context is then the working directory,
	// so COPY instructions resolve relative to it.
	File      string
	BuildArgs map[string]string
}

// BuildImage runs docker build.
func BuildImage(ctx context.Context, o BuildImageOptions) error {
	args := []string{"build", "-t", o.ImageName}
	keys := make([]string, 0, len(o.BuildArgs))
	for k := range o.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+o.BuildArgs[k])
	}
	if o.File != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		args = append(args, "-f", abs(o.File), cwd)
	} else {
		args = append(args, abs(o.ContextDir))
	}
	return run(ctx, args...)
}

// RemoveImage runs docker rmi.
func RemoveImage(ctx context.Context, imageName string) error {
	return run(ctx, "rmi", imageName)
}

func run(ctx context.Context, args ...string) error {
	cmd := execCommand(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("docker %s failed: %s", args[0], detail)
	}
	return nil
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// DefaultImageName derives the image name from the repo directory name.
func DefaultImageName(repoDir string) string { return mount.DefaultImageName(repoDir) }
