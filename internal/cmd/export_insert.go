package cmd

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
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
	cmd.Flags().StringVarP(&dataDir, "data", "d", "./output/data/", "directory containing CSV data files")
	cmd.Flags().StringVar(&dialect, "dialect", "postgres", "target dialect: oracle/postgres/mysql")
	cmd.Flags().IntVarP(&batchSize, "batch-size", "n", 100, "VALUES rows per INSERT statement")
	cmd.Flags().BoolVar(&truncateBefore, "truncate", false, "add TRUNCATE TABLE before INSERT")
	cmd.Flags().BoolVar(&noQuote, "no-quote-identifiers", false, "do not quote identifiers (bare names, for compatibility)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			cfg = &config.Config{}
			if dialect != "" {
				cfg.DDL.TargetDialect = dialect
			}
		}

		var tables []*md.TableDef
		sm, err := loadSchemaModel(cfg)
		if err != nil || len(sm.GetTables()) == 0 {
			tables, err = detectTablesFromCSV(dataDir)
			if err != nil {
				return fmt.Errorf("no config file and no CSV files found in %s: %w", dataDir, err)
			}
		} else {
			tables = sm.GetTables()
		}

		gen := generator.NewInsertGenerator(generator.InsertConfig{
			OutputDir:           outputDir,
			BatchSize:           batchSize,
			TruncateBefore:      truncateBefore,
			Dialect:             dialect,
			NoQuoteIdentifiers:  noQuote,
		})

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

// detectTablesFromCSV scans a directory for CSV files and creates TableDef
// entries with ColumnDef inferred from CSV headers.
func detectTablesFromCSV(dir string) ([]*md.TableDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read data directory %q: %w", dir, err)
	}

	var tables []*md.TableDef
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".csv")
		parts := strings.SplitN(name, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema := parts[0]
		tableName := parts[1]

		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", entry.Name(), err)
		}
		r := csv.NewReader(f)
		header, err := r.Read()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read header from %s: %w", entry.Name(), err)
		}

		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, fmt.Errorf("create table def for %s: %w", entry.Name(), err)
		}
		for i, colName := range header {
			col, err := md.NewColumnDef(schema, tableName, colName, i+1, "VARCHAR")
			if err != nil {
				return nil, fmt.Errorf("create column %s: %w", colName, err)
			}
			tbl.AddColumn(col)
		}
		tables = append(tables, tbl)
	}

	if len(tables) == 0 {
		return nil, fmt.Errorf("no CSV files found in %q", dir)
	}
	return tables, nil
}
