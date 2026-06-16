package cmd

import (
	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/config"
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
	return dialect.BuildOptions{
		TargetDialect:      cfg.DDL.TargetDialect,
		SchemaMapping:      cfg.DDL.SchemaMapping,
		IncludeComments:    cfg.DDL.IncludeComments,
		IncludeIfNotExists: cfg.DDL.IncludeIfNotExists,
		AddRowIDColumn:     cfg.DDL.AddRowIDColumn,
		IdentityToSerial:   cfg.DDL.IdentityToSerial,
		SkipPartitions:     !cfg.DDL.Partition.Migrate,
		NoQuoteIdentifiers: cfg.DDL.NoQuoteIdentifiers,
	}
}
