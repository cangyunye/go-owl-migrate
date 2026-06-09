package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

// loadSchemaModel loads metadata from CSV files or live database based on config.
func loadSchemaModel(cfg *config.Config) (*md.SchemaModel, error) {
	switch cfg.Metadata.Type {
	case "csv":
		return loadCSVModel(cfg.Metadata.CSV.Path)
	case "database":
		return loadDBModel(cfg.Source.Type, cfg.Source.DSN, cfg.Source.Schema)
	default:
		return nil, fmt.Errorf("unsupported metadata type %q", cfg.Metadata.Type)
	}
}

// loadCSVModel loads metadata from CSV files in the given directory.
// If path is empty, defaults to "./testdata/csv/".
func loadCSVModel(csvDir string) (*md.SchemaModel, error) {
	if csvDir == "" {
		csvDir = "./testdata/csv/"
	}
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

// loadDBModel connects to a live database and extracts full schema metadata.
func loadDBModel(dbType, dsn, schema string) (*md.SchemaModel, error) {
	if dsn == "" {
		return nil, fmt.Errorf("source.dsn is required when metadata.type is 'database'")
	}
	if schema == "" {
		return nil, fmt.Errorf("source.schema is required when metadata.type is 'database'")
	}

	db, err := openDB(dbType, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to source for metadata extraction: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping source for metadata extraction: %w", err)
	}

	sm, err := extractor.Extract(db, dbType, schema)
	if err != nil {
		return nil, fmt.Errorf("extract metadata from %s: %w", dbType, err)
	}
	fmt.Printf("Extracted metadata: %d tables from schema %q\n", len(sm.GetTables()), schema)
	return sm, nil
}

// openDB opens a database connection by type.
func openDB(dbType, dsn string) (*sql.DB, error) {
	switch strings.ToLower(dbType) {
	case "mysql":
		return sql.Open("mysql", dsn)
	case "postgres", "postgresql":
		return sql.Open("postgres", dsn)
	case "oracle":
		return sql.Open("oracle", dsn)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}

// buildPKMap builds the primary key column map for cursor-based pagination.
func buildPKMap(sm *md.SchemaModel) map[string][]string {
	pkMap := make(map[string][]string)
	for _, tbl := range sm.GetTables() {
		pks := tbl.GetPrimaryKeys()
		if len(pks) > 0 {
			key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
			names := make([]string, len(pks))
			for i, pk := range pks {
				names[i] = pk.ColumnName
			}
			pkMap[key] = names
		}
	}
	return pkMap
}

// filterTables filters tables by include list.
func filterTables(tables []*md.TableDef, include []string) []*md.TableDef {
	if len(include) == 1 && include[0] == "*" {
		return tables
	}
	includeSet := make(map[string]bool)
	for _, inc := range include {
		includeSet[inc] = true
	}
	var result []*md.TableDef
	for _, tbl := range tables {
		key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
		if includeSet[key] || includeSet["*"] {
			result = append(result, tbl)
		}
	}
	return result
}
