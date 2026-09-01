package extractor

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── Metadata Query SQL ──
// These constants expose the SQL used by each query method.
// They are referenced by the show-query command and by the method implementations.

const queryOracleTables = `SELECT t.table_name, t.tablespace_name, t.num_rows,
	NVL(c.comments, '') AS comments, t.temporary
FROM all_tables t
LEFT JOIN all_tab_comments c
	ON c.owner = t.owner AND c.table_name = t.table_name AND c.table_type = 'TABLE'
WHERE t.owner = UPPER(:1)
ORDER BY t.table_name`

const queryOracleColumns = `SELECT
	c.table_name,
	c.column_name,
	COALESCE(c.column_id, 0) AS ordinal_position,
	c.data_type,
	COALESCE(c.data_length, 0) AS data_length,
	COALESCE(c.data_precision, 0) AS data_precision,
	COALESCE(c.data_scale, 0) AS data_scale,
	c.nullable,
	c.data_default,
	cc.comments AS comments,
	COALESCE(c.char_used, '') AS char_used,
	COALESCE(c.character_set_name, '') AS charset,
	COALESCE(c.collation, '') AS collation
FROM all_tab_columns c
LEFT JOIN all_col_comments cc
	ON cc.owner = c.owner AND cc.table_name = c.table_name AND cc.column_name = c.column_name
WHERE c.owner = UPPER(:1)
ORDER BY c.table_name, c.column_id`

// queryOracleColumnsOceanBase is the OceanBase Oracle-compatible variant:
// all_tab_columns.collation does not exist there, so it is omitted.
const queryOracleColumnsOceanBase = `SELECT
	c.table_name,
	c.column_name,
	COALESCE(c.column_id, 0) AS ordinal_position,
	c.data_type,
	COALESCE(c.data_length, 0) AS data_length,
	COALESCE(c.data_precision, 0) AS data_precision,
	COALESCE(c.data_scale, 0) AS data_scale,
	c.nullable,
	c.data_default,
	cc.comments AS comments,
	COALESCE(c.char_used, '') AS char_used,
	COALESCE(c.character_set_name, '') AS charset
FROM all_tab_columns c
LEFT JOIN all_col_comments cc
	ON cc.owner = c.owner AND cc.table_name = c.table_name AND cc.column_name = c.column_name
WHERE c.owner = UPPER(:1)
ORDER BY c.table_name, c.column_id`

const queryOraclePrimaryKeys = `SELECT
	cc.table_name,
	cc.constraint_name,
	cc.column_name,
	cc.position
FROM all_cons_columns cc
JOIN all_constraints c
	ON cc.owner = c.owner
	AND cc.constraint_name = c.constraint_name
	AND cc.table_name = c.table_name
WHERE c.constraint_type = 'P'
	AND cc.owner = UPPER(:1)
ORDER BY cc.table_name, cc.constraint_name, cc.position`

const queryOracleIndexes = `SELECT
	i.table_name,
	i.index_name,
	CASE WHEN i.uniqueness = 'UNIQUE' THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
	ic.column_name,
	ic.column_position,
	CASE WHEN i.index_type = 'BITMAP' THEN 'BITMAP' ELSE 'BTREE' END AS index_type
FROM all_indexes i
JOIN all_ind_columns ic
	ON i.owner = ic.index_owner
	AND i.index_name = ic.index_name
	AND i.table_name = ic.table_name
WHERE i.owner = UPPER(:1)
ORDER BY i.table_name, i.index_name, ic.column_position`

const queryOracleForeignKeys = `SELECT
	cc.table_name,
	cc.constraint_name,
	cc.column_name,
	c.r_owner AS ref_owner,
	(SELECT table_name FROM all_constraints WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name) AS ref_table,
	(SELECT column_name FROM all_cons_columns WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name AND position = cc.position) AS ref_column,
	COALESCE(c.delete_rule, 'NO ACTION') AS delete_rule,
	COALESCE(c.deferrable, 'NOT DEFERRABLE') AS deferrable
FROM all_cons_columns cc
JOIN all_constraints c
	ON cc.owner = c.owner
	AND cc.constraint_name = c.constraint_name
	AND cc.table_name = c.table_name
WHERE c.constraint_type = 'R'
	AND cc.owner = UPPER(:1)
ORDER BY cc.table_name, cc.constraint_name, cc.position`

const queryOracleViews = `SELECT
	v.view_name,
	v.text AS view_definition,
	NVL(t.comments, '') AS view_comment,
	'NO' AS is_updatable,
	'' AS check_option,
	v.owner
FROM all_views v
LEFT JOIN all_tab_comments t
	ON v.owner = t.owner AND v.view_name = t.table_name
WHERE v.owner = UPPER(:1)
ORDER BY v.view_name`

const queryOracleSequences = `SELECT
	sequence_name,
	COALESCE(increment_by, 1) AS increment_by,
	COALESCE(min_value, 1) AS min_value,
	COALESCE(max_value, 9999999999999999999999999999) AS max_value,
	CASE WHEN cycle_flag = 'Y' THEN 'YES' ELSE 'NO' END AS cycle_flag,
	COALESCE(cache_size, 20) AS cache_size,
	COALESCE(last_number, 0) AS last_number,
	COALESCE(order_flag, 'NO') AS order_flag
FROM all_sequences
WHERE sequence_owner = UPPER(:1)
ORDER BY sequence_name`

const queryOracleTriggers = `SELECT
	trigger_name,
	table_owner,
	table_name,
	trigger_type,
	triggering_event,
	trigger_body,
	status,
	CASE WHEN trigger_type LIKE '%EACH ROW%' THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
	COALESCE(when_clause, '') AS when_clause,
	COALESCE(description, '') AS description
FROM all_triggers
WHERE owner = UPPER(:1)
ORDER BY trigger_name`

const queryOracleSynonyms = `SELECT
	synonym_name,
	owner,
	table_owner,
	table_name,
	CASE WHEN owner = 'PUBLIC' THEN 'YES' ELSE 'NO' END AS is_public
FROM all_synonyms
WHERE owner = UPPER(:1)
	OR table_owner = UPPER(:1)
ORDER BY synonym_name`

// OracleMetadataQuerier implements MetadataQuerier for Oracle using ALL_* dictionary views.
// Placeholder selects the bind style: "" keeps Oracle ":N" binds; "?" rewrites
// them for drivers speaking the MySQL wire protocol (OceanBase Oracle tenants).
type OracleMetadataQuerier struct {
	Placeholder string
	// OceanBase marks an Oracle-compatible OceanBase tenant, whose ALL_* dictionary
	// views expose a narrower column set than native Oracle:
	//   - all_tab_columns.collation does not exist (<21c) or is always NULL (OB);
	//   - all_tab_identity_cols does not exist in OceanBase at all.
	// When set, the columns query omits COLLATION and drops identity extraction,
	// which relies on all_tab_identity_cols / all_sequences (no reliable source in OB).
	OceanBase bool
}

func (OracleMetadataQuerier) Type() string { return "oracle" }

var oracleBindRe = regexp.MustCompile(`:\d+`)

func (q OracleMetadataQuerier) bind(sqlText string) string {
	if q.Placeholder != "?" {
		return sqlText
	}
	return oracleBindRe.ReplaceAllString(sqlText, "?")
}

// OceanBaseOracleWireQuerier extracts Oracle-compatible metadata from an
// OceanBase Oracle tenant reached over the MySQL wire protocol, which uses
// "?" placeholders instead of ":N".
type OceanBaseOracleWireQuerier struct{ OracleMetadataQuerier }

func (OceanBaseOracleWireQuerier) Type() string { return "oceanbase-oracle-wire" }

func (q OracleMetadataQuerier) QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT t.table_name, t.tablespace_name, t.num_rows,
			NVL(c.comments, '') AS comments, t.temporary
		FROM all_tables t
		LEFT JOIN all_tab_comments c
			ON c.owner = t.owner AND c.table_name = t.table_name AND c.table_type = 'TABLE'
		WHERE t.owner = UPPER(:1)
		ORDER BY t.table_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []*md.TableDef
	for rows.Next() {
		var tableName string
		// OceanBase Oracle-compatible tenants return NULL for these even though
		// native Oracle exposes a non-null value, so scan into NullString.
		var tablespace, temporary sql.NullString
		var numRows sql.NullInt64
		var comment sql.NullString
		if err := rows.Scan(&tableName, &tablespace, &numRows, &comment, &temporary); err != nil {
			return nil, err
		}
		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, err
		}
		tbl.Owner = schema
		tbl.TableType = "TABLE"
		tbl.Tablespace = tablespace.String
		tbl.TableComment = comment.String
		tbl.Temporary = temporary.String
		if numRows.Valid {
			tbl.RowCount = int(numRows.Int64)
		}
		tables = append(tables, tbl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	enrichOraclePartitions(db, schema, tables, q)
	return tables, nil
}

// enrichOraclePartitions reconstructs PARTITION BY definitions (best-effort:
// INTERVAL clauses and subpartitions are not rebuilt).
func enrichOraclePartitions(db *sql.DB, schema string, tables []*md.TableDef, q OracleMetadataQuerier) {
	byName := make(map[string]*md.TableDef, len(tables))
	for _, t := range tables {
		byName[t.TableName] = t
	}

	rows, err := db.Query(q.bind(`
		SELECT pt.table_name, pt.partitioning_type, pt.partition_count,
			LISTAGG(kc.column_name, ', ') WITHIN GROUP (ORDER BY kc.column_position) AS key_cols
		FROM all_part_tables pt
		LEFT JOIN all_part_key_columns kc
			ON kc.owner = pt.owner AND kc.name = pt.table_name AND kc.object_type = 'TABLE'
		WHERE pt.owner = UPPER(:1)
		GROUP BY pt.table_name, pt.partitioning_type, pt.partition_count`), schema)
	if err != nil {
		return
	}
	type partMeta struct {
		pType   string
		count   int
		keyCols string
	}
	metas := make(map[string]partMeta)
	for rows.Next() {
		var tableName, pType string
		var count int
		var keyCols sql.NullString
		if err := rows.Scan(&tableName, &pType, &count, &keyCols); err != nil {
			rows.Close()
			return
		}
		metas[tableName] = partMeta{pType: strings.ToUpper(pType), count: count, keyCols: keyCols.String}
	}
	rows.Close()

	for tableName, meta := range metas {
		tbl, ok := byName[tableName]
		if !ok {
			continue
		}
		tbl.Partitioned = "YES"
		tbl.PartitionInfo = buildOraclePartitionClause(db, schema, q, tableName, meta.pType, meta.count, meta.keyCols)
	}
}

func buildOraclePartitionClause(db *sql.DB, schema string, q OracleMetadataQuerier, table, pType string, count int, keyCols string) string {
	switch pType {
	case "HASH", "SYSTEM":
		if keyCols == "" {
			keyCols = "/* key column unknown */"
		}
		return fmt.Sprintf("PARTITION BY %s(%s) PARTITIONS %d", pType, keyCols, count)
	case "RANGE", "LIST":
		verb := "VALUES LESS THAN"
		if pType == "LIST" {
			verb = "VALUES"
		}
		rows, err := db.Query(q.bind(`
			SELECT partition_name, high_value
			FROM all_tab_partitions
			WHERE table_owner = UPPER(:1) AND table_name = :2
			ORDER BY partition_position`), schema, table)
		if err != nil {
			return fmt.Sprintf("PARTITION BY %s(%s) PARTITIONS %d", pType, keyCols, count)
		}
		defer rows.Close()
		var b strings.Builder
		fmt.Fprintf(&b, "PARTITION BY %s(%s) (\n", pType, keyCols)
		first := true
		for rows.Next() {
			var partName string
			var highValue sql.NullString
			if err := rows.Scan(&partName, &highValue); err != nil {
				return fmt.Sprintf("PARTITION BY %s(%s) PARTITIONS %d", pType, keyCols, count)
			}
			if !first {
				b.WriteString(",\n")
			}
			first = false
			fmt.Fprintf(&b, "  PARTITION %s %s (%s)", partName, verb, highValue.String)
		}
		b.WriteString("\n)")
		return b.String()
	default:
		return ""
	}
}

func (q OracleMetadataQuerier) QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error) {
	// Native Oracle (21c+) exposes all_tab_columns.collation; OceanBase Oracle
	// tenants do not (ORA-00904 / always NULL), so drop the column there.
	defs := []string{
		"c.table_name",
		"c.column_name",
		"c.column_id AS ordinal_position",
		"c.data_type",
		"COALESCE(c.data_length, 0) AS data_length",
		"COALESCE(c.data_precision, 0) AS data_precision",
		"COALESCE(c.data_scale, 0) AS data_scale",
		"c.nullable",
		"c.data_default",
		"cc.comments AS comments",
		"COALESCE(c.char_used, '') AS char_used",
		"COALESCE(c.character_set_name, '') AS charset",
	}
	if !q.OceanBase {
		defs = append(defs,
			"COALESCE(c.collation, '') AS collation",
			// Identity metadata: IDENTITY_COLUMN lives on all_tab_columns,
			// GENERATION_TYPE on all_tab_identity_cols, start/increment on
			// the backing all_sequences row. Must stay in the same order the
			// native scanArgs append them.
			"c.identity_column",
			"ic.generation_type",
			"seq.min_value",
			"seq.increment_by",
		)
	}
	var joins strings.Builder
	joins.WriteString("LEFT JOIN all_col_comments cc\n\t\t\tON cc.owner = c.owner AND cc.table_name = c.table_name AND cc.column_name = c.column_name")
	if !q.OceanBase {
		// OceanBase has no all_tab_identity_cols view; identity metadata is
		// unavailable there, so only enrich for native Oracle.
		joins.WriteString("\n\t\tLEFT JOIN all_tab_identity_cols ic\n\t\t\tON ic.owner = c.owner AND ic.table_name = c.table_name AND ic.column_name = c.column_name")
		joins.WriteString("\n\t\tLEFT JOIN all_sequences seq\n\t\t\tON seq.sequence_owner = ic.owner AND seq.sequence_name = ic.sequence_name")
	}
	rows, err := db.Query(q.bind(fmt.Sprintf(`
		SELECT
			%s
		FROM all_tab_columns c
		%s
		WHERE c.owner = UPPER(:1)
		ORDER BY c.table_name, c.column_id`, strings.Join(defs, ",\n\t\t\t"), joins.String())), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []*md.ColumnDef
	curTable := ""
	pos := 0
	for rows.Next() {
		var tableName, colName, dataType, nullable, identityCol string
		// column_id can be NULL in OceanBase Oracle-compatible tenants; scan it
		// as NullInt64 and fall back to a per-table scan order so the column
		// keeps a valid (>=1) ordinal instead of failing the whole migration.
		var ordinal sql.NullInt64
		var dataLen, dataPrec, dataScale int
		var defaultVal, comments, charUsed, charset, collation sql.NullString
		var identGen sql.NullString
		var identStart, identIncr sql.NullInt64
		scanArgs := []any{
			&tableName, &colName, &ordinal, &dataType,
			&dataLen, &dataPrec, &dataScale, &nullable, &defaultVal, &comments,
			&charUsed, &charset,
		}
		if !q.OceanBase {
			scanArgs = append(scanArgs, &collation)
		}
		if !q.OceanBase {
			scanArgs = append(scanArgs, &identityCol, &identGen, &identStart, &identIncr)
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}

		// Map Oracle nullable format
		nullStr := "YES"
		if nullable == "N" {
			nullStr = "NO"
		}

		if tableName != curTable {
			curTable = tableName
			pos = 0
		}
		pos++
		// NewColumnDef rejects ordinals < 1; column_id is NULL in OceanBase
		// Oracle tenants, so fall back to the per-table scan order, which
		// matches ORDER BY c.table_name, c.column_id (NULLS LAST).
		colOrdinal := int(ordinal.Int64)
		if !ordinal.Valid || colOrdinal < 1 {
			colOrdinal = pos
		}

		col, err := md.NewColumnDef(schema, tableName, colName, colOrdinal, dataType)
		if err != nil {
			return nil, err
		}
		col.DataLength = dataLen
		col.DataPrecision = dataPrec
		col.DataScale = dataScale
		col.Nullable = nullStr
		col.DefaultValue = defaultVal.String
		col.ColumnComment = comments.String
		col.CharUsed = charUsed.String
		col.CharacterSet = charset.String
		if !q.OceanBase {
			col.Collation = collation.String
		}

		if identityCol == "YES" {
			col.IsIdentity = "YES"
			if identGen.Valid && identGen.String != "" {
				col.IdentityGeneration = strings.ToUpper(identGen.String)
			} else {
				col.IdentityGeneration = "ALWAYS"
			}
			if identStart.Valid {
				col.IdentityStart = int(identStart.Int64)
			}
			if identIncr.Valid {
				col.IdentityIncrement = int(identIncr.Int64)
			}
		}

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (q OracleMetadataQuerier) QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			cc.table_name,
			cc.constraint_name,
			cc.column_name,
			cc.position
		FROM all_cons_columns cc
		JOIN all_constraints c
			ON cc.owner = c.owner
			AND cc.constraint_name = c.constraint_name
			AND cc.table_name = c.table_name
		WHERE c.constraint_type = 'P'
		  AND cc.owner = UPPER(:1)
		ORDER BY cc.table_name, cc.constraint_name, cc.position`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pks []*md.PrimaryKeyDef
	for rows.Next() {
		var tableName, constraintName, columnName string
		var position int
		if err := rows.Scan(&tableName, &constraintName, &columnName, &position); err != nil {
			return nil, err
		}
		pks = append(pks, &md.PrimaryKeyDef{
			TableSchema:     schema,
			TableName:       tableName,
			ConstraintName:  constraintName,
			ColumnName:      columnName,
			OrdinalPosition: position,
		})
	}
	return pks, rows.Err()
}

func (q OracleMetadataQuerier) QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			i.table_name,
			i.index_name,
			CASE WHEN i.uniqueness = 'UNIQUE' THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
			ic.column_name,
			ic.column_position,
			CASE WHEN i.index_type = 'BITMAP' THEN 'BITMAP' ELSE 'BTREE' END AS index_type
		FROM all_indexes i
		JOIN all_ind_columns ic
			ON i.owner = ic.index_owner
			AND i.index_name = ic.index_name
			AND i.table_name = ic.table_name
		WHERE i.owner = UPPER(:1)
		ORDER BY i.table_name, i.index_name, ic.column_position`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []*md.IndexDef
	for rows.Next() {
		var tableName, indexName, uniqueness, columnName, indexType string
		var position int
		if err := rows.Scan(&tableName, &indexName, &uniqueness, &columnName, &position, &indexType); err != nil {
			return nil, err
		}
		indexes = append(indexes, &md.IndexDef{
			TableSchema:     schema,
			TableName:       tableName,
			IndexName:       indexName,
			IndexType:       indexType,
			Uniqueness:      uniqueness,
			ColumnName:      columnName,
			OrdinalPosition: position,
		})
	}
	return indexes, rows.Err()
}

func (q OracleMetadataQuerier) QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			cc.table_name,
			cc.constraint_name,
			cc.column_name,
			c.r_owner AS ref_owner,
			(SELECT table_name FROM all_constraints WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name) AS ref_table,
			(SELECT column_name FROM all_cons_columns WHERE owner = c.r_owner AND constraint_name = c.r_constraint_name AND position = cc.position) AS ref_column,
			COALESCE(c.delete_rule, 'NO ACTION') AS delete_rule,
			COALESCE(c.deferrable, 'NOT DEFERRABLE') AS deferrable
		FROM all_cons_columns cc
		JOIN all_constraints c
			ON cc.owner = c.owner
			AND cc.constraint_name = c.constraint_name
			AND cc.table_name = c.table_name
		WHERE c.constraint_type = 'R'
		  AND cc.owner = UPPER(:1)
		ORDER BY cc.table_name, cc.constraint_name, cc.position`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []*md.ForeignKeyDef
	for rows.Next() {
		var tableName, constraintName, columnName, refOwner, refTable, refColumn, deleteRule, deferrable string
		if err := rows.Scan(&tableName, &constraintName, &columnName, &refOwner, &refTable, &refColumn,
			&deleteRule, &deferrable); err != nil {
			return nil, err
		}
		fks = append(fks, &md.ForeignKeyDef{
			ConstraintName: constraintName,
			TableSchema:    schema,
			TableName:      tableName,
			ColumnName:     columnName,
			RefSchema:      refOwner,
			RefTable:       refTable,
			RefColumn:      refColumn,
			DeleteRule:     deleteRule,
			Deferrable:     deferrable,
		})
	}
	return fks, rows.Err()
}

func (q OracleMetadataQuerier) QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			v.view_name,
			NVL(v.text, '') AS view_definition,
			NVL(t.comments, '') AS view_comment,
			'NONE' AS is_updatable,
			'NONE' AS check_option,
			v.owner
		FROM all_views v
		LEFT JOIN all_tab_comments t
			ON v.owner = t.owner AND v.view_name = t.table_name
		WHERE v.owner = UPPER(:1)
		ORDER BY v.view_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*md.ViewDef
	for rows.Next() {
		var viewName, viewDef, updatable, checkOption, owner string
		var viewComment sql.NullString
		if err := rows.Scan(&viewName, &viewDef, &viewComment, &updatable, &checkOption, &owner); err != nil {
			return nil, err
		}
		vc := viewComment.String
		views = append(views, &md.ViewDef{
			ViewSchema:     schema,
			ViewName:       viewName,
			ViewDefinition: viewDef,
			ViewComment:    vc,
			IsUpdatable:    updatable,
			CheckOption:    checkOption,
			Owner:          owner,
		})
	}
	return views, rows.Err()
}

func (q OracleMetadataQuerier) QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			sequence_name,
			COALESCE(increment_by, 1) AS increment_by,
			COALESCE(min_value, 1) AS min_value,
			COALESCE(max_value, 9999999999999999999999999999) AS max_value,
			CASE WHEN cycle_flag = 'Y' THEN 'YES' ELSE 'NO' END AS cycle_flag,
			COALESCE(cache_size, 20) AS cache_size,
			COALESCE(last_number, 0) AS last_number,
			COALESCE(order_flag, 'NO') AS order_flag
		FROM all_sequences
		WHERE sequence_owner = UPPER(:1)
		ORDER BY sequence_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []*md.SequenceDef
	for rows.Next() {
		var seqName, cycleFlag, orderFlag string
		var increment, minVal, maxVal, cache, lastVal int
		if err := rows.Scan(&seqName, &increment, &minVal, &maxVal, &cycleFlag, &cache, &lastVal, &orderFlag); err != nil {
			return nil, err
		}
		// Oracle exposes no original START WITH once a sequence has been
		// consumed; last_number is the next value to dispense. Starting the
		// migrated sequence there avoids colliding with already-generated keys.
		start := lastVal
		if start <= 0 {
			start = minVal
		}
		seqs = append(seqs, &md.SequenceDef{
			SequenceSchema: schema,
			SequenceName:   seqName,
			StartValue:     start,
			IncrementBy:    increment,
			MinValue:       minVal,
			MaxValue:       maxVal,
			Cycle:          cycleFlag,
			CacheSize:      cache,
			CurrentValue:   lastVal,
			OrderFlag:      orderFlag,
		})
	}
	return seqs, rows.Err()
}

func (q OracleMetadataQuerier) QueryTriggers(db *sql.DB, schema string) ([]*md.TriggerDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			trigger_name,
			table_owner,
			table_name,
			trigger_type,
			triggering_event,
			trigger_body,
			status,
			CASE WHEN trigger_type LIKE '%EACH ROW%' THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
			COALESCE(when_clause, '') AS when_clause,
			COALESCE(description, '') AS description
		FROM all_triggers
		WHERE owner = UPPER(:1)
		ORDER BY trigger_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*md.TriggerDef
	for rows.Next() {
		var triggerName, tableOwner, tableName, triggerType, triggerEvent, triggerBody, status, forEach string
		var whenClause, description sql.NullString
		if err := rows.Scan(&triggerName, &tableOwner, &tableName, &triggerType, &triggerEvent,
			&triggerBody, &status, &forEach, &whenClause, &description); err != nil {
			return nil, err
		}
		triggers = append(triggers, &md.TriggerDef{
			TriggerSchema: schema,
			TriggerName:   triggerName,
			TableSchema:   tableOwner,
			TableName:     tableName,
			TriggerType:   triggerType,
			TriggerEvent:  triggerEvent,
			TriggerBody:   triggerBody,
			Status:        status,
			ForEach:       forEach,
			WhenClause:    whenClause.String,
			Description:   description.String,
			Language:      "PLSQL",
		})
	}
	return triggers, rows.Err()
}

func (q OracleMetadataQuerier) QuerySynonyms(db *sql.DB, schema string) ([]*md.SynonymDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT
			synonym_name,
			owner,
			table_owner,
			table_name,
			CASE WHEN owner = 'PUBLIC' THEN 'YES' ELSE 'NO' END AS is_public
		FROM all_synonyms
		WHERE owner = UPPER(:1)
		   OR table_owner = UPPER(:1)
		ORDER BY synonym_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var synonyms []*md.SynonymDef
	for rows.Next() {
		var synName, synOwner, tblOwner, tblName, isPublic string
		if err := rows.Scan(&synName, &synOwner, &tblOwner, &tblName, &isPublic); err != nil {
			return nil, err
		}
		synonyms = append(synonyms, &md.SynonymDef{
			SynonymName:   synName,
			SynonymSchema: synOwner,
			TargetSchema:  tblOwner,
			TargetName:    tblName,
			IsPublic:      isPublic,
		})
	}
	return synonyms, rows.Err()
}

func (q OracleMetadataQuerier) QueryFunctions(db *sql.DB, schema string) ([]*md.FunctionDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT object_name, object_type, status
		FROM all_objects
		WHERE owner = UPPER(:1)
		  AND object_type IN ('FUNCTION', 'PROCEDURE')
		  AND generated = 'N'
		ORDER BY object_name`), schema)
	if err != nil {
		return nil, err
	}
	type obj struct{ name, typ, status string }
	var objs []obj
	for rows.Next() {
		var o obj
		if err := rows.Scan(&o.name, &o.typ, &o.status); err != nil {
			rows.Close()
			return nil, err
		}
		objs = append(objs, o)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var funcs []*md.FunctionDef
	for _, o := range objs {
		fn := &md.FunctionDef{
			FunctionSchema: schema,
			FunctionName:   o.name,
			FunctionType:   o.typ,
			Language:       "PLSQL",
			Status:         o.status,
		}
		ddl, derr := oracleObjectDDL(db, q, o.typ, o.name, schema)
		if derr == nil && strings.TrimSpace(ddl) != "" {
			fn.FunctionBody = strings.TrimSpace(ddl)
		} else {
			src, serr := oracleSourceText(db, q, schema, o.name, o.typ)
			if serr != nil {
				return nil, serr
			}
			fn.FunctionBody = src
		}
		if o.typ == "FUNCTION" {
			fn.ReturnType = oracleFunctionReturnType(db, q, schema, o.name)
		}
		funcs = append(funcs, fn)
	}
	return funcs, nil
}

func (q OracleMetadataQuerier) QueryMViews(db *sql.DB, schema string) ([]*md.MViewDef, error) {
	rows, err := db.Query(q.bind(`
		SELECT mv.mview_name, mv.query, mv.refresh_method, mv.refresh_mode, mv.build_mode,
			NVL(c.comments, '') AS comments
		FROM all_mviews mv
		LEFT JOIN all_tab_comments c
			ON c.owner = mv.owner AND c.table_name = mv.mview_name
		WHERE mv.owner = UPPER(:1)
		ORDER BY mv.mview_name`), schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mviews []*md.MViewDef
	for rows.Next() {
		var name, method, mode, buildMode string
		var query, comment sql.NullString
		if err := rows.Scan(&name, &query, &method, &mode, &buildMode, &comment); err != nil {
			return nil, err
		}
		mviews = append(mviews, &md.MViewDef{
			MViewSchema:   schema,
			MViewName:     name,
			MViewQuery:    query.String,
			RefreshMethod: method,
			RefreshMode:   mode,
			BuildMode:     buildMode,
			MViewComment:  comment.String,
		})
	}
	return mviews, rows.Err()
}

func (q OracleMetadataQuerier) QueryPackages(db *sql.DB, schema string) ([]*md.PackageDef, error) {
	names, err := oracleObjectNames(db, q, schema, "PACKAGE")
	if err != nil {
		return nil, err
	}
	var pkgs []*md.PackageDef
	for _, name := range names {
		spec, derr := oracleObjectDDL(db, q, "PACKAGE", name, schema)
		if derr != nil {
			spec, derr = oracleSourceText(db, q, schema, name, "PACKAGE")
			if derr != nil {
				return nil, derr
			}
		}
		pkgs = append(pkgs, &md.PackageDef{
			PackageSchema: schema,
			PackageName:   name,
			PackageSpec:   strings.TrimSpace(spec),
			Status:        "ENABLED",
		})
	}
	return pkgs, nil
}

func (q OracleMetadataQuerier) QueryPackageBodies(db *sql.DB, schema string) ([]*md.PackageBodyDef, error) {
	names, err := oracleObjectNames(db, q, schema, "PACKAGE BODY")
	if err != nil {
		return nil, err
	}
	var bodies []*md.PackageBodyDef
	for _, name := range names {
		body, derr := oracleObjectDDL(db, q, "PACKAGE_BODY", name, schema)
		if derr != nil {
			body, derr = oracleSourceText(db, q, schema, name, "PACKAGE BODY")
			if derr != nil {
				return nil, derr
			}
		}
		bodies = append(bodies, &md.PackageBodyDef{
			PackageSchema: schema,
			PackageName:   name,
			PackageBody:   strings.TrimSpace(body),
			Status:        "ENABLED",
		})
	}
	return bodies, nil
}

func oracleObjectNames(db *sql.DB, q OracleMetadataQuerier, schema, objectType string) ([]string, error) {
	rows, err := db.Query(q.bind(`
		SELECT object_name
		FROM all_objects
		WHERE owner = UPPER(:1) AND object_type = :2 AND generated = 'N'
		ORDER BY object_name`), schema, objectType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// oracleObjectDDL fetches full DDL via DBMS_METADATA (comments included).
func oracleObjectDDL(db *sql.DB, q OracleMetadataQuerier, objectType, name, schema string) (string, error) {
	var ddl string
	err := db.QueryRow(q.bind(`SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM DUAL`),
		objectType, name, strings.ToUpper(schema)).Scan(&ddl)
	if err != nil {
		return "", err
	}
	return ddl, nil
}

// oracleSourceText aggregates all_source lines for one object.
func oracleSourceText(db *sql.DB, q OracleMetadataQuerier, schema, name, objectType string) (string, error) {
	rows, err := db.Query(q.bind(`
		SELECT text
		FROM all_source
		WHERE owner = UPPER(:1) AND name = :2 AND UPPER(type) = UPPER(:3)
		ORDER BY line`), schema, name, objectType)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		b.WriteString(line)
	}
	return b.String(), rows.Err()
}

func oracleFunctionReturnType(db *sql.DB, q OracleMetadataQuerier, schema, name string) string {
	var dataType string
	err := db.QueryRow(q.bind(`
		SELECT data_type
		FROM all_arguments
		WHERE owner = UPPER(:1) AND object_name = :2 AND position = 0 AND data_level = 0`),
		schema, name).Scan(&dataType)
	if err != nil {
		return ""
	}
	return dataType
}
