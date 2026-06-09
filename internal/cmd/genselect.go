package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func genSelectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-select",
		Short: "Generate paginated SELECT statements from CSV metadata",
		Long: `Reads CSV metadata and generates SELECT statements with cursor-based or offset-based pagination.

The generated SQL files contain placeholder variables ($PAGE_SIZE, $OFFSET, $LAST_*) for batch script use.`,
	}

	var (
		outputDir string
		batchMethod string
		pageSize   int
	)

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/select/", "output directory for SELECT files")
	cmd.Flags().StringVar(&batchMethod, "batch-method", "cursor", "pagination method: cursor/offset")
	cmd.Flags().IntVarP(&pageSize, "page-size", "n", 5000, "rows per batch")

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

		// Load CSV metadata
		csvDir := cfg.Metadata.CSV.Path
		if csvDir == "" {
			csvDir = "./metadata/"
		}
		sm, err := loadCSVFromDir(csvDir)
		if err != nil {
			return err
		}

		// Get dialect for quoting
		d, err := registry.Get(cfg.DDL.TargetDialect)
		if err != nil {
			return err
		}

		gen := generator.NewSelectGenerator(batchMethod, pageSize, outputDir, d.Quote)

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

func loadCSVFromDir(csvDir string) (*md.SchemaModel, error) {
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
