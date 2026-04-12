// Package softwarecmd defines the `caib software` command tree.
package softwarecmd

import (
	"github.com/spf13/cobra"
)

// Options wires the software command tree to caller-owned handlers.
type Options struct {
	RunList   func(cmd *cobra.Command, args []string)
	RunShow   func(cmd *cobra.Command, args []string)
	RunBuild  func(cmd *cobra.Command, args []string)
	RunLogs   func(cmd *cobra.Command, args []string)
	RunDelete func(cmd *cobra.Command, args []string)
	RunCreate func(cmd *cobra.Command, args []string)
}

// NewSoftwareCmd creates the top-level `caib software` command with subcommands.
func NewSoftwareCmd(opts Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "software",
		Short: "Manage software builds (firmware, MCU, any toolchain)",
		Long:  "Commands for triggering, monitoring, and managing SoftwareBuild resources.\nUse 'caib software list' to see available build configurations.",
	}

	// Shared flags bound to package-level vars
	var serverURL, authToken string

	// list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available software build configurations",
		Run:   opts.RunList,
	}
	listCmd.Flags().StringVar(&serverURL, "server", "", "Build API server URL (or $CAIB_SERVER)")
	listCmd.Flags().StringVar(&authToken, "token", "", "Bearer token (or $CAIB_TOKEN)")

	// show
	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a software build",
		Args:  cobra.ExactArgs(1),
		Run:   opts.RunShow,
	}
	showCmd.Flags().StringVar(&serverURL, "server", "", "Build API server URL")
	showCmd.Flags().StringVar(&authToken, "token", "", "Bearer token")
	showCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	// run
	runCmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a software build",
		Long:  "Trigger a new build from an existing SoftwareBuild configuration.\nUse --local to build from your working directory.",
		Args:  cobra.ExactArgs(1),
		Run:   opts.RunBuild,
	}
	runCmd.Flags().StringVar(&serverURL, "server", "", "Build API server URL")
	runCmd.Flags().StringVar(&authToken, "token", "", "Bearer token")
	runCmd.Flags().String("revision", "", "Override git revision (remote mode)")
	runCmd.Flags().Bool("local", false, "Build from local workspace instead of git")
	runCmd.Flags().String("workspace", ".", "Local workspace directory (with --local)")
	runCmd.Flags().String("image", "", "Override runtime container image")
	runCmd.Flags().BoolP("follow", "f", true, "Follow build logs")
	runCmd.Flags().BoolP("wait", "w", false, "Wait for build to complete")
	runCmd.Flags().Bool("no-compliance", false, "Skip compliance (default in local mode)")

	// logs
	logsCmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Stream logs from a software build",
		Args:  cobra.ExactArgs(1),
		Run:   opts.RunLogs,
	}
	logsCmd.Flags().StringVar(&serverURL, "server", "", "Build API server URL")
	logsCmd.Flags().StringVar(&authToken, "token", "", "Bearer token")
	logsCmd.Flags().BoolP("follow", "f", true, "Follow logs")

	// delete
	deleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a software build",
		Args:  cobra.ExactArgs(1),
		Run:   opts.RunDelete,
	}
	deleteCmd.Flags().StringVar(&serverURL, "server", "", "Build API server URL")
	deleteCmd.Flags().StringVar(&authToken, "token", "", "Bearer token")

	// create (SRE command)
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a SoftwareBuild CR YAML",
		Long:  "Generate a SoftwareBuild custom resource YAML from flags.\nAlways outputs YAML (dry-run by default). Commit the output to Git.",
		Run:   opts.RunCreate,
	}
	createCmd.Flags().String("name", "", "Build name (required)")
	createCmd.Flags().String("source", "", "Git repository URL")
	createCmd.Flags().String("source-revision", "main", "Git revision")
	createCmd.Flags().String("runtime-image", "ubuntu:24.04", "Runtime container image")
	createCmd.Flags().String("fetch", "", "Fetch stage command")
	createCmd.Flags().String("prebuild", "", "Prebuild stage command")
	createCmd.Flags().String("build", "", "Build stage command (required)")
	createCmd.Flags().String("postbuild", "", "Postbuild stage command")
	createCmd.Flags().String("deploy", "", "Deploy stage command")
	createCmd.Flags().StringP("output", "o", "yaml", "Output format: yaml, json")
	_ = createCmd.MarkFlagRequired("name")
	_ = createCmd.MarkFlagRequired("build")

	cmd.AddCommand(listCmd, showCmd, runCmd, logsCmd, deleteCmd, createCmd)
	return cmd
}
