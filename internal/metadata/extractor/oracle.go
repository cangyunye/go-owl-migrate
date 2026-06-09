package extractor

import (
	"database/sql"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// OracleMetadataQuerier implements MetadataQuerier for Oracle using ALL_* dictionary views.
type OracleMetadataQuerier struct{}

func (OracleMetadataQuerier) Type() string { return "oracle" }

func (OracleMetadataQuerier) QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error) {
	rows, err := db.Query(`
		SELECT table_name, tablespace_name, num_rows
		FROM all_tables
		WHERE owner = UPPER(:1)
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []*md.TableDef
	for rows.Next() {
		var tableName, tablespace string
		var numRows sql.NullInt64
		if err := rows.Scan(&tableName, &tablespace, &numRows); err != nil {
			return nil, err
		}
		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, err
		}
		tbl.Owner = schema
		tbl.TableType = "TABLE"
		tbl.Tablespace = tablespace
		if numRows.Valid {
			tbl.RowCount = int(numRows.Int64)
		}
		tables = append(tables, tbl)
	}
	return tables, rows.Err()
}

func (OracleMetadataQuerier) QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error) {
	rows, err := db.Query(`
		SELECT
			table_name,
			column_name,
			column_id AS ordinal_position,
			data_type,
			COALESCE(data_length, 0) AS data_length,
			COALESCE(data_precision, 0) AS data_precision,
			COALESCE(data_scale, 0) AS data_scale,
			nullable,
			data_default,
			NVL(comments, '') AS comments,
			COALESCE(char_used, '') AS char_used,
			COALESCE(character_set_name, '') AS charset,
			COALESCE(collation, '') AS collation,
			identity_column
		FROM all_tab_columns c
		LEFT JOIN all_col_comments USING (owner, table_name, column_name)
		WHERE owner = UPPER(:1)
		ORDER BY table_name, column_id`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []*md.ColumnDef
	for rows.Next() {
		var tableName, colName, dataType, nullable, identityCol string
		var ordinal, dataLen, dataPrec, dataScale int
		var defaultVal, comments, charUsed, charset, collation sql.NullString
		if err := rows.Scan(&tableName, &colName, &ordinal, &dataType,
			&dataLen, &dataPrec, &dataScale, &nullable, &defaultVal, &comments,
			&charUsed, &charset, &collation, &identityCol); err != nil {
			return nil, err
		}

		// Map Oracle nullable format
		nullStr := "YES"
		if nullable == "N" {
			nullStr = "NO"
		}

		col, err := md.NewColumnDef(schema, tableName, colName, ordinal, dataType)
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
		col.Collation = collation.String

		if identityCol == "YES" {
			col.IsIdentity = "YES"
			col.IdentityGeneration = "ALWAYS"
		}

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (OracleMetadataQuerier) QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error) {
	rows, err := db.Query(`
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
		ORDER BY cc.table_name, cc.constraint_name, cc.position`, schema)
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

func (OracleMetadataQuerier) QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error) {
	rows, err := db.Query(`
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
		ORDER BY i.table_name, i.index_name, ic.column_position`, schema)
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

func (OracleMetadataQuerier) QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error) {
	rows, err := db.Query(`
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
		ORDER BY cc.table_name, cc.constraint_name, cc.position`, schema)
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

func (OracleMetadataQuerier) QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error) {
	rows, err := db.Query(`
		SELECT
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
		ORDER BY v.view_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*md.ViewDef
	for rows.Next() {
		var viewName, viewDef, viewComment, updatable, checkOption, owner string
		if err := rows.Scan(&viewName, &viewDef, &viewComment, &updatable, &checkOption, &owner); err != nil {
			return nil, err
		}
		views = append(views, &md.ViewDef{
			ViewSchema:     schema,
			ViewName:       viewName,
			ViewDefinition: viewDef,
			ViewComment:    viewComment,
			IsUpdatable:    updatable,
			CheckOption:    checkOption,
			Owner:          owner,
		})
	}
	return views, rows.Err()
}

func (OracleMetadataQuerier) QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error) {
	rows, err := db.Query(`
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
		ORDER BY sequence_name`, schema)
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
		seqs = append(seqs, &md.SequenceDef{
			SequenceSchema: schema,
			SequenceName:   seqName,
			StartValue:     1,
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

func (OracleMetadataQuerier) QueryTriggers(db *sql.DB, schema string) ([]*md.TriggerDef, error) {
	rows, err := db.Query(`
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
		ORDER BY trigger_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*md.TriggerDef
	for rows.Next() {
		var triggerName, tableOwner, tableName, triggerType, triggerEvent, triggerBody, status, forEach, whenClause, description string
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
			WhenClause:    whenClause,
			Description:   description,
			Language:      "PLSQL",
		})
	}
	return triggers, rows.Err()
}
