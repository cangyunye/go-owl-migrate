package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate CSV metadata files",
		Long:  `Reads CSV metadata files and validates them against the schema reference model.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			csvDir := cfg.Metadata.CSV.Path
			if csvDir == "" {
				csvDir = "./metadata/"
			}

			loader := csv.NewLoader()
			entries, err := os.ReadDir(csvDir)
			if err != nil {
				return fmt.Errorf("read metadata dir %q: %w", csvDir, err)
			}

			hasTables := false
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				if !strings.HasSuffix(name, ".csv") {
					continue
				}
				path := filepath.Join(csvDir, name)
				f, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("open %s: %w", path, err)
				}
				loader.AddReader(name, f)
				defer f.Close()

				if name == "tables.csv" || name == "Tables.csv" {
					hasTables = true
				}
			}

			if !hasTables {
				return fmt.Errorf("tables.csv not found in %s", csvDir)
			}

			sm, err := loader.Load()
			if err != nil {
				return fmt.Errorf("load metadata: %w", err)
			}

			errs := csv.Validate(sm)
			if len(errs) > 0 {
				fmt.Printf("Validation found %d issue(s):\n", len(errs))
				for _, e := range errs {
					fmt.Printf("  %s\n", e.Error())
				}
				os.Exit(1)
			}

			fmt.Printf("Validation passed: %d tables, %d views loaded\n",
				len(sm.GetTables()), len(sm.GetViews()))
			return nil
		},
	}
}
