package cmd

import (
	"github.com/spf13/cobra"
)

// exportCmd represents the parent export command.
var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export data, DDL, or INSERT SQL",
	Long: `Unified export command with subcommands:

  export ddl      — Generate DDL (CREATE TABLE/INDEX/VIEW) from metadata
  export data     — Export data from source database to CSV/SQL/XLSX files
  export insert   — Generate INSERT SQL from CSV data files (offline)

Use -h on each subcommand for detailed help.
Run 'owl-migrate init --scenario export-ddl' to generate a config for DDL export.`,
	Example: `  owl-migrate export ddl    -c migrate.yaml -o ./output/ddl/
  owl-migrate export data   -c migrate.yaml --format csv
  owl-migrate export insert -c migrate.yaml -d ./output/data/ -o ./output/insert/`,
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.AddCommand(exportDDLCmd())
	exportCmd.AddCommand(exportDataCmd())
	exportCmd.AddCommand(exportInsertCmd())
}
