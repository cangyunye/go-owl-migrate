package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func genDDLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-ddl",
		Short: "Generate DDL from metadata (CSV or live database)",
		Long: `Reads metadata from CSV files or a live database and generates
	CREATE TABLE/INDEX/VIEW/etc DDL for the target dialect.`,
	}

	var outputDir string
	var quoteAll bool
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/ddl/", "output directory for DDL files")
	cmd.Flags().BoolVar(&quoteAll, "quote-all-identifiers", false, "force double-quote all identifiers, preserve case")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Load metadata from CSV or database
		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		// Get dialect
		d, err := registry.Get(cfg.DDL.TargetDialect)
		if err != nil {
			return err
		}

		opts := toBuildOptions(cfg)
		if cmd.Flags().Changed("quote-all-identifiers") {
			opts.QuoteAllIdentifiers = quoteAll
		}
		gen := generator.NewDDLGenerator(d, opts, outputDir)

		files, err := gen.GenerateTables(sm)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}

		idxFiles, _ := gen.GenerateIndexes(sm)
		for _, f := range idxFiles {
			fmt.Printf("  %s\n", f)
		}

		viewFiles, _ := gen.GenerateViews(sm)
		for _, f := range viewFiles {
			fmt.Printf("  %s\n", f)
		}

		fmt.Printf("Generated %d DDL files\n", len(files)+len(idxFiles)+len(viewFiles))
		return nil
	}

	return cmd
}

func toBuildOptions(cfg *config.Config) dialect.BuildOptions {
	return dialect.BuildOptions{
		TargetDialect:      cfg.DDL.TargetDialect,
		SchemaMapping:      cfg.DDL.SchemaMapping,
		IncludeComments:    cfg.DDL.IncludeComments,
		IncludeIfNotExists: cfg.DDL.IncludeIfNotExists,
		AddRowIDColumn:     cfg.DDL.AddRowIDColumn,
		IdentityToSerial:   cfg.DDL.IdentityToSerial,
		SkipPartitions:      !cfg.DDL.Partition.Migrate,
		QuoteAllIdentifiers: cfg.DDL.QuoteAllIdentifiers,
	}
}
