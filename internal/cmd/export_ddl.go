package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func exportDDLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ddl",
		Short: "Generate DDL from metadata (CSV or live database)",
		Long: `Reads metadata from CSV files or a live database and generates
	CREATE TABLE/INDEX/VIEW/etc DDL for the target dialect.`,
	}

	var outputDir string
	var noQuote bool
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/ddl/", "output directory for DDL files")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		var noQuotePtr *bool
		if cmd.Flags().Changed("no-quote-identifiers") {
			noQuotePtr = &noQuote
		}

		files, err := service.GenerateDDL(sm, cfg, cfg.Export.Tables.Include, noQuotePtr, outputDir)
		if err != nil {
			return fmt.Errorf("generate ddl: %w", err)
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("Generated %d DDL files\n", len(files))
		return nil
	}

	return cmd
}
