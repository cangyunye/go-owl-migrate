package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	xlsxpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/xlsx"
)

// loadSchemaModel loads metadata from CSV files, xlsx, or live database based on config.
func loadSchemaModel(cfg *config.Config) (*md.SchemaModel, error) {
	switch cfg.Metadata.Type {
	case "csv":
		return loadCSVModel(cfg.Metadata.CSV.Path, cfg.Metadata.CSV.ColumnNameMatching)
	case "xlsx":
		return loadXLSXModel(cfg.Metadata.XLSX.Path, cfg.Metadata.XLSX.DataOutputDir)
	case "database":
		return loadDBModel(cfg.Source)
	default:
		return nil, fmt.Errorf("unsupported metadata type %q", cfg.Metadata.Type)
	}
}

// loadXLSXModel loads metadata from an xlsx file with @sheet data.
func loadXLSXModel(xlsxPath, dataOutputDir string) (*md.SchemaModel, error) {
	if xlsxPath == "" {
		return nil, fmt.Errorf("metadata.xlsx.path is required")
	}
	if dataOutputDir == "" {
		dataOutputDir = "./output/data/"
	}
	sm, err := xlsxpkg.Load(xlsxpkg.Config{
		FilePath:      xlsxPath,
		DataOutputDir: dataOutputDir,
	})
	if err != nil {
		return nil, fmt.Errorf("load xlsx %q: %w", xlsxPath, err)
	}
	fmt.Printf("Loaded %d tables from xlsx\n", len(sm.GetTables()))
	return sm, nil
}

// loadCSVModel loads metadata from CSV files in the given directory.
// If path is empty, defaults to "./testdata/csv/".
func loadCSVModel(csvDir, columnNameMatching string) (*md.SchemaModel, error) {
	if csvDir == "" {
		csvDir = "./testdata/csv/"
	}
	loader := csvpkg.NewLoader()
	loader.SetColumnNameMatching(columnNameMatching)
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
func loadDBModel(src config.DBConfig) (*md.SchemaModel, error) {
	if src.DSN == "" {
		return nil, fmt.Errorf("source.dsn is required when metadata.type is 'database'")
	}
	if src.Schema == "" {
		return nil, fmt.Errorf("source.schema is required when metadata.type is 'database'")
	}

	db, err := openDB(src)
	if err != nil {
		return nil, fmt.Errorf("connect to source for metadata extraction: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping source for metadata extraction: %w", err)
	}

	sm, err := extractor.Extract(db, dbconn.MetadataSourceType(src), src.Schema)
	if err != nil {
		return nil, fmt.Errorf("extract metadata from %s: %w", src.Type, err)
	}
	fmt.Printf("Extracted metadata: %d tables from schema %q\n", len(sm.GetTables()), src.Schema)
	return sm, nil
}

// openDB opens a database connection by type and configures the connection pool.
func openDB(cfg config.DBConfig) (*sql.DB, error) {
	return dbconn.Open(cfg)
}

// parseDuration parses a duration string, returning fallback if empty or invalid.
func parseDuration(s string, fallback time.Duration) (time.Duration, error) {
	if s == "" {
		return fallback, nil
	}
	return time.ParseDuration(s)
}

// connectTimeout returns the configured connect timeout or a default of 30s.
func connectTimeout(cfg config.DBConfig) time.Duration {
	if d, err := parseDuration(cfg.ConnectTimeout, 0); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// queryTimeout returns the configured query timeout or 0 (no timeout).
func queryTimeout(cfg config.DBConfig) time.Duration {
	if d, err := parseDuration(cfg.QueryTimeout, 0); err == nil {
		return d
	}
	return 0
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

// filterTables filters tables by include list（收敛到 metadata.ObjectSelector 语义，ADR-003）。
func filterTables(tables []*md.TableDef, include []string) []*md.TableDef {
	return md.FilterTablesByInclude(tables, include)
}
