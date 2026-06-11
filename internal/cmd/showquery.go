package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

func showQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show-query <dialect> [object-type]",
		Short: "Show metadata extraction SQL queries for a dialect",
		Long: `Prints the SQL queries used to extract metadata from a database.

Dialect is required: oracle, postgres, mysql (also accepts goldendb, oceanbase, etc.)

Object type is optional. If omitted, all queries for the dialect are shown.
Valid object types: tables, columns, pk, indexes, fk, views, sequences, triggers, synonyms

Examples:
  owl-migrate show-query oracle
  owl-migrate show-query oracle tables
  owl-migrate show-query postgres columns`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dialect := strings.ToLower(args[0])

			// Validate dialect
			if !config.ValidDialects[dialect] {
				// Check if it's a compound dialect
				valid := false
				for k := range config.ValidDialects {
					if strings.EqualFold(k, dialect) {
						valid = true
						break
					}
				}
				if !valid {
					fmt.Fprintf(os.Stderr, "Error: unsupported dialect %q\n", dialect)
					return fmt.Errorf("unsupported dialect %q", dialect)
				}
			}

			objectTypes := []string{"tables", "columns", "pk", "indexes", "fk", "views", "sequences", "triggers", "synonyms"}

			if len(args) == 2 {
				objectTypes = []string{strings.ToLower(args[1])}
			}

			for _, ot := range objectTypes {
				sql := extractor.GetQuerySQL(dialect, ot)
				if sql == "" {
					continue
				}
				fmt.Printf("--- %s: %s ---\n", dialect, ot)
				fmt.Println(sql)
				fmt.Println()
			}

			return nil
		},
	}

	return cmd
}
