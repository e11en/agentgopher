package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/internal/initcmd"
	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// configDir is the per-repo config directory that init creates.
const configDir = ".agentgopher"

// Replaced in tests.
var (
	cliBuildDocker  = docker.BuildImage
	cliRemoveDocker = docker.RemoveImage
	cliBuildPodman  = podman.BuildImage
	cliRemovePodman = podman.RemoveImage
)

func init() {
	subcommandFactories = append(subcommandFactories, newDockerCmd, newPodmanCmd)
}

func requireConfigDir(cwd string) error {
	if _, err := os.Stat(filepath.Join(cwd, configDir)); err != nil {
		return errors.New("no .agentgopher/ found; run agentgopher init first")
	}
	return nil
}

func imageName(cmd *cobra.Command, cwd string) string {
	if v, _ := cmd.Flags().GetString("image-name"); v != "" {
		return v
	}
	return docker.DefaultImageName(cwd)
}

func newDockerCmd() *cobra.Command {
	root := &cobra.Command{Use: "docker", Short: "Docker sandbox commands"}
	build := &cobra.Command{
		Use:   "build-image",
		Short: "Build the sandbox image from .agentgopher/Dockerfile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			if err := requireConfigDir(cwd); err != nil {
				return err
			}
			name := imageName(cmd, cwd)
			file, _ := cmd.Flags().GetString("dockerfile")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building Docker image '%s'...\n", name)
			if err := cliBuildDocker(cmd.Context(), docker.BuildImageOptions{ImageName: name, ContextDir: filepath.Join(cwd, configDir), File: file, BuildArgs: initcmd.UIDBuildArgs()}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Build complete!")
			return nil
		},
	}
	build.Flags().String("image-name", "", "Docker image name")
	build.Flags().String("dockerfile", "", "Path to a custom Dockerfile (build context becomes the current directory)")
	remove := &cobra.Command{
		Use:   "remove-image",
		Short: "Remove the sandbox image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			name := imageName(cmd, cwd)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removing Docker image '%s'...\n", name)
			if err := cliRemoveDocker(cmd.Context(), name); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Image removed.")
			return nil
		},
	}
	remove.Flags().String("image-name", "", "Docker image name")
	root.AddCommand(build, remove)
	return root
}

func newPodmanCmd() *cobra.Command {
	root := &cobra.Command{Use: "podman", Short: "Podman sandbox commands"}
	build := &cobra.Command{
		Use:   "build-image",
		Short: "Build the sandbox image from .agentgopher/Containerfile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			if err := requireConfigDir(cwd); err != nil {
				return err
			}
			name := imageName(cmd, cwd)
			file, _ := cmd.Flags().GetString("containerfile")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building Podman image '%s'...\n", name)
			// Podman aligns UIDs at run time with --userns=keep-id, so no build args.
			if err := cliBuildPodman(cmd.Context(), podman.BuildImageOptions{ImageName: name, ContextDir: filepath.Join(cwd, configDir), File: file}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Build complete!")
			return nil
		},
	}
	build.Flags().String("image-name", "", "Podman image name")
	build.Flags().String("containerfile", "", "Path to a custom Containerfile (build context becomes the current directory)")
	remove := &cobra.Command{
		Use:   "remove-image",
		Short: "Remove the sandbox image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			name := imageName(cmd, cwd)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removing Podman image '%s'...\n", name)
			if err := cliRemovePodman(cmd.Context(), name); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Image removed.")
			return nil
		},
	}
	remove.Flags().String("image-name", "", "Podman image name")
	root.AddCommand(build, remove)
	return root
}
