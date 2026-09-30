package cmd

import (
	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

// genDDLCmd is a hidden alias for "export ddl". It remains registered for
// backward compatibility with existing scripts.
func genDDLCmd() *cobra.Command {
	exportDDL := exportDDLCmd()
	exportDDL.Use = "gen-ddl"
	exportDDL.Hidden = true
	exportDDL.Example = ""
	// Re-register aliased flags referencing the same variables is not needed
	// since we reuse the same command object — but we need to ensure the
	// RunE function works the same way.
	return exportDDL
}

// toBuildOptions remains here because it is used by both genddl.go and
// other commands (e.g., import.go, migrate_cmd.go).
func toBuildOptions(cfg *config.Config) dialect.BuildOptions {
	// 与 service.ToBuildOptions 同源（含 column_types 归一），避免两份漂移。
	return service.ToBuildOptions(cfg)
}
