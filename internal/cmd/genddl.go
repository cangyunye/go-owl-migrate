package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func genDDLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-ddl",
		Short: "Generate DDL from CSV metadata",
		Long: `Reads CSV metadata and generates CREATE TABLE/INDEX/VIEW/etc DDL for the target dialect.

Note: CSV metadata only supports data migration. Structure migration requires live database connection as metadata source.`,
	}

	var outputDir string
	cmd.Flags().StringVarP(&outputDir, "output", "o", "./output/ddl/", "output directory for DDL files")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Load CSV metadata
		csvDir := cfg.Metadata.CSV.Path
		if csvDir == "" {
			csvDir = "./metadata/"
		}
		sm, err := loadCSVModel(csvDir)
		if err != nil {
			return err
		}

		// Get dialect
		d, err := registry.Get(cfg.DDL.TargetDialect)
		if err != nil {
			return err
		}

		opts := toBuildOptions(cfg)
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
		SkipPartitions:     !cfg.DDL.Partition.Migrate,
	}
}

func loadCSVModel(csvDir string) (*md.SchemaModel, error) {
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
