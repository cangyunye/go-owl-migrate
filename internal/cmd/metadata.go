package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
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
	// Embedded databases (sqlite3/duckdb) have no schema concept — the same
	// exemption the init wizard applies; without it an AI/CLI-generated config
	// for a sqlite source could never execute.
	if src.Schema == "" && !isEmbedded(src.Type) {
		return nil, fmt.Errorf("source.schema is required when metadata.type is 'database'")
	}

	db, err := openDB(src)
	if err != nil {
		return nil, connFailure("connect", "source for metadata extraction", src, err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, connFailure("ping", "source for metadata extraction", src, err)
	}

	sm, err := extractor.Extract(db, dbconn.MetadataSourceType(src), src.Schema)
	if err != nil {
		return nil, fmt.Errorf("extract metadata from %s: %w", src.Type, err)
	}
	fmt.Printf("Extracted metadata: %d tables from schema %q\n", len(sm.GetTables()), src.Schema)
	return sm, nil
}

// channelFlag / jarsDirFlag override the yaml `channel` / `agent.jars_dir`
// settings from the command line (root persistent flags; "" = no override).
var (
	channelFlag string
	jarsDirFlag string
	hooksWired  sync.Once
)

// wireDBConnHooks routes dbconn channel/encoding decisions to stderr so an
// auto-fallback or a charset override never happens silently.
func wireDBConnHooks() {
	hooksWired.Do(func() {
		dbconn.FallbackHook = func(dbType, reason string) {
			fmt.Fprintf(os.Stderr, "[channel] %s: %s, using owljdbc agent\n", dbType, reason)
		}
		dbconn.EncodingWarnHook = func(dbType, message string) {
			fmt.Fprintf(os.Stderr, "[encoding] %s: %s\n", dbType, message)
		}
	})
}

// openDB opens a database connection by type and configures the connection
// pool. CLI channel/jars overrides win over the yaml config.
func openDB(cfg config.DBConfig) (*sql.DB, error) {
	wireDBConnHooks()
	if channelFlag != "" {
		cfg.Channel = channelFlag
	}
	if jarsDirFlag != "" { // flag 覆盖 yaml（阶段二计划 §2.1）
		cfg.Agent.JarsDir = jarsDirFlag
	}
	return dbconn.Open(cfg)
}

// connFailure renders a connect/ping failure with the masked DSN and a triage
// hint — enough for the user to confirm WHICH host/account failed and where to
// look next, without echoing the password. verb is "connect"/"ping", side is
// "source"/"target"/"source for metadata extraction" etc.
func connFailure(verb, side string, cfg config.DBConfig, err error) error {
	dsn := config.MaskDSN(cfg.DSN)
	if dsn == "" {
		dsn = "(empty — set it in the config or via the datasource profile)"
	}
	return fmt.Errorf("%s %s: %w\n  DSN(masked): %s\n  hint: verify host/port/service name, network reachability, firewall/ACL, and account status",
		verb, side, err, dsn)
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

// splitTableList parses a comma-separated --tables flag value, dropping blanks.
// A nil result means "no override" — the config include list stays in effect.
// splitWhereList parses a CLI --where value: comma-separated
// "PATTERN: fragment" entries. The first colon separates pattern from the
// fragment (fragments may contain further colons, e.g. dates).
func splitWhereList(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.Index(part, ":")
		if i <= 0 {
			return nil, fmt.Errorf("--where entry %q must look like PATTERN: sql-where-fragment", part)
		}
		pat := strings.TrimSpace(part[:i])
		frag := strings.TrimSpace(part[i+1:])
		if pat == "" || frag == "" {
			return nil, fmt.Errorf("--where entry %q must look like PATTERN: sql-where-fragment", part)
		}
		if err := exporter.ValidateFilterFragment(frag); err != nil {
			return nil, fmt.Errorf("--where %s: %w", pat, err)
		}
		out[pat] = frag
	}
	return out, nil
}

// applyColumnProjection applies export.columns include/rename to the selected
// table definitions (metadata-level, once): DDL, export and import all see
// the projected model, so target tables and CSV headers stay consistent.
// No-op when export.columns is empty.
func applyColumnProjection(tables []*md.TableDef, cfg *config.Config) ([]*md.TableDef, error) {
	if len(cfg.Export.Columns.Include) == 0 && len(cfg.Export.Columns.Rename) == 0 {
		return tables, nil
	}
	out := make([]*md.TableDef, len(tables))
	for i, tbl := range tables {
		include, hasInc, err := exporter.ResolvePatternKey(cfg.Export.Columns.Include, tbl.TableSchema, tbl.TableName)
		if err != nil {
			return nil, err
		}
		rename, _, err := exporter.ResolvePatternKey(cfg.Export.Columns.Rename, tbl.TableSchema, tbl.TableName)
		if err != nil {
			return nil, err
		}
		if !hasInc {
			include = nil
		}
		projected, err := md.ProjectTable(tbl, include, rename)
		if err != nil {
			return nil, err
		}
		out[i] = projected
	}
	return out, nil
}

func splitTableList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
