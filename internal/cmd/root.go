package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
)

var (
	cfgFile   string
	logLevel  string
	version   = "0.4.0"
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
	Version:       versionString(),
	SilenceErrors: true, // Execute() prints the error exactly once
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfgFile = paths.ResolveConfigPath(cfgFile)
		// Flag-parse errors have already happened by now, so usage is only
		// useful for them; runtime errors below get a clean message instead
		// of a flag dump.
		cmd.SilenceUsage = true
		return nil
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// versionString keeps --version short when built without ldflags metadata.
func versionString() string {
	if commitID == "unknown" && buildTime == "unknown" {
		return version
	}
	return fmt.Sprintf("%s (commit: %s, built: %s)", version, commitID, buildTime)
}

// loadConfigFile loads the resolved config file. Commands that can run without
// a config (offline flag-driven modes) pass lenient=true: a missing file then
// yields an empty config and flag defaults apply. A config that exists but
// fails to parse or validate is always an error.
func loadConfigFile(lenient bool) (*config.Config, error) {
	cfg, err := config.Load(cfgFile)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		if lenient {
			return &config.Config{}, nil
		}
		return nil, fmt.Errorf("config file not found: %s\n"+
			"  run 'owl-migrate init' to generate one, or pass -c <path>\n"+
			"  (resolution order: -c flag > ./migrate.yaml > $OWL_MIGRATE_CONFIG > ~/.owl/migrate/migrate.yaml)",
			cfgFile)
	}
	return nil, fmt.Errorf("load config: %w", err)
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
