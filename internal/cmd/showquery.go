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

Dialect is required. Supported dialects:

  Oracle-based:     oracle, goldendb-oracle, oceanbase-oracle, panweidb-oracle
  PostgreSQL-based: postgres, panweidb, opengaussdb
  MySQL-based:      mysql, goldendb-mysql, oceanbase-mysql, panweidb-mysql
  Short aliases:    goldendb, oceanbase

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
					supportedDialects := []string{
						"oracle", "postgres", "mysql",
						"goldendb", "goldendb-mysql", "goldendb-oracle",
						"oceanbase", "oceanbase-mysql", "oceanbase-oracle",
						"panweidb", "panweidb-mysql", "panweidb-oracle", "opengaussdb",
					}
					fmt.Fprintf(os.Stderr, "Error: unsupported dialect %q\nSupported dialects: %s\n",
						dialect, strings.Join(supportedDialects, ", "))
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
