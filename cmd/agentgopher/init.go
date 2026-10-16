package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/internal/initcmd"
)

func init() {
	subcommandFactories = append(subcommandFactories, newInitCmd)
}

// isTerminal reports whether prompts can be shown. Replaced in tests.
var isTerminal = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// triBool parses a "true"/"false" flag that may be absent.
func triBool(cmd *cobra.Command, name string) (*bool, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	v, _ := cmd.Flags().GetString(name)
	switch v {
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, fmt.Errorf("--%s must be true or false", name)
}

func newInitCmd() *cobra.Command {
	var answers []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the .agentgopher/ config directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			get := func(name string) string { v, _ := f.GetString(name); return v }
			createLabel, err := triBool(cmd, "create-label")
			if err != nil {
				return err
			}
			buildImage, err := triBool(cmd, "build-image")
			if err != nil {
				return err
			}
			if yes {
				t := true
				if createLabel == nil {
					createLabel = &t
				}
				if buildImage == nil {
					buildImage = &t
				}
			}
			parsed := map[string]string{}
			for _, a := range answers {
				k, v, ok := strings.Cut(a, "=")
				if !ok || k == "" {
					return errors.New("--answer expects KEY=VALUE")
				}
				parsed[k] = v
			}
			interactive := isTerminal() && !yes
			return initcmd.Run(cmd.Context(), initcmd.Options{
				RepoDir:          cwd,
				ImageName:        get("image-name"),
				Template:         get("template"),
				Agent:            get("agent"),
				Model:            get("model"),
				Sandbox:          get("sandbox"),
				IssueTracker:     get("issue-tracker"),
				CreateLabel:      createLabel,
				BuildImage:       buildImage,
				Answers:          parsed,
				Interactive:      interactive,
				BuildDockerImage: cliBuildDocker,
				BuildPodmanImage: cliBuildPodman,
				Prompter:         initcmd.NewStdinPrompter(os.Stdin, cmd.OutOrStdout()),
				Out:              cmd.OutOrStdout(),
			})
		},
	}
	f := cmd.Flags()
	f.String("template", "", "Template to scaffold (blank, simple-loop, sequential-reviewer, parallel-planner, parallel-planner-with-review)")
	f.String("agent", "", "Agent to use (e.g. claude-code)")
	f.String("model", "", "Model for the agent; defaults to the agent's default model")
	f.String("sandbox", "", "Sandbox provider (docker, podman)")
	f.String("issue-tracker", "", "Issue tracker (github-issues, beads, custom, jira, windshift)")
	f.String("image-name", "", "Image name (default: agentgopher:<repo dir>)")
	f.String("create-label", "", "true|false: create the \"agentgopher\" GitHub label (github-issues only)")
	f.String("build-image", "", "true|false: build the sandbox image now (ignored for the custom issue tracker)")
	f.StringArrayVar(&answers, "answer", nil, "Pre-answer a tracker question as KEY=VALUE (repeatable), e.g. JIRA_JQL='project = X'")
	f.BoolVar(&yes, "yes", false, "Non-interactive: answer yes to confirmations (tracker questions take their defaults whenever there is no TTY; override with --answer)")
	return cmd
}
