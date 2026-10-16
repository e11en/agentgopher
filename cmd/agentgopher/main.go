// Command agentgopher scaffolds and manages agentgopher config directories.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/internal/version"
)

// subcommandFactories is filled by the init() functions of the other files in
// this package. Commands are built per root so flag values never leak between
// Execute calls (tests build many roots in one process).
var subcommandFactories []func() *cobra.Command

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agentgopher",
		Short:         "Orchestrate sandboxed coding agents",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "agentgopher %s\nUse --help to see available commands.\n", version.Version)
			return nil
		},
	}
	root.SetVersionTemplate("agentgopher version {{.Version}}\n")
	for _, factory := range subcommandFactories {
		root.AddCommand(factory())
	}
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
