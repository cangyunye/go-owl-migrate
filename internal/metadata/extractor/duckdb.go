//go:build duckdb

package extractor

import (
	"database/sql"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// DuckDBMetadataQuerier extracts metadata from DuckDB databases.
// DuckDB supports SQL-standard information_schema for metadata queries.
// The schema parameter is used as the DuckDB schema name (default: "main").
type DuckDBMetadataQuerier struct{}

func (DuckDBMetadataQuerier) Type() string { return "duckdb" }

func (DuckDBMetadataQuerier) QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = ? AND table_type = 'BASE TABLE'
		ORDER BY table_name`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []*md.TableDef
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}
		tbl, err := md.NewTableDef(sch, tableName)
		if err != nil {
			return nil, err
		}
		tables = append(tables, tbl)
	}
	return tables, rows.Err()
}

func (DuckDBMetadataQuerier) QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT table_name, column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = ?
		ORDER BY table_name, ordinal_position`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []*md.ColumnDef
	for rows.Next() {
		var tableName, colName, dataType, nullable string
		var defaultVal sql.NullString
		if err := rows.Scan(&tableName, &colName, &dataType, &nullable, &defaultVal); err != nil {
			return nil, err
		}
		col, err := md.NewColumnDef(sch, tableName, colName, len(columns)+1, dataType)
		if err != nil {
			return nil, err
		}
		if nullable == "NO" || nullable == "no" {
			col.Nullable = "NO"
		} else {
			col.Nullable = "YES"
		}
		if defaultVal.Valid {
			col.DefaultValue = defaultVal.String
		}
		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (DuckDBMetadataQuerier) QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT kcu.table_name, kcu.constraint_name, kcu.column_name, kcu.ordinal_position
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		WHERE tc.table_schema = ? AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY kcu.table_name, kcu.ordinal_position`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pks []*md.PrimaryKeyDef
	for rows.Next() {
		var tableName, constraintName, colName string
		var pos int
		if err := rows.Scan(&tableName, &constraintName, &colName, &pos); err != nil {
			return nil, err
		}
		pks = append(pks, &md.PrimaryKeyDef{
			TableSchema:      sch,
			TableName:        tableName,
			ConstraintName:   constraintName,
			ColumnName:       colName,
			OrdinalPosition:  pos,
		})
	}
	return pks, rows.Err()
}

func (DuckDBMetadataQuerier) QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error) {
	// DuckDB does not expose indexes via information_schema.
	// The duckdb_indexes() table function provides this info.
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT index_name, table_name, is_unique, expressions
		FROM duckdb_indexes()
		WHERE schema_name = ?
		ORDER BY index_name`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []*md.IndexDef
	for rows.Next() {
		var indexName, tableName, colNames string
		var isUnique bool
		if err := rows.Scan(&indexName, &tableName, &isUnique, &colNames); err != nil {
			return nil, err
		}
		unq := "NONUNIQUE"
		if isUnique {
			unq = "UNIQUE"
		}
		// Skip primary key indexes
		if strings.Contains(strings.ToLower(indexName), "pk_") {
			continue
		}
		// Parse column names from DuckDB format: "[col1, col2]" or "col1, col2"
		colNames = strings.Trim(colNames, "[]")
		cols := splitCSL(colNames)
		for i, col := range cols {
			indexes = append(indexes, &md.IndexDef{
				TableSchema:     sch,
				TableName:       tableName,
				IndexName:       indexName,
				IndexType:       "BTREE",
				Uniqueness:      unq,
				ColumnName:      col,
				OrdinalPosition: i + 1,
			})
		}
	}
	return indexes, rows.Err()
}

func (DuckDBMetadataQuerier) QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT
			kcu.table_name, kcu.column_name, kcu.constraint_name,
			ccu.table_name AS ref_table, ccu.column_name AS ref_column,
			COALESCE(r.update_rule, 'NO ACTION'), COALESCE(r.delete_rule, 'NO ACTION')
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		JOIN information_schema.referential_constraints r
			ON tc.constraint_name = r.constraint_name AND tc.table_schema = r.constraint_schema
		JOIN information_schema.constraint_column_usage ccu
			ON r.unique_constraint_name = ccu.constraint_name
		WHERE tc.table_schema = ? AND tc.constraint_type = 'FOREIGN KEY'`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []*md.ForeignKeyDef
	for rows.Next() {
		var tableName, colName, constraintName, refTable, refCol, updateRule, deleteRule string
		if err := rows.Scan(&tableName, &colName, &constraintName, &refTable, &refCol, &updateRule, &deleteRule); err != nil {
			return nil, err
		}
		fks = append(fks, &md.ForeignKeyDef{
			ConstraintName: constraintName,
			TableSchema:    sch,
			TableName:      tableName,
			ColumnName:     colName,
			RefSchema:      sch,
			RefTable:       refTable,
			RefColumn:      refCol,
			DeleteRule:     deleteRule,
			UpdateRule:     updateRule,
		})
	}
	return fks, rows.Err()
}

func (DuckDBMetadataQuerier) QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT table_name, view_definition
		FROM information_schema.views
		WHERE table_schema = ? AND table_name NOT LIKE 'duckdb_%' AND table_name NOT LIKE 'pragma_%' AND table_name NOT LIKE 'sqlite_%'
		ORDER BY table_name`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*md.ViewDef
	for rows.Next() {
		var viewName, viewDef string
		if err := rows.Scan(&viewName, &viewDef); err != nil {
			return nil, err
		}
		views = append(views, &md.ViewDef{
			ViewSchema:     sch,
			ViewName:       viewName,
			ViewDefinition: viewDef,
		})
	}
	return views, rows.Err()
}

func (DuckDBMetadataQuerier) QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error) {
	sch := schema
	if sch == "" {
		sch = "main"
	}
	rows, err := db.Query(`
		SELECT sequencename, start_value, increment_by, min_value, max_value, cycle
		FROM pg_catalog.pg_sequences
		WHERE schemaname = ?
		ORDER BY sequencename`, sch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []*md.SequenceDef
	for rows.Next() {
		var seqName string
		var start, incr, min, max int
		var cycle bool
		if err := rows.Scan(&seqName, &start, &incr, &min, &max, &cycle); err != nil {
			return nil, err
		}
		cyc := "NO"
		if cycle {
			cyc = "YES"
		}
		seqs = append(seqs, &md.SequenceDef{
			SequenceSchema: sch,
			SequenceName:   seqName,
			StartValue:     start,
			IncrementBy:    incr,
			MinValue:       min,
			MaxValue:       max,
			Cycle:          cyc,
			CacheSize:      1,
		})
	}
	return seqs, rows.Err()
}

func (DuckDBMetadataQuerier) QueryTriggers(_ *sql.DB, _ string) ([]*md.TriggerDef, error) {
	return nil, nil // DuckDB does not support triggers
}

func (DuckDBMetadataQuerier) QuerySynonyms(_ *sql.DB, _ string) ([]*md.SynonymDef, error) {
	return nil, nil // DuckDB does not support synonyms
}

// splitCSL splits a comma-separated list, trimming whitespace.
func splitCSL(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	current := make([]rune, 0)
	parenDepth := 0
	for _, c := range s {
		switch c {
		case ',':
			if parenDepth == 0 {
				result = append(result, string(current))
				current = make([]rune, 0)
			} else {
				current = append(current, c)
			}
		case '(':
			parenDepth++
			current = append(current, c)
		case ')':
			parenDepth--
			current = append(current, c)
		default:
			current = append(current, c)
		}
	}
	if len(current) > 0 {
		result = append(result, string(current))
	}
	// Trim space from each result
	for i, r := range result {
		result[i] = strings.TrimSpace(r)
	}
	return result
}
