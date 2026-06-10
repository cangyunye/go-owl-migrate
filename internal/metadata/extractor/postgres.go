package extractor

import (
	"database/sql"
	"fmt"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// PGMetadataQuerier implements MetadataQuerier for PostgreSQL using information_schema.
type PGMetadataQuerier struct{}

func (PGMetadataQuerier) Type() string { return "postgres" }

func (PGMetadataQuerier) QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error) {
	rows, err := db.Query(`
		SELECT table_name, table_type
		FROM information_schema.tables
		WHERE table_schema = $1
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []*md.TableDef
	for rows.Next() {
		var tableName, tableType string
		if err := rows.Scan(&tableName, &tableType); err != nil {
			return nil, err
		}
		tbl, err := md.NewTableDef(schema, tableName)
		if err != nil {
			return nil, err
		}
		tbl.Owner = schema
		tbl.TableType = "TABLE"
		tables = append(tables, tbl)
	}
	return tables, rows.Err()
}

func (PGMetadataQuerier) QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error) {
	rows, err := db.Query(`
		SELECT
			c.table_name,
			c.column_name,
			c.ordinal_position,
			c.data_type,
			COALESCE(c.character_maximum_length, 0) AS char_length,
			COALESCE(c.numeric_precision, 0) AS num_precision,
			COALESCE(c.numeric_scale, 0) AS num_scale,
			c.is_nullable,
			COALESCE(c.column_default, '') AS column_default,
			COALESCE(pgd.description, '') AS column_comment,
			COALESCE(c.identity_generation, '') AS identity_generation,
			COALESCE(c.character_set_name, '') AS character_set_name,
			COALESCE(c.collation_name, '') AS collation_name
		FROM information_schema.columns c
		LEFT JOIN pg_catalog.pg_description pgd
			ON pgd.objsubid = c.ordinal_position
			AND pgd.objoid = (quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass::oid
		WHERE c.table_schema = $1
		ORDER BY c.table_name, c.ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []*md.ColumnDef
	for rows.Next() {
		var tableName, colName, dataType, nullable, defaultVal, comment, identityGen, charset, collation string
		var ordinal, charLen, numPrec, numScale int
		if err := rows.Scan(&tableName, &colName, &ordinal, &dataType,
			&charLen, &numPrec, &numScale, &nullable, &defaultVal, &comment,
			&identityGen, &charset, &collation); err != nil {
			return nil, err
		}

		col, err := md.NewColumnDef(schema, tableName, colName, ordinal, dataType)
		if err != nil {
			return nil, fmt.Errorf("column %s.%s: %w", schema, colName, err)
		}

		col.DataLength = charLen
		col.DataPrecision = numPrec
		col.DataScale = numScale
		col.Nullable = nullable
		col.DefaultValue = defaultVal
		col.ColumnComment = comment
		col.CharacterSet = charset
		col.Collation = collation

		if identityGen != "" {
			col.IsIdentity = "YES"
			col.IdentityGeneration = identityGen
		}

		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func (PGMetadataQuerier) QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error) {
	rows, err := db.Query(`
		SELECT
			tc.table_name,
			tc.constraint_name,
			kcu.column_name,
			kcu.ordinal_position
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_catalog = kcu.constraint_catalog
			AND tc.constraint_schema = kcu.constraint_schema
			AND tc.constraint_name = kcu.constraint_name
			AND tc.table_name = kcu.table_name
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = $1
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

func (PGMetadataQuerier) QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error) {
	rows, err := db.Query(`
		SELECT
			n.nspname AS schema_name,
			t.relname AS table_name,
			i.relname AS index_name,
			am.amname AS index_type,
			CASE WHEN ix.indisunique THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
			a.attname AS column_name,
			a.attnum AS ordinal_position,
			COALESCE(pg_get_expr(ix.indexprs, ix.indrelid), '') AS expression
		FROM pg_class t
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON ix.indexrelid = i.oid
		JOIN pg_am am ON i.relam = am.oid
		JOIN pg_namespace n ON t.relnamespace = n.oid
		LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
		WHERE n.nspname = $1
		  AND t.relkind = 'r'
		  AND NOT ix.indisprimary
		ORDER BY t.relname, i.relname, a.attnum`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []*md.IndexDef
	for rows.Next() {
		var schemaName, tableName, indexName, indexType, uniqueness, columnName, expr string
		var ordinal int
		if err := rows.Scan(&schemaName, &tableName, &indexName, &indexType,
			&uniqueness, &columnName, &ordinal, &expr); err != nil {
			return nil, err
		}
		if columnName == "" {
			continue // functional/partial index with expression only
		}
		indexes = append(indexes, &md.IndexDef{
			TableSchema:     schema,
			TableName:       tableName,
			IndexName:       indexName,
			IndexType:       indexType,
			Uniqueness:      uniqueness,
			ColumnName:      columnName,
			OrdinalPosition: ordinal,
			Expression:      expr,
		})
	}
	return indexes, rows.Err()
}

func (PGMetadataQuerier) QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error) {
	rows, err := db.Query(`
		SELECT
			tc.table_name,
			tc.constraint_name,
			kcu.column_name,
			ccu.table_schema AS ref_schema,
			ccu.table_name AS ref_table,
			ccu.column_name AS ref_column,
			COALESCE(rc.delete_rule, '') AS delete_rule,
			COALESCE(rc.update_rule, '') AS update_rule,
			COALESCE(tc.is_deferrable, 'NOT DEFERRABLE') AS deferrable
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_catalog = kcu.constraint_catalog
			AND tc.constraint_schema = kcu.constraint_schema
			AND tc.constraint_name = kcu.constraint_name
			AND tc.table_name = kcu.table_name
		JOIN information_schema.constraint_column_usage ccu
			ON tc.constraint_catalog = ccu.constraint_catalog
			AND tc.constraint_schema = ccu.constraint_schema
			AND tc.constraint_name = ccu.constraint_name
		LEFT JOIN information_schema.referential_constraints rc
			ON tc.constraint_catalog = rc.constraint_catalog
			AND tc.constraint_schema = rc.constraint_schema
			AND tc.constraint_name = rc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = $1
		ORDER BY tc.table_name, tc.constraint_name, kcu.ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []*md.ForeignKeyDef
	for rows.Next() {
		var tableName, constraintName, columnName, refSchema, refTable, refColumn, deleteRule, updateRule, deferrable string
		if err := rows.Scan(&tableName, &constraintName, &columnName, &refSchema, &refTable, &refColumn,
			&deleteRule, &updateRule, &deferrable); err != nil {
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
			Deferrable:     deferrable,
		})
	}
	return fks, rows.Err()
}

func (PGMetadataQuerier) QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error) {
	rows, err := db.Query(`
		SELECT
			table_name,
			view_definition,
			'' AS view_comment,
			CASE WHEN is_updatable = 'YES' THEN 'YES' ELSE 'NO' END AS is_updatable,
			CASE WHEN is_insertable_into = 'YES' THEN 'YES' ELSE 'NO' END AS check_option
		FROM information_schema.views
		WHERE table_schema = $1
		ORDER BY table_name`, schema)
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

func (PGMetadataQuerier) QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error) {
	rows, err := db.Query(`
		SELECT
			sequencename,
			start_value,
			increment_by,
			min_value,
			max_value,
			CASE WHEN cycle THEN 'YES' ELSE 'NO' END AS cycle,
			COALESCE(cache_size, 1) AS cache_size,
			COALESCE(last_value, start_value) AS last_value,
			data_type::text AS data_type
		FROM pg_sequences
		WHERE schemaname = $1
		ORDER BY sequencename`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []*md.SequenceDef
	for rows.Next() {
		var seqName, dataType string
		var startVal, increment, minVal, maxVal, cache, lastVal int
		var cycle string
		if err := rows.Scan(&seqName, &startVal, &increment, &minVal, &maxVal, &cycle, &cache, &lastVal, &dataType); err != nil {
			return nil, err
		}
		seqs = append(seqs, &md.SequenceDef{
			SequenceSchema: schema,
			SequenceName:   seqName,
			StartValue:     startVal,
			IncrementBy:    increment,
			MinValue:       minVal,
			MaxValue:       maxVal,
			Cycle:          cycle,
			CacheSize:      cache,
			CurrentValue:   lastVal,
			DataType:       dataType,
		})
	}
	return seqs, rows.Err()
}

func (PGMetadataQuerier) QueryTriggers(db *sql.DB, schema string) ([]*md.TriggerDef, error) {
	// PostgreSQL 不通过 information_schema 暴露 trigger body
	// 需要 pg_trigger + pg_proc
	rows, err := db.Query(`
		SELECT
			tg.tgname AS trigger_name,
			n.nspname AS table_schema,
			t.relname AS table_name,
			CASE
				WHEN tg.tgtype & 2 = 2 THEN 'BEFORE'
				WHEN tg.tgtype & 64 = 64 THEN 'INSTEAD OF'
				ELSE 'AFTER'
			END AS trigger_type,
			string_agg(DISTINCT CASE
				WHEN tg.tgtype & 4 = 4 THEN 'INSERT'
				WHEN tg.tgtype & 16 = 16 THEN 'UPDATE'
				WHEN tg.tgtype & 32 = 32 THEN 'DELETE'
				WHEN tg.tgtype & 128 = 128 THEN 'TRUNCATE'
			END, '/') AS trigger_event,
			pg_get_functiondef(tgfoid) AS trigger_body,
			CASE WHEN tg.tgenabled = 'O' THEN 'ENABLED' ELSE 'DISABLED' END AS status,
			CASE WHEN tg.tgtype & 1 = 1 THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
			COALESCE(pg_get_expr(tg.tgqual, tg.tgrelid), '') AS when_clause,
			COALESCE(obj_description(tg.oid, 'pg_trigger'), '') AS description,
			'PLPGSQL' AS language
		FROM pg_trigger tg
		JOIN pg_class t ON tg.tgrelid = t.oid
		JOIN pg_namespace n ON t.relnamespace = n.oid
		WHERE n.nspname = $1
		  AND NOT tg.tgisinternal
		GROUP BY tg.oid, tg.tgname, n.nspname, t.relname, tg.tgtype, tg.tgfoid, tg.tgenabled, tg.tgrelid, tg.tgqual
		ORDER BY tg.tgname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*md.TriggerDef
	for rows.Next() {
		var triggerName, tableSchema, tableName, triggerType, triggerEvent, triggerBody, status, forEach, whenClause, description, language string
		if err := rows.Scan(&triggerName, &tableSchema, &tableName, &triggerType,
			&triggerEvent, &triggerBody, &status, &forEach, &whenClause, &description, &language); err != nil {
			return nil, err
		}
		triggers = append(triggers, &md.TriggerDef{
			TriggerSchema: schema,
			TriggerName:   triggerName,
			TableSchema:   tableSchema,
			TableName:     tableName,
			TriggerType:   triggerType,
			TriggerEvent:  triggerEvent,
			TriggerBody:   triggerBody,
			Status:        status,
			ForEach:       forEach,
			WhenClause:    whenClause,
			Description:   description,
			Language:      language,
		})
	}
	return triggers, rows.Err()
}

func (PGMetadataQuerier) QuerySynonyms(db *sql.DB, schema string) ([]*md.SynonymDef, error) {
	return nil, nil // PostgreSQL does not have synonyms
}
