package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/paths"
)

var (
	cfgFile   string
	logLevel  string
	version   = "0.2.0"
	commitID  = "unknown"
	buildTime = "unknown"

	progressDB string
	jobID      string
	parentPID  int
)

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "owl-migrate",
	Short: "Database migration tool for the owl ecosystem",
	Long: `owl-migrate reads database metadata from CSV files (or live databases)
and generates DDL, SELECT, INSERT statements and data export/import pipelines.

Supported dialects: oracle, postgres, mysql
Supported metadata sources: csv, xlsx, database

Config resolution order: -c flag > ./migrate.yaml > $OWL_MIGRATE_CONFIG > ~/.owl/migrate/migrate.yaml`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commitID, buildTime),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfgFile = paths.ResolveConfigPath(cfgFile)
		return nil
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ./migrate.yaml or ~/.owl/migrate/migrate.yaml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "override log level (debug/info/warn/error)")

	rootCmd.PersistentFlags().StringVar(&progressDB, "progress-db", "", "path to shared SQLite database for progress events (worker mode)")
	rootCmd.PersistentFlags().StringVar(&jobID, "job-id", "", "job identifier for progress reporting (worker mode)")
	rootCmd.PersistentFlags().IntVar(&parentPID, "parent-pid", 0, "parent process PID for orphan detection (worker mode)")
	rootCmd.PersistentFlags().MarkHidden("progress-db")
	rootCmd.PersistentFlags().MarkHidden("job-id")
	rootCmd.PersistentFlags().MarkHidden("parent-pid")

	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(validateCmd())
	rootCmd.AddCommand(genDDLCmd())
	rootCmd.AddCommand(genSelectCmd())
	rootCmd.AddCommand(importCmd())
	rootCmd.AddCommand(migrateCmd())
	rootCmd.AddCommand(genInsertCmd())
	rootCmd.AddCommand(showQueryCmd())
	rootCmd.AddCommand(exportMetadataCmd())
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(onlineCmd())
}
