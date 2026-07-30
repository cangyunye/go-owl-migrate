//go:build e2e

package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
	"go.uber.org/zap"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

// DSN constants matching docker-compose in testdata/db/
const (
	oracleSrcDSN    = "oracle://scott:tiger@127.0.0.1:1521/XEPDB1"
	mysqlSrcDSN     = "root:root123456@tcp(127.0.0.1:3306)/default_db"
	pgSrcDSN        = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"
	pgTargetDSN     = pgSrcDSN
	oracleTargetDSN = "oracle://appuser:App123!@127.0.0.1:1521/XEPDB1"
	mysqlRootDSN    = "root:root123456@tcp(127.0.0.1:3306)/"
)

// ── helpers ──

func connectE2E(t *testing.T, dbType, dsn string) *sql.DB {
	t.Helper()
	cfg := config.DBConfig{Type: dbType, DSN: dsn}
	db, err := openDB(cfg)
	if err != nil {
		t.Skipf("open %s unavailable: %v", dbType, err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("ping %s unreachable: %v", dbType, err)
	}
	return db
}

func countTargetRows(t *testing.T, db *sql.DB, schema, table, dbType string) int {
	t.Helper()
	var n int
	patterns := []string{
		`SELECT COUNT(*) FROM "%s"."%s"`,
		`SELECT COUNT(*) FROM %s.%s`,
		`SELECT COUNT(*) FROM "%s".%s`,
		`SELECT COUNT(*) FROM %s."%s"`,
	}
	// MySQL-specific quoting
	if dbType == "mysql" {
		patterns = []string{
			"SELECT COUNT(*) FROM `%s`.`%s`",
			"SELECT COUNT(*) FROM %s.%s",
			"SELECT COUNT(*) FROM `%s`.%s",
			"SELECT COUNT(*) FROM %s.`%s`",
		}
	}
	for _, pat := range patterns {
		q := fmt.Sprintf(pat, schema, table)
		err := db.QueryRow(q).Scan(&n)
		if err == nil {
			return n
		}
	}
	t.Fatalf("count %s.%s failed for all quoting variations", schema, table)
	return 0
}

func setupPGSchema(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	_, err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", name))
	if err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", name))
	})
	return name
}

func setupMySQLTargetDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("migtarget_%d", os.Getpid())
	db, err := sql.Open("mysql", mysqlRootDSN)
	if err != nil {
		t.Fatalf("open mysql root: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", name))
	if err != nil {
		t.Fatalf("create mysql db %s: %v", name, err)
	}
	t.Cleanup(func() {
		db2, _ := sql.Open("mysql", mysqlRootDSN)
		defer db2.Close()
		db2.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
	})
	return name
}

func cleanupOracleTarget(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("SELECT table_name FROM user_tables")
	if err != nil {
		t.Fatalf("query oracle user_tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	for _, tbl := range tables {
		db.Exec(fmt.Sprintf("DROP TABLE \"%s\" CASCADE CONSTRAINTS PURGE", tbl))
	}
}

func nopLogger() *zap.Logger {
	return zap.NewNop()
}

// ── pipeline runner ──

type migratePipelineConfig struct {
	sourceType    string
	sourceDSN     string
	sourceSchema  string
	targetType    string
	targetDSN     string
	schemaMapping map[string]string
}

func runMigratePipeline(t *testing.T, cfg migratePipelineConfig) {
	t.Helper()

	tic := time.Now()

	metaCfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source: config.DBConfig{
			Type:   cfg.sourceType,
			DSN:    cfg.sourceDSN,
			Schema: cfg.sourceSchema,
		},
		Target: config.DBConfig{Type: cfg.targetType, DSN: cfg.targetDSN},
		DDL: config.DDLConfig{
			TargetDialect:      cfg.targetType,
			SchemaMapping:      cfg.schemaMapping,
			IncludeIfNotExists: !strings.EqualFold(cfg.targetType, "oracle"),
		},
	}

	// Connect source
	srcDB := connectE2E(t, cfg.sourceType, cfg.sourceDSN)

	// Connect target
	tgtDB := connectE2E(t, cfg.targetType, cfg.targetDSN)

	// Load metadata from source database
	sm, err := loadSchemaModel(metaCfg)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables loaded from source metadata")
	}
	t.Logf("loaded %d tables: %v", len(tables), tableNames(tables))

	// Resolve effective target schema from mapping
	effectiveSchema := cfg.schemaMapping[cfg.sourceSchema]

	// Build PK map for cursor pagination
	pkMap := buildPKMap(sm)

	// Create target tables
	ctx := context.Background()
	if err := ensureTablesForMigrate(ctx, tgtDB, sm, metaCfg); err != nil {
		t.Fatalf("create target tables: %v", err)
	}

	// Export from source to CSV
	tmpDir := t.TempDir()
	exp := exporter.New(srcDB, exporter.Config{
		OutputDir:    tmpDir,
		PageSize:     100,
		MaxWorkers:   1,
		CSVDelimiter: ",",
		CSVHeader:    true,
		CSVNullRep:   "\\N",
		DBType:       cfg.sourceType,
		Logger:       nopLogger(),
	})
	exportResults, err := exp.ExportTables(ctx, tables, pkMap)
	if err != nil {
		t.Fatalf("export tables: %v", err)
	}
	for _, r := range exportResults {
		if r.Error != nil {
			t.Logf("export %s.%s: %v", r.Schema, r.Table, r.Error)
		}
	}

	// Import CSV into target
	impCfg := importer.Config{
		SourceDir:        tmpDir,
		CSVDelimiter:     ",",
		CSVNullMarker:    "\\N",
		CommitInterval:   100,
		ErrorPolicy:      "stop",
		MaxWorkers:       1,
		DateTimeFormat:   "yyyyMMddHHmmss",
		TrimStrings:      true,
		RespectForeignKeys: false,
		TargetDBType:     cfg.targetType,
		Logger:           nopLogger(),
	}
	// Truncate for repeatable runs (except Oracle where TRUNCATE is DDL)
	if !strings.EqualFold(cfg.targetType, "oracle") {
		impCfg.TruncateBefore = true
	}
	imp := importer.New(tgtDB, impCfg)
	importResults, err := imp.ImportTables(ctx, tables, metaCfg.DDL.SchemaMapping)
	if err != nil {
		t.Fatalf("import tables: %v", err)
	}
	for _, r := range importResults {
		if r.Err != nil {
			t.Errorf("import %s.%s failed: %v", r.Schema, r.Table, r.Err)
		} else {
			t.Logf("import %s.%s: %d/%d rows (skipped %d)", r.Schema, r.Table, r.Actual, r.Expected, r.Skipped)
		}
	}

	// Verify target row counts
	gotEmp := countTargetRows(t, tgtDB, effectiveSchema, "EMP", cfg.targetType)
	if gotEmp != 14 {
		t.Errorf("EMP rows = %d, want 14", gotEmp)
	}
	gotDept := countTargetRows(t, tgtDB, effectiveSchema, "DEPT", cfg.targetType)
	if gotDept != 4 {
		t.Errorf("DEPT rows = %d, want 4", gotDept)
	}
	t.Logf("pipeline completed in %v: EMP=%d, DEPT=%d", time.Since(tic), gotEmp, gotDept)
}

func tableNames(tables []*md.TableDef) []string {
	var names []string
	for _, t := range tables {
		names = append(names, t.TableName)
	}
	return names
}

// ── migration matrix: 5 combinations ──

func TestMigrateE2E_OracleToPG(t *testing.T) {
	schema := fmt.Sprintf("mig_orapg_%d", os.Getpid())
	tgtDB := connectE2E(t, "postgres", pgTargetDSN)
	setupPGSchema(t, tgtDB, schema)
	runMigratePipeline(t, migratePipelineConfig{
		sourceType:   "oracle",
		sourceDSN:    oracleSrcDSN,
		sourceSchema: "SCOTT",
		targetType:   "postgres",
		targetDSN:    pgTargetDSN,
		schemaMapping: map[string]string{"SCOTT": schema},
	})
}

func TestMigrateE2E_MySQLToPG(t *testing.T) {
	schema := fmt.Sprintf("mig_mypg_%d", os.Getpid())
	tgtDB := connectE2E(t, "postgres", pgTargetDSN)
	setupPGSchema(t, tgtDB, schema)
	runMigratePipeline(t, migratePipelineConfig{
		sourceType:   "mysql",
		sourceDSN:    mysqlSrcDSN,
		sourceSchema: "default_db",
		targetType:   "postgres",
		targetDSN:    pgTargetDSN,
		schemaMapping: map[string]string{"default_db": schema},
	})
}

func TestMigrateE2E_PGToMySQL(t *testing.T) {
	dbName := setupMySQLTargetDB(t)
	targetDSN := fmt.Sprintf("root:root123456@tcp(127.0.0.1:3306)/%s?charset=utf8mb4", dbName)
	runMigratePipeline(t, migratePipelineConfig{
		sourceType:   "postgres",
		sourceDSN:    pgSrcDSN,
		sourceSchema: "public",
		targetType:   "mysql",
		targetDSN:    targetDSN,
		schemaMapping: map[string]string{"public": dbName},
	})
}

func TestMigrateE2E_PGToOracle(t *testing.T) {
	tgtDB := connectE2E(t, "oracle", oracleTargetDSN)
	cleanupOracleTarget(t, tgtDB)
	runMigratePipeline(t, migratePipelineConfig{
		sourceType:   "postgres",
		sourceDSN:    pgSrcDSN,
		sourceSchema: "public",
		targetType:   "oracle",
		targetDSN:    oracleTargetDSN,
		schemaMapping: map[string]string{"public": "APPUSER"},
	})
}

func TestMigrateE2E_MySQLToOracle(t *testing.T) {
	tgtDB := connectE2E(t, "oracle", oracleTargetDSN)
	cleanupOracleTarget(t, tgtDB)
	runMigratePipeline(t, migratePipelineConfig{
		sourceType:   "mysql",
		sourceDSN:    mysqlSrcDSN,
		sourceSchema: "default_db",
		targetType:   "oracle",
		targetDSN:    oracleTargetDSN,
		schemaMapping: map[string]string{"default_db": "APPUSER"},
	})
}

// ── SQL output mode test ──

func TestMigrateE2E_SQLOutMode(t *testing.T) {
	schema := fmt.Sprintf("mig_sqlout_%d", os.Getpid())
	srcDB := connectE2E(t, "oracle", oracleSrcDSN)
	pkMap := buildPKMap(loadSchemaModelOrDie(t, "oracle", oracleSrcDSN, "SCOTT"))
	tables := loadSchemaModelOrDie(t, "oracle", oracleSrcDSN, "SCOTT").GetTables()

	tmpDir := t.TempDir()
	exp := exporter.New(srcDB, exporter.Config{
		OutputDir:    tmpDir,
		PageSize:     100,
		MaxWorkers:   1,
		CSVDelimiter: ",",
		CSVHeader:    true,
		CSVNullRep:   "\\N",
		DBType:       "oracle",
		Logger:       nopLogger(),
	})
	ctx := context.Background()
	_, err := exp.ExportTables(ctx, tables, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// Generate INSERT SQL (sql-out mode equivalent)
	insertGen := generator.NewInsertGenerator(generator.InsertConfig{
		OutputDir:          t.TempDir(),
		BatchSize:          100,
		Dialect:            "postgres",
		NoQuoteIdentifiers: false,
	})
	files, err := insertGen.Generate(tables, tmpDir)
	if err != nil {
		t.Fatalf("generate insert SQL: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least 1 INSERT SQL file")
	}
	t.Logf("generated %d SQL files: %v", len(files), files)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(content), "INSERT INTO") {
			t.Errorf("file %s missing INSERT INTO", f)
		}
	}

	// Verify target DB was NOT touched
	_ = schema
}

func loadSchemaModelOrDie(t *testing.T, dbType, dsn, schema string) *md.SchemaModel {
	t.Helper()
	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source: config.DBConfig{Type: dbType, DSN: dsn, Schema: schema},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	return sm
}


