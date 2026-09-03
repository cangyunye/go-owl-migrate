package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
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
	cmd.Flags().StringVar(&batchMethod, "batch-method", "", "pagination method: cursor/offset")
	cmd.Flags().IntVarP(&pageSize, "page-size", "n", 0, "rows per batch (default from config)")
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

		// 与 serve 一致：默认表清单来自 cfg.Export.Tables.Include（留空 = 全部）。
		files, err := service.GenerateSelect(sm, cfg, cfg.Export.Tables.Include,
			batchMethod, pageSize, noQuotePtr, outputDir)
		if err != nil {
			return fmt.Errorf("generate select: %w", err)
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("Generated %d SELECT files\n", len(files))
		return nil
	}

	return cmd
}
