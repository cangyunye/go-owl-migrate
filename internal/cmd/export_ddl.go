package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
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

		d, err := registry.Get(cfg.DDL.TargetDialect)
		if err != nil {
			return err
		}

		opts := toBuildOptions(cfg)
		if cmd.Flags().Changed("no-quote-identifiers") {
			opts.NoQuoteIdentifiers = noQuote
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

		// Determine schema for sequences from config or loaded metadata
		schema := cfg.Source.Schema
		if schema == "" {
			if tables := sm.GetTables(); len(tables) > 0 {
				schema = tables[0].TableSchema
			}
		}
		seqFiles, _ := gen.GenerateSequences(sm, schema)
		for _, f := range seqFiles {
			fmt.Printf("  %s\n", f)
		}

		synFiles, _ := gen.GenerateSynonyms(sm, schema)
		for _, f := range synFiles {
			fmt.Printf("  %s\n", f)
		}

		mvFiles, _ := gen.GenerateMViews(sm)
		for _, f := range mvFiles {
			fmt.Printf("  %s\n", f)
		}

		trgFiles, _ := gen.GenerateTriggers(sm)
		for _, f := range trgFiles {
			fmt.Printf("  %s\n", f)
		}

		fnFiles, _ := gen.GenerateFunctions(sm, schema)
		for _, f := range fnFiles {
			fmt.Printf("  %s\n", f)
		}

		pkgFiles, _ := gen.GeneratePackages(sm, schema)
		for _, f := range pkgFiles {
			fmt.Printf("  %s\n", f)
		}

		pkgBodyFiles, _ := gen.GeneratePackageBodies(sm, schema)
		for _, f := range pkgBodyFiles {
			fmt.Printf("  %s\n", f)
		}

		total := len(files) + len(idxFiles) + len(viewFiles) + len(seqFiles) +
			len(synFiles) + len(mvFiles) + len(trgFiles) + len(fnFiles) +
			len(pkgFiles) + len(pkgBodyFiles)
		fmt.Printf("Generated %d DDL files\n", total)
		return nil
	}

	return cmd
}
