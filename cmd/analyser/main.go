package main

import (
	"os"

	"github.com/spf13/cobra"

	"codebase-analyser/internal/cli"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "analyser",
		Short: "Analyse Go/Rust codebases for production-safety issues",
	}
	root.AddCommand(cli.NewRunCmd())
	return root
}

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}
	// cli.Execute may report a non-zero outcome (findings met the severity
	// threshold) that isn't a Go error worth "Error: ..." noise - cobra's
	// RunE wraps that case in *cli.ExitError so we can exit with the right
	// code without printing anything extra; the report itself already
	// carries the information the user needs.
	if ec, ok := err.(interface{ ExitCode() int }); ok {
		os.Exit(ec.ExitCode())
	}
	os.Exit(1)
}
