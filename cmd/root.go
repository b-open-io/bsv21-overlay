package cmd

import (
	"github.com/b-open-io/bsv21-overlay/cmd/config"
	"github.com/b-open-io/bsv21-overlay/cmd/server"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "bsv21",
	Short: "BSV-21 overlay service",
	Long: `BSV-21 overlay service for handling BSV-21 token transactions,
providing real-time event streaming, lookups, and SPV validation.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(server.Command)
	rootCmd.AddCommand(config.Command)
}