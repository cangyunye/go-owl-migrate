package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
)

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate metadata (CSV files or live database)",
		Long: `Validates metadata from CSV files or a live database connection
against the schema reference model.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			sm, err := loadSchemaModel(cfg)
			if err != nil {
				return fmt.Errorf("load metadata: %w", err)
			}

			errs := csv.Validate(sm)
			if len(errs) > 0 {
				fmt.Printf("Validation found %d issue(s):\n", len(errs))
				for _, e := range errs {
					fmt.Printf("  %s\n", e.Error())
				}
				return fmt.Errorf("validation failed with %d errors", len(errs))
			}

			fmt.Printf("Validation passed: %d tables, %d views loaded\n",
				len(sm.GetTables()), len(sm.GetViews()))
			return nil
		},
	}
}
