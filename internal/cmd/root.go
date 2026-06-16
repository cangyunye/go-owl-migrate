package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgFile   string
	logLevel  string
	version   = "0.1.0"
	commitID  = "unknown"
	buildTime = "unknown"
)

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "owl-migrate",
	Short: "Database migration tool for the owl ecosystem",
	Long: `owl-migrate reads database metadata from CSV files (or live databases)
and generates DDL, SELECT, INSERT statements and data export/import pipelines.

Supported dialects: oracle, postgres, mysql
Supported metadata sources: csv (xlsx and live database coming soon)`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commitID, buildTime),
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "./migrate.yaml", "config file path")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "override log level (debug/info/warn/error)")

	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(validateCmd())
	rootCmd.AddCommand(genDDLCmd())
	rootCmd.AddCommand(genSelectCmd())
	rootCmd.AddCommand(importCmd())
	rootCmd.AddCommand(migrateCmd())
	rootCmd.AddCommand(genInsertCmd())
	rootCmd.AddCommand(showQueryCmd())
	rootCmd.AddCommand(exportMetadataCmd())
}

// GetConfigFile returns the global config file path.
func GetConfigFile() string {
	return cfgFile
}

// GetLogLevel returns the global log level override.
func GetLogLevel() string {
	return logLevel
}
