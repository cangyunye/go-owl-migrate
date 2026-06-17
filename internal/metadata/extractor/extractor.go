package extractor

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// MetadataQuerier defines per-dialect schema introspection queries.
// Each method returns the metadata objects for the given schema.
// Returning (nil, nil) means the object type is not supported by this querier.
type MetadataQuerier interface {
	Type() string
	QueryTables(db *sql.DB, schema string) ([]*md.TableDef, error)
	QueryColumns(db *sql.DB, schema string) ([]*md.ColumnDef, error)
	QueryPrimaryKeys(db *sql.DB, schema string) ([]*md.PrimaryKeyDef, error)
	QueryIndexes(db *sql.DB, schema string) ([]*md.IndexDef, error)
	QueryForeignKeys(db *sql.DB, schema string) ([]*md.ForeignKeyDef, error)
	QueryViews(db *sql.DB, schema string) ([]*md.ViewDef, error)
	QuerySequences(db *sql.DB, schema string) ([]*md.SequenceDef, error)
	QueryTriggers(db *sql.DB, schema string) ([]*md.TriggerDef, error)
	QuerySynonyms(db *sql.DB, schema string) ([]*md.SynonymDef, error)
}

var (
	queriersMu sync.RWMutex
	queriers   = make(map[string]MetadataQuerier)
)

func init() {
	Register(&PGMetadataQuerier{})
	Register(&MySQLMetadataQuerier{})
	Register(&OracleMetadataQuerier{})
}

// Register adds a querier to the global registry.
func Register(q MetadataQuerier) {
	queriersMu.Lock()
	defer queriersMu.Unlock()
	t := strings.ToLower(q.Type())
	if _, exists := queriers[t]; exists {
		panic(fmt.Sprintf("metadata querier %q already registered", t))
	}
	queriers[t] = q
}

// Get returns a registered querier by database type.
func Get(dbType string) (MetadataQuerier, error) {
	queriersMu.RLock()
	defer queriersMu.RUnlock()
	q, ok := queriers[strings.ToLower(dbType)]
	if !ok {
		return nil, fmt.Errorf("unsupported source database type %q", dbType)
	}
	return q, nil
}

// normalizeDBType maps compound dialect names (e.g. "goldendb-mysql", "oceanbase-oracle")
// to their base querier type.
func normalizeDBType(t string) string {
	switch {
	case strings.HasSuffix(t, "-mysql"):
		return "mysql"
	case strings.HasSuffix(t, "-oracle"):
		return "oracle"
	case t == "goldendb", t == "oceanbase":
		return "mysql"
	case t == "panweidb", t == "opengaussdb":
		return "postgres"
	default:
		return t
	}
}

// Extract connects to the database and retrieves full schema metadata.
// Returns a fully populated SchemaModel with table definitions, columns,
// primary keys, indexes, foreign keys, views, sequences, and triggers.
func Extract(db *sql.DB, dbType, schema string) (*md.SchemaModel, error) {
	querier, err := Get(normalizeDBType(dbType))
	if err != nil {
		return nil, err
	}

	sm := md.NewSchemaModel()

	// 1. Tables (required)
	tables, err := querier.QueryTables(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	for _, t := range tables {
		if err := sm.AddTable(t); err != nil {
			return nil, fmt.Errorf("add table %s.%s: %w", t.TableSchema, t.TableName, err)
		}
	}
	if len(tables) == 0 {
		return sm, fmt.Errorf("no tables found in schema %q", schema)
	}

	// 2. Columns
	columns, err := querier.QueryColumns(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}
	for _, col := range columns {
		tbl := sm.GetTable(col.TableSchema, col.TableName)
		if tbl != nil {
			tbl.AddColumn(col)
		}
	}

	// 3. Primary keys
	pks, err := querier.QueryPrimaryKeys(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query primary keys: %w", err)
	}
	for _, pk := range pks {
		tbl := sm.GetTable(pk.TableSchema, pk.TableName)
		if tbl != nil {
			tbl.AddPrimaryKey(pk.ConstraintName, pk.ColumnName)
		}
	}

	// 4. Indexes
	indexes, err := querier.QueryIndexes(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query indexes: %w", err)
	}
	for _, idx := range indexes {
		sm.AddIndex(idx)
	}

	// 5. Foreign keys
	fks, err := querier.QueryForeignKeys(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query foreign keys: %w", err)
	}
	for _, fk := range fks {
		sm.AddForeignKey(fk)
	}

	// 6. Views (optional)
	views, err := querier.QueryViews(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query views: %w", err)
	}
	for _, v := range views {
		sm.AddView(v)
	}

	// 7. Sequences (optional)
	seqs, err := querier.QuerySequences(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query sequences: %w", err)
	}
	for _, seq := range seqs {
		sm.AddSequence(seq)
	}

	// 8. Triggers (optional)
	triggers, err := querier.QueryTriggers(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query triggers: %w", err)
	}
	for _, trg := range triggers {
		sm.AddTrigger(trg)
	}

	// 9. Synonyms (optional)
	synonyms, err := querier.QuerySynonyms(db, schema)
	if err != nil {
		return nil, fmt.Errorf("query synonyms: %w", err)
	}
	for _, syn := range synonyms {
		sm.AddSynonym(syn)
	}

	return sm, nil
}

// GetQuerySQL returns the SQL query used for a given dialect and object type.
// Object type values: tables, columns, pk, indexes, fk, views, sequences, triggers, synonyms.
// Returns empty string if unknown.
func GetQuerySQL(dbType, objectType string) string {
	base := normalizeDBType(dbType)
	ot := strings.ToLower(objectType)

	if base == "oracle" {
		switch ot {
		case "tables":    return queryOracleTables
		case "columns":   return queryOracleColumns
		case "pk", "primary_keys": return queryOraclePrimaryKeys
		case "indexes":   return queryOracleIndexes
		case "fk", "foreign_keys": return queryOracleForeignKeys
		case "views":     return queryOracleViews
		case "sequences": return queryOracleSequences
		case "triggers":  return queryOracleTriggers
		case "synonyms":  return queryOracleSynonyms
		}
	}
	if base == "postgres" {
		switch ot {
		case "tables":    return queryPGTables
		case "columns":   return queryPGColumns
		case "pk", "primary_keys": return queryPGPrimaryKeys
		case "indexes":   return queryPGIndexes
		case "fk", "foreign_keys": return queryPGForeignKeys
		case "views":     return queryPGViews
		case "sequences": return queryPGSequences
		case "triggers":  return queryPGTriggers
		}
	}
	if base == "mysql" {
		switch ot {
		case "tables":    return queryMySQLTables
		case "columns":   return queryMySQLColumns
		case "pk", "primary_keys": return queryMySQLPrimaryKeys
		case "indexes":   return queryMySQLIndexes
		case "fk", "foreign_keys": return queryMySQLForeignKeys
		case "views":     return queryMySQLViews
		case "triggers":  return queryMySQLTriggers
		}
	}
	return ""
}
