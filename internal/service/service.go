package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/logger"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	csvpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/csv"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	xlsxpkg "github.com/cangyunye/go-owl-migrate/internal/metadata/xlsx"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func LoadMetadata(cfg *config.Config) (*md.SchemaModel, error) {
	switch cfg.Metadata.Type {
	case "csv":
		return loadCSVModel(cfg.Metadata.CSV.Path, cfg.Metadata.CSV.ColumnNameMatching)
	case "xlsx":
		return loadXLSXModel(cfg.Metadata.XLSX.Path, cfg.Metadata.XLSX.DataOutputDir)
	case "database":
		return loadDBModel(cfg.Source.Type, cfg.Source.DSN, cfg.Source.Schema)
	default:
		return nil, fmt.Errorf("unsupported metadata type %q", cfg.Metadata.Type)
	}
}

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
	return sm, nil
}

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

func loadDBModel(dbType, dsn, schema string) (*md.SchemaModel, error) {
	if dsn == "" {
		return nil, fmt.Errorf("source.dsn is required when metadata.type is 'database'")
	}
	if schema == "" {
		return nil, fmt.Errorf("source.schema is required when metadata.type is 'database'")
	}

	db, err := OpenDB(config.DBConfig{Type: dbType, DSN: dsn})
	if err != nil {
		return nil, fmt.Errorf("connect to source for metadata extraction: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping source for metadata extraction: %w", err)
	}

	sm, err := extractor.Extract(db, dbType, schema)
	if err != nil {
		return nil, fmt.Errorf("extract metadata from %s: %w", dbType, err)
	}
	return sm, nil
}

func OpenDB(cfg config.DBConfig) (*sql.DB, error) {
	var db *sql.DB
	var err error

	switch registry.Normalize(strings.ToLower(cfg.Type)) {
	case "mysql", "goldendb-mysql", "oceanbase-mysql":
		db, err = sql.Open("mysql", cfg.DSN)
	case "sqlite3":
		db, err = sql.Open("sqlite3", cfg.DSN)
	case "duckdb":
		db, err = sql.Open("duckdb", cfg.DSN)
	case "postgres", "postgresql", "panweidb", "panweidb-mysql", "panweidb-oracle", "opengaussdb":
		db, err = sql.Open("postgres", cfg.DSN)
	case "oracle", "goldendb-oracle", "oceanbase-oracle":
		db, err = sql.Open("oracle", cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
	if err != nil {
		return nil, err
	}

	configurePool(db, cfg)
	return db, nil
}

func configurePool(db *sql.DB, cfg config.DBConfig) {
	maxOpen := cfg.Pool.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	db.SetMaxOpenConns(maxOpen)

	maxIdle := cfg.Pool.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	db.SetMaxIdleConns(maxIdle)

	if d, err := parseDuration(cfg.Pool.ConnMaxLifetime, 30*time.Minute); err == nil {
		db.SetConnMaxLifetime(d)
	}
	if d, err := parseDuration(cfg.Pool.ConnMaxIdleTime, 5*time.Minute); err == nil {
		db.SetConnMaxIdleTime(d)
	}
}

func parseDuration(s string, fallback time.Duration) (time.Duration, error) {
	if s == "" {
		return fallback, nil
	}
	return time.ParseDuration(s)
}

func ConnectTimeout(cfg config.DBConfig) time.Duration {
	if d, err := parseDuration(cfg.ConnectTimeout, 0); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func QueryTimeout(cfg config.DBConfig) time.Duration {
	if d, err := parseDuration(cfg.QueryTimeout, 0); err == nil {
		return d
	}
	return 0
}

func BuildPKMap(sm *md.SchemaModel) map[string][]string {
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

func FilterTables(tables []*md.TableDef, include []string) []*md.TableDef {
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

func ToBuildOptions(cfg *config.Config) dialect.BuildOptions {
	return dialect.BuildOptions{
		TargetDialect:      cfg.DDL.TargetDialect,
		SchemaMapping:      cfg.DDL.SchemaMapping,
		IncludeComments:    cfg.DDL.IncludeComments,
		IncludeIfNotExists: cfg.DDL.IncludeIfNotExists,
		IncludeDrop:        cfg.DDL.IncludeDrop,
		TypeOverrides:      cfg.DDL.TypeOverrides,
		BooleanMapping:     cfg.DDL.BooleanMapping,
		EmptyStringToNull:  cfg.DDL.EmptyStringToNull,
		AddRowIDColumn:     cfg.DDL.AddRowIDColumn,
		IdentityToSerial:   cfg.DDL.IdentityToSerial,
		SkipPartitions:     !cfg.DDL.Partition.Migrate,
		NoQuoteIdentifiers: cfg.DDL.NoQuoteIdentifiers,
	}
}

func NewLogger(cfg *config.Config) (*zap.Logger, error) {
	return logger.New(logger.Config{
		Level:  cfg.General.LogLevel,
		Format: cfg.General.LogFormat,
		File:   cfg.General.LogFile,
	})
}
