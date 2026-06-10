package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
)

func genInsertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-insert",
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
	cmd.Flags().StringVarP(&dataDir, "data", "d", "./output/data/", "directory containing CSV data files")
	cmd.Flags().StringVar(&dialect, "dialect", "postgres", "target dialect: oracle/postgres/mysql")
	cmd.Flags().IntVarP(&batchSize, "batch-size", "n", 100, "VALUES rows per INSERT statement")
	cmd.Flags().BoolVar(&truncateBefore, "truncate", false, "add TRUNCATE TABLE before INSERT")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			// Config optional for gen-insert; use flags
			cfg = &config.Config{}
			cfg.DDL.TargetDialect = dialect
		}

		// Load metadata from CSV or database
		sm, err := loadSchemaModel(cfg)
		if err != nil {
			return fmt.Errorf("load metadata: %w", err)
		}

		gen := generator.NewInsertGenerator(generator.InsertConfig{
			OutputDir:      outputDir,
			BatchSize:      batchSize,
			TruncateBefore: truncateBefore,
			Dialect:        dialect,
			NoQuoteIdentifiers: noQuote,
		})

		tables := sm.GetTables()
		files, err := gen.Generate(tables, dataDir)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Printf("Generated %d INSERT SQL files\n", len(files))
		return nil
	}

	return cmd
}
