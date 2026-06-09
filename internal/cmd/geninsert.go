package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
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
	)

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/insert/", "output directory for INSERT SQL files")
	cmd.Flags().StringVarP(&dataDir, "data", "d", "./output/data/", "directory containing CSV data files")
	cmd.Flags().StringVar(&dialect, "dialect", "postgres", "target dialect: oracle/postgres/mysql")
	cmd.Flags().IntVarP(&batchSize, "batch-size", "n", 100, "VALUES rows per INSERT statement")
	cmd.Flags().BoolVar(&truncateBefore, "truncate", false, "add TRUNCATE TABLE before INSERT")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			// Config optional for gen-insert; use flags
			cfg = &config.Config{}
			cfg.DDL.TargetDialect = dialect
		}

		// Load metadata
		csvDir := cfg.Metadata.CSV.Path
		if csvDir == "" {
			csvDir = "./testdata/csv/"
		}
		sm, err := loadMetadataForGenInsert(csvDir)
		if err != nil {
			return fmt.Errorf("load metadata: %w", err)
		}

		gen := generator.NewInsertGenerator(generator.InsertConfig{
			OutputDir:      outputDir,
			BatchSize:      batchSize,
			TruncateBefore: truncateBefore,
			Dialect:        dialect,
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

func loadMetadataForGenInsert(csvDir string) (*md.SchemaModel, error) {
	loader := csvpkg.NewLoader()
	entries, err := os.ReadDir(csvDir)
	if err != nil {
		return nil, fmt.Errorf("read metadata dir %q: %w", csvDir, err)
	}
	hasTables := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		path := filepath.Join(csvDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()
		loader.AddReader(entry.Name(), f)
		if entry.Name() == "tables.csv" || entry.Name() == "Tables.csv" {
			hasTables = true
		}
	}
	if !hasTables {
		return nil, fmt.Errorf("tables.csv not found in %s", csvDir)
	}
	return loader.Load()
}
