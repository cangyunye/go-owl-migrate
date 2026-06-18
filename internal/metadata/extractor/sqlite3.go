//go:build sqlite3

package extractor

import (
	"database/sql"
	"fmt"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// SQLite3MetadataQuerier extracts metadata from SQLite3 databases.
// SQLite3 has no schema concept — all objects live in sqlite_master.
// The schema parameter is accepted by the interface but always ignored.
// Metadata is queried via sqlite_master table + PRAGMA commands.
// Note: PRAGMA commands do not support ? placeholders; use fmt.Sprintf.
type SQLite3MetadataQuerier struct{}

func (SQLite3MetadataQuerier) Type() string { return "sqlite3" }

func (SQLite3MetadataQuerier) QueryTables(db *sql.DB, _ string) ([]*md.TableDef, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
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
		tbl, err := md.NewTableDef("main", tableName)
		if err != nil {
			return nil, err
		}
		tables = append(tables, tbl)
	}
	return tables, rows.Err()
}

func (SQLite3MetadataQuerier) QueryColumns(db *sql.DB, _ string) ([]*md.ColumnDef, error) {
	// First get all tables
	tables, err := SQLite3MetadataQuerier{}.QueryTables(db, "")
	if err != nil {
		return nil, err
	}

	var columns []*md.ColumnDef
	for _, tbl := range tables {
		rows, err := db.Query(fmt.Sprintf("PRAGMA table_info('%s')", tbl.TableName))
		if err != nil {
			return nil, fmt.Errorf("pragma table_info(%s): %w", tbl.TableName, err)
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, dataType string
			var dfltValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notnull, &dfltValue, &pk); err != nil {
				rows.Close()
				return nil, err
			}
			nullable := "YES"
			if notnull == 1 {
				nullable = "NO"
			}
			defVal := ""
			if dfltValue.Valid {
				defVal = dfltValue.String
			}
			col, err := md.NewColumnDef("main", tbl.TableName, name, cid+1, dataType)
			if err != nil {
				rows.Close()
				return nil, err
			}
			col.Nullable = nullable
			col.DefaultValue = defVal
			columns = append(columns, col)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (SQLite3MetadataQuerier) QueryPrimaryKeys(db *sql.DB, _ string) ([]*md.PrimaryKeyDef, error) {
	tables, err := SQLite3MetadataQuerier{}.QueryTables(db, "")
	if err != nil {
		return nil, err
	}

	var pks []*md.PrimaryKeyDef
	for _, tbl := range tables {
		rows, err := db.Query(fmt.Sprintf("PRAGMA table_info('%s')", tbl.TableName))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, dataType string
			var dfltValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notnull, &dfltValue, &pk); err != nil {
				rows.Close()
				return nil, err
			}
			if pk > 0 {
				pks = append(pks, &md.PrimaryKeyDef{
					TableSchema:      "main",
					TableName:        tbl.TableName,
					ConstraintName:   "pk_" + tbl.TableName,
					ColumnName:       name,
					OrdinalPosition:  pk,
				})
			}
		}
		rows.Close()
	}
	return pks, nil
}

func (SQLite3MetadataQuerier) QueryIndexes(db *sql.DB, _ string) ([]*md.IndexDef, error) {
	tables, err := SQLite3MetadataQuerier{}.QueryTables(db, "")
	if err != nil {
		return nil, err
	}

	var indexes []*md.IndexDef
	for _, tbl := range tables {
		rows, err := db.Query(fmt.Sprintf("PRAGMA index_list('%s')", tbl.TableName))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var seq int
			var indexName, uniqueStr string
			var origin, partial interface{}
			if err := rows.Scan(&seq, &indexName, &uniqueStr, &origin, &partial); err != nil {
				rows.Close()
				return nil, err
			}
			// Skip auto-generated indexes (origin = 'pk')
			if o, ok := origin.(string); ok && o == "pk" {
				continue
			}

			uniqueness := "NONUNIQUE"
			if strings.EqualFold(uniqueStr, "1") || strings.EqualFold(uniqueStr, "yes") {
				uniqueness = "UNIQUE"
			}

			// Get index columns
			colRows, err := db.Query(fmt.Sprintf("PRAGMA index_info('%s')", indexName))
			if err != nil {
				rows.Close()
				return nil, err
			}
			for colRows.Next() {
				var pos, cid int
				var colName string
				if err := colRows.Scan(&pos, &cid, &colName); err != nil {
					colRows.Close()
					rows.Close()
					return nil, err
				}
				indexes = append(indexes, &md.IndexDef{
					TableSchema:     "main",
					TableName:       tbl.TableName,
					IndexName:       indexName,
					IndexType:       "BTREE",
					Uniqueness:      uniqueness,
					ColumnName:      colName,
					OrdinalPosition: pos + 1,
				})
			}
			colRows.Close()
		}
		rows.Close()
	}
	return indexes, nil
}

func (SQLite3MetadataQuerier) QueryForeignKeys(db *sql.DB, _ string) ([]*md.ForeignKeyDef, error) {
	tables, err := SQLite3MetadataQuerier{}.QueryTables(db, "")
	if err != nil {
		return nil, err
	}

	var fks []*md.ForeignKeyDef
	for _, tbl := range tables {
		rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list('%s')", tbl.TableName))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, seq int
			var refTable, from, to, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				return nil, err
			}
			fks = append(fks, &md.ForeignKeyDef{
				TableSchema: "main",
				TableName:   tbl.TableName,
				ColumnName:  from,
				RefSchema:   "main",
				RefTable:    refTable,
				RefColumn:   to,
				DeleteRule:  onDelete,
				UpdateRule:  onUpdate,
			})
		}
		rows.Close()
	}
	return fks, nil
}

func (SQLite3MetadataQuerier) QueryViews(db *sql.DB, _ string) ([]*md.ViewDef, error) {
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type='view' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*md.ViewDef
	for rows.Next() {
		var viewName, viewSQL string
		if err := rows.Scan(&viewName, &viewSQL); err != nil {
			return nil, err
		}
		views = append(views, &md.ViewDef{
			ViewSchema:     "main",
			ViewName:       viewName,
			ViewDefinition: viewSQL,
		})
	}
	return views, rows.Err()
}

func (SQLite3MetadataQuerier) QueryTriggers(db *sql.DB, _ string) ([]*md.TriggerDef, error) {
	rows, err := db.Query(`SELECT name, sql, tbl_name FROM sqlite_master WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []*md.TriggerDef
	for rows.Next() {
		var triggerName, triggerSQL, tableName string
		if err := rows.Scan(&triggerName, &triggerSQL, &tableName); err != nil {
			return nil, err
		}
		// Parse trigger type and event from the CREATE TRIGGER statement
		triggerType, triggerEvent := parseTriggerInfo(triggerSQL)
		triggers = append(triggers, &md.TriggerDef{
			TriggerSchema: "main",
			TriggerName:   triggerName,
			TableSchema:   "main",
			TableName:     tableName,
			TriggerType:   triggerType,
			TriggerEvent:  triggerEvent,
			TriggerBody:   triggerSQL,
			Status:        "ENABLED",
			ForEach:       "ROW",
			Language:      "SQL",
		})
	}
	return triggers, rows.Err()
}

func (SQLite3MetadataQuerier) QuerySequences(_ *sql.DB, _ string) ([]*md.SequenceDef, error) {
	return nil, nil // SQLite3 does not support sequences
}

func (SQLite3MetadataQuerier) QuerySynonyms(_ *sql.DB, _ string) ([]*md.SynonymDef, error) {
	return nil, nil // SQLite3 does not support synonyms
}

// parseTriggerInfo extracts trigger type (BEFORE/AFTER/INSTEAD OF) and event
// (INSERT/UPDATE/DELETE) from a CREATE TRIGGER statement.
func parseTriggerInfo(triggerSQL string) (string, string) {
	upper := strings.ToUpper(triggerSQL)
	for _, tt := range []string{"BEFORE", "AFTER", "INSTEAD OF"} {
		idx := strings.Index(upper, tt)
		if idx < 0 {
			continue
		}
		after := upper[idx+len(tt):]
		for _, ev := range []string{"INSERT", "UPDATE", "DELETE"} {
			if strings.HasPrefix(strings.TrimSpace(after), ev) {
				return tt, ev
			}
		}
	}
	return "BEFORE", "INSERT"
}
