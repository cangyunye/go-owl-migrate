package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func targetTypeFamily(dbType string) string {
	t := strings.ToLower(strings.TrimSpace(dbType))
	t = registry.Normalize(t)
	switch {
	case t == "panweidb" || strings.HasPrefix(t, "panweidb-"):
		// PanWeiDB speaks the PostgreSQL wire protocol in all SQL modes.
		return "postgres"
	case t == "mysql" || strings.HasSuffix(t, "-mysql"):
		return "mysql"
	case t == "oracle" || strings.HasSuffix(t, "-oracle"):
		return "oracle"
	case t == "sqlite3" || t == "duckdb":
		return t
	default:
		return "postgres"
	}
}

// placeholderFamilyFor returns the bind placeholder override required by the
// connection, e.g. "?" for OceanBase Oracle tenants reached over MySQL wire.
func placeholderFamilyFor(cfg config.DBConfig) string {
	if dbconn.OceanBaseOracleUsesMySQLWire(cfg) {
		return "qmark"
	}
	return ""
}

// tableExists reports whether the target table already exists. wireQmark
// selects "?" binds for Oracle-family targets reached over the MySQL wire.
func tableExists(ctx context.Context, db *sql.DB, dbType, schema, table string, wireQmark bool) (bool, error) {
	var query string
	var args []any
	switch targetTypeFamily(dbType) {
	case "mysql":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?"
		args = []any{schema, table}
	case "oracle":
		if wireQmark {
			query = "SELECT COUNT(*) FROM all_tables WHERE owner = UPPER(?) AND table_name = UPPER(?)"
		} else {
			query = "SELECT COUNT(*) FROM all_tables WHERE owner = UPPER(:1) AND table_name = UPPER(:2)"
		}
		args = []any{schema, table}
	case "sqlite3":
		query = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
		args = []any{table}
	case "duckdb":
		query = "SELECT COUNT(*) FROM duckdb_tables() WHERE schema_name = ? AND table_name = ?"
		args = []any{schema, table}
	default:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2"
		args = []any{schema, table}
	}
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// resolveSourceDialect determines the dialect of the metadata column types:
// explicit ddl.source_dialect wins, then the live source database type.
func resolveSourceDialect(cfg *config.Config) string {
	if s := strings.TrimSpace(cfg.DDL.SourceDialect); s != "" {
		return s
	}
	return strings.TrimSpace(cfg.Source.Type)
}

// buildCreateTableViaDialect renders the CREATE TABLE statement for the target
// database using the dialect system. Cross-dialect type conversion goes through
// the LogicalType IR (source ToLogicalType → target FromLogicalType); otherwise
// source types are emitted with length/precision qualifiers.
func buildCreateTableViaDialect(tbl *md.TableDef, cfg *config.Config) (string, error) {
	targetName := registry.Normalize(strings.ToLower(cfg.Target.Type))
	target, err := registry.Get(targetName)
	if err != nil {
		return "", fmt.Errorf("unknown target dialect %q: %w", cfg.Target.Type, err)
	}

	opts := toBuildOptions(cfg)
	opts.TargetDialect = targetName
	opts.PreserveIdentifierCase = true

	converted := tbl
	if srcName := resolveSourceDialect(cfg); srcName != "" {
		srcNorm := registry.Normalize(strings.ToLower(srcName))
		if srcNorm != targetName && targetTypeFamily(srcNorm) != targetTypeFamily(targetName) {
			if src, serr := registry.Get(srcNorm); serr == nil {
				converted = convertTableTypes(tbl, src, target, opts)
			} else {
				converted = qualifyTableTypes(tbl, opts)
			}
		} else {
			converted = qualifyTableTypes(tbl, opts)
		}
	} else {
		converted = qualifyTableTypes(tbl, opts)
	}

	return target.BuildCreateTable(converted, opts)
}

// convertTableTypes returns a copy of tbl with column types converted from the
// source dialect to the target dialect via the LogicalType IR.
func convertTableTypes(tbl *md.TableDef, src, tgt dialect.Dialect, opts dialect.BuildOptions) *md.TableDef {
	cols := tbl.GetColumns()
	newCols := make([]*md.ColumnDef, len(cols))
	for i, col := range cols {
		nc := *col
		nc.DataType = convertColumnType(col, src, tgt, opts)
		// Source-dialect DEFAULT expressions (e.g. nextval(...), sysdate) are
		// not portable across dialects; drop them on cross-dialect conversion.
		nc.DefaultValue = ""
		newCols[i] = &nc
	}
	cp := *tbl
	cp.Columns = newCols
	return &cp
}

func convertColumnType(col *md.ColumnDef, src, tgt dialect.Dialect, opts dialect.BuildOptions) string {
	if override, ok := dialect.ApplyTypeOverride(col.DataType, col.DataLength, col.DataPrecision, col.DataScale, opts); ok {
		return override
	}
	lt := src.ToLogicalType(col.DataType, col.DataLength, col.DataPrecision, col.DataScale)
	return tgt.FromLogicalType(lt)
}

// qualifyTableTypes returns a copy of tbl with length/precision qualifiers
// appended to bare column types (CSV-style metadata keeps them in separate
// fields).
func qualifyTableTypes(tbl *md.TableDef, opts dialect.BuildOptions) *md.TableDef {
	cols := tbl.GetColumns()
	newCols := make([]*md.ColumnDef, len(cols))
	for i, col := range cols {
		nc := *col
		nc.DataType = qualifyColumnType(col, opts)
		newCols[i] = &nc
	}
	cp := *tbl
	cp.Columns = newCols
	return &cp
}

// convertSchemaModelForDDL returns a copy of sm whose table columns are
// qualified (same-dialect) or converted (cross-dialect) for the target
// dialect. Live-extracted metadata carries bare data_type values (e.g.
// "varchar", "decimal") with length/precision in separate fields, so without
// this the generator would emit invalid target DDL. Returns the input model
// unchanged when no source dialect can be resolved.
func convertSchemaModelForDDL(sm *md.SchemaModel, cfg *config.Config, tgt dialect.Dialect, opts dialect.BuildOptions) *md.SchemaModel {
	srcName := resolveSourceDialect(cfg)
	if srcName == "" {
		return sm
	}
	srcNorm := registry.Normalize(strings.ToLower(srcName))
	tgtNorm := registry.Normalize(strings.ToLower(cfg.DDL.TargetDialect))
	// Same type family (e.g. oceanbase-mysql → mysql) keeps source types and
	// DEFAULTs verbatim, only adding length/precision qualifiers; the LogicalType
	// IR conversion is reserved for genuinely different families (mysql ↔ pg ↔
	// oracle) where defaults are not portable anyway.
	cross := srcNorm != tgtNorm && targetTypeFamily(srcNorm) != targetTypeFamily(tgtNorm)

	var src dialect.Dialect
	if cross {
		s, err := registry.Get(srcNorm)
		if err != nil {
			return sm
		}
		src = s
	}

	converted := md.NewSchemaModel()
	for _, tbl := range sm.GetTables() {
		var out *md.TableDef
		if cross {
			out = convertTableTypes(tbl, src, tgt, opts)
		} else {
			out = qualifyTableTypes(tbl, opts)
		}
		_ = converted.AddTable(out) // GetTables() yields unique keys
	}
	return converted
}

func qualifyColumnType(col *md.ColumnDef, opts dialect.BuildOptions) string {
	if _, ok := dialect.ApplyTypeOverride(col.DataType, col.DataLength, col.DataPrecision, col.DataScale, opts); ok {
		return col.DataType
	}
	dt := strings.TrimSpace(col.DataType)
	if strings.Contains(dt, "(") {
		return dt
	}
	up := strings.ToUpper(dt)
	switch {
	case isCharTypeName(up) && col.DataLength > 0:
		return fmt.Sprintf("%s(%d)", dt, col.DataLength)
	case isNumericTypeName(up) && col.DataPrecision > 0:
		if col.DataScale > 0 {
			return fmt.Sprintf("%s(%d,%d)", dt, col.DataPrecision, col.DataScale)
		}
		return fmt.Sprintf("%s(%d)", dt, col.DataPrecision)
	default:
		return dt
	}
}

func isCharTypeName(t string) bool {
	switch t {
	case "VARCHAR", "VARCHAR2", "NVARCHAR2", "CHAR", "NCHAR", "CHARACTER",
		"CHARACTER VARYING", "RAW", "VARBINARY", "BINARY", "STRING":
		return true
	}
	return false
}

func isNumericTypeName(t string) bool {
	switch t {
	case "NUMBER", "NUMERIC", "DECIMAL", "DEC":
		return true
	}
	return false
}
