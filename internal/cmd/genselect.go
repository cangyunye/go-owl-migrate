package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func genSelectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-select",
		Short: "Generate paginated SELECT statements from metadata",
		Long: `Reads metadata from CSV files or a live database and generates
	SELECT statements with cursor-based or offset-based pagination.`,
	}

	var (
		outputDir   string
		batchMethod string
		pageSize    int
		noQuote     bool
	)

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/select/", "output directory for SELECT files")
	cmd.Flags().StringVar(&batchMethod, "batch-method", "cursor", "pagination method: cursor/offset")
	cmd.Flags().IntVarP(&pageSize, "page-size", "n", 5000, "rows per batch")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if pageSize == 0 {
			pageSize = cfg.SelectGen.Batch.PageSize
		}
		if batchMethod == "" {
			batchMethod = cfg.SelectGen.Batch.Method
		}

		// Load metadata from CSV or database
		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return err
		}

		// Get dialect for quoting
		d, err := registry.Get(cfg.DDL.TargetDialect)
		if err != nil {
			return err
		}

		quoteFn := d.Quote
	if cmd.Flags().Changed("no-quote-identifiers") && noQuote {
		quoteFn = func(s string) string { return s }
	}
	gen := generator.NewSelectGenerator(batchMethod, pageSize, outputDir, quoteFn)

		files, err := gen.Generate(sm)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("Generated %d SELECT files\n", len(files))
		return nil
	}

	return cmd
}
