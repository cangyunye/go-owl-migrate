package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func exportInsertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insert",
		Short: "Generate INSERT SQL from CSV data files (offline)",
		Long: `Reads CSV data files and generates INSERT SQL statements for the target dialect.

	This is offline mode — no database connection required.
	The CSV files are read from the data directory and INSERT SQL is written to the output directory.`,
	}

	var (
		outputDir      string
		dataDir        string
		dialect        string
		batchSize      int
		truncateBefore bool
		noQuote        bool
	)

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/insert/", "output directory for INSERT SQL files")
	cmd.Flags().StringVarP(&dataDir, "data", "d", "", "directory containing CSV data files (default: config import.source_dir, else ./output/data/)")
	cmd.Flags().StringVar(&dialect, "dialect", "", "target dialect (default: config ddl.target_dialect, else postgres)")
	cmd.Flags().IntVarP(&batchSize, "batch-size", "n", 100, "VALUES rows per INSERT statement")
	cmd.Flags().BoolVar(&truncateBefore, "truncate", false, "add TRUNCATE TABLE before INSERT")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			cfg = &config.Config{}
		}
		if dataDir == "" {
			dataDir = service.InsertDataDir(cfg)
		}

		var noQuotePtr *bool
		if cmd.Flags().Changed("no-quote-identifiers") {
			noQuotePtr = &noQuote
		}

		// 与 serve 一致：数据目录内 {schema}.{table}.csv 检测驱动，include 留空 = 全部。
		files, err := service.GenerateInsert(cfg, nil, dataDir, outputDir, service.InsertOptions{
			Dialect:   dialect,
			BatchSize: batchSize,
			Truncate:  truncateBefore,
			NoQuote:   noQuotePtr,
		})
		if err != nil {
			return fmt.Errorf("generate insert: %w", err)
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("Generated %d INSERT SQL files\n", len(files))
		return nil
	}

	return cmd
}
