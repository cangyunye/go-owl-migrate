package extractor

import (
	"database/sql"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// MySQLMetadataQuerier implements MetadataQuerier for MySQL using information_schema.
type MySQLMetadataQuerier struct{}

func (MySQLMetadataQuerier) Type() string { return "mysql" }

func (MySQLMetadataQuerier) QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error) {
	rows, err := db.Query(`
		SELECT table_name, engine, table_comment, row_format, table_collation,
		       COALESCE(create_options, ''),
		       COALESCE(IF(row_format = 'TEMPORARY', 'YES', 'NO'), 'NO') AS temporary
		FROM information_schema.tables
		WHERE table_schema = ?
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []*md.TableDef
	for rows.Next() {
		var tableName, engine, tableComment, rowFormat, collation, createOptions, temporary string
		if err := rows.Scan(&tableName, &engine, &tableComment, &rowFormat, &collation, &createOptions, &temporary); err != nil {
			return nil, err
		}

		// Extract partition info from create_options
		partitionInfo := ""
		partitioned := "NO"
		if strings.Contains(strings.ToUpper(createOptions), "PARTITION") {
			partitioned = "YES"
			partitionInfo = createOptions
		}

		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, err
		}
		tbl.Engine = engine
		tbl.TableComment = tableComment
		tbl.RowFormat = rowFormat
		tbl.Collation = collation
		tbl.Partitioned = partitioned
		tbl.PartitionInfo = partitionInfo
		tbl.Tablespace = ""
		tbl.Temporary = temporary
		tbl.Owner = schema
		tbl.TableType = "TABLE"

		// Fetch row count estimate
		var estRows int
		_ = db.QueryRow(`SELECT table_rows FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`, schema, tableName).Scan(&estRows)
		tbl.RowCount = estRows

		tables = append(tables, tbl)
	}
	return tables, rows.Err()
}

func (MySQLMetadataQuerier) QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error) {
	rows, err := db.Query(`
		SELECT
			table_name,
			column_name,
			ordinal_position,
			data_type,
			COALESCE(character_maximum_length, 0) AS char_length,
			COALESCE(numeric_precision, 0) AS num_precision,
			COALESCE(numeric_scale, 0) AS num_scale,
			is_nullable,
			COALESCE(column_default, '') AS column_default,
			COALESCE(column_comment, '') AS column_comment,
			COALESCE(extra, '') AS extra,
			COALESCE(character_set_name, '') AS charset,
			COALESCE(collation_name, '') AS collation,
			COALESCE(column_type, '') AS column_type_raw
		FROM information_schema.columns
		WHERE table_schema = ?
		ORDER BY table_name, ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []*md.ColumnDef
	for rows.Next() {
		var tableName, colName, dataType, nullable, defaultVal, comment, extra, charset, collation, colTypeRaw string
		var ordinal, charLen, numPrec, numScale int
		if err := rows.Scan(&tableName, &colName, &ordinal, &dataType,
			&charLen, &numPrec, &numScale, &nullable, &defaultVal, &comment,
			&extra, &charset, &collation, &colTypeRaw); err != nil {
			return nil, err
		}

		col, err := md.NewColumnDef(schema, tableName, colName, ordinal, dataType)
		if err != nil {
			return nil, err
		}

		col.DataLength = charLen
		col.DataPrecision = numPrec
		col.DataScale = numScale
		col.Nullable = nullable
		col.DefaultValue = defaultVal
		col.ColumnComment = comment
		col.CharacterSet = charset
		col.Collation = collation

		// Detect identity/auto_increment
		if strings.Contains(strings.ToLower(extra), "auto_increment") {
			col.IsIdentity = "YES"
			col.IdentityGeneration = "BY DEFAULT"
		}

		// On update
		if strings.Contains(strings.ToLower(extra), "on update") {
			col.OnUpdate = strings.TrimPrefix(extra, "on update ")
		}

		// Enum values from column_type raw (e.g. "enum('a','b')")
		if strings.HasPrefix(strings.ToLower(colTypeRaw), "enum(") {
			col.EnumValues = colTypeRaw
		}

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (MySQLMetadataQuerier) QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error) {
	rows, err := db.Query(`
		SELECT
			tc.table_name,
			tc.constraint_name,
			kcu.column_name,
			kcu.ordinal_position
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_schema = kcu.constraint_schema
			AND tc.constraint_name = kcu.constraint_name
			AND tc.table_name = kcu.table_name
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = ?
		ORDER BY tc.table_name, kcu.ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pks []*md.PrimaryKeyDef
	for rows.Next() {
		var tableName, constraintName, columnName string
		var ordinal int
		if err := rows.Scan(&tableName, &constraintName, &columnName, &ordinal); err != nil {
			return nil, err
		}
		pks = append(pks, &md.PrimaryKeyDef{
			TableSchema:     schema,
			TableName:       tableName,
			ConstraintName:  constraintName,
			ColumnName:      columnName,
			OrdinalPosition: ordinal,
		})
	}
	return pks, rows.Err()
}

func (MySQLMetadataQuerier) QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error) {
	// MySQL uses SHOW INDEX or information_schema.statistics
	rows, err := db.Query(`
		SELECT
			table_name,
			index_name,
			CASE WHEN non_unique = 0 THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
			column_name,
			seq_in_index,
			COALESCE(index_type, 'BTREE') AS index_type,
			COALESCE(expression, '') AS expression
		FROM information_schema.statistics
		WHERE table_schema = ?
		ORDER BY table_name, index_name, seq_in_index`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []*md.IndexDef
	for rows.Next() {
		var tableName, indexName, uniqueness, columnName string
		var ordinal int
		var indexType, expression string
		if err := rows.Scan(&tableName, &indexName, &uniqueness, &columnName, &ordinal, &indexType, &expression); err != nil {
			return nil, err
		}
		indexes = append(indexes, &md.IndexDef{
			TableSchema:     schema,
			TableName:       tableName,
			IndexName:       indexName,
			IndexType:       indexType,
			Uniqueness:      uniqueness,
			ColumnName:      columnName,
			OrdinalPosition: ordinal,
			Expression:      expression,
		})
	}
	return indexes, rows.Err()
}

func (MySQLMetadataQuerier) QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error) {
	rows, err := db.Query(`
		SELECT
			kcu.table_name,
			kcu.constraint_name,
			kcu.column_name,
			kcu.referenced_table_schema,
			kcu.referenced_table_name,
			kcu.referenced_column_name,
			COALESCE(rc.delete_rule, '') AS delete_rule,
			COALESCE(rc.update_rule, '') AS update_rule
		FROM information_schema.key_column_usage kcu
		LEFT JOIN information_schema.referential_constraints rc
			ON kcu.constraint_schema = rc.constraint_schema
			AND kcu.constraint_name = rc.constraint_name
		WHERE kcu.table_schema = ?
		  AND kcu.referenced_table_name IS NOT NULL
		ORDER BY kcu.table_name, kcu.constraint_name, kcu.ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []*md.ForeignKeyDef
	for rows.Next() {
		var tableName, constraintName, columnName, refSchema, refTable, refColumn, deleteRule, updateRule string
		if err := rows.Scan(&tableName, &constraintName, &columnName, &refSchema, &refTable, &refColumn,
			&deleteRule, &updateRule); err != nil {
			return nil, err
		}
		fks = append(fks, &md.ForeignKeyDef{
			ConstraintName: constraintName,
			TableSchema:    schema,
			TableName:      tableName,
			ColumnName:     columnName,
			RefSchema:      refSchema,
			RefTable:       refTable,
			RefColumn:      refColumn,
			DeleteRule:     deleteRule,
			UpdateRule:     updateRule,
		})
	}
	return fks, rows.Err()
}

func (MySQLMetadataQuerier) QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error) {
	rows, err := db.Query(`
		SELECT
			v.table_name,
			v.view_definition,
			COALESCE(t.table_comment, '') AS view_comment,
			CASE WHEN v.is_updatable = 'YES' THEN 'YES' ELSE 'NO' END AS is_updatable,
			CASE WHEN v.check_option = 'NONE' THEN '' ELSE COALESCE(v.check_option, '') END AS check_option
		FROM information_schema.views v
		LEFT JOIN information_schema.tables t
			ON v.table_schema = t.table_schema AND v.table_name = t.table_name
		WHERE v.table_schema = ?
		ORDER BY v.table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*md.ViewDef
	for rows.Next() {
		var viewName, viewDef, comment, updatable, checkOption string
		if err := rows.Scan(&viewName, &viewDef, &comment, &updatable, &checkOption); err != nil {
			return nil, err
		}
		views = append(views, &md.ViewDef{
			ViewSchema:     schema,
			ViewName:       viewName,
			ViewDefinition: viewDef,
			ViewComment:    comment,
			IsUpdatable:    updatable,
			CheckOption:    checkOption,
			Owner:          schema,
		})
	}
	return views, rows.Err()
}

func (MySQLMetadataQuerier) QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error) {
	return nil, nil // MySQL 8.0 does not have sequences as native objects
}

func (MySQLMetadataQuerier) QueryTriggers(db *sql.DB, schema string) ([]*md.TriggerDef, error) {
	rows, err := db.Query(`
		SELECT
			trigger_name,
			event_object_table AS table_name,
			action_timing AS trigger_type,
			event_manipulation AS trigger_event,
			action_statement AS trigger_body,
			action_condition AS when_clause,
			'',
			'PLSQL' AS language
		FROM information_schema.triggers
		WHERE trigger_schema = ?
		ORDER BY trigger_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*md.TriggerDef
	for rows.Next() {
		var triggerName, tableName, triggerType, triggerEvent, triggerBody, whenClause, description, language string
		if err := rows.Scan(&triggerName, &tableName, &triggerType, &triggerEvent,
			&triggerBody, &whenClause, &description, &language); err != nil {
			return nil, err
		}
		triggers = append(triggers, &md.TriggerDef{
			TriggerSchema: schema,
			TriggerName:   triggerName,
			TableSchema:   schema,
			TableName:     tableName,
			TriggerType:   triggerType,
			TriggerEvent:  triggerEvent,
			TriggerBody:   triggerBody,
			Status:        "ENABLED",
			ForEach:       "ROW",
			WhenClause:    whenClause,
			Description:   description,
			Language:      language,
		})
	}
	return triggers, rows.Err()
}

func (MySQLMetadataQuerier) QuerySynonyms(db *sql.DB, schema string) ([]*md.SynonymDef, error) {
	return nil, nil // MySQL does not have synonyms
}
