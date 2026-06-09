package csv

import (
	"fmt"
	"io"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Loader assembles a SchemaModel from multiple CSV readers.
type Loader struct {
	files map[string]io.Reader // filename → reader
}

// NewLoader creates a new Loader.
func NewLoader() *Loader {
	return &Loader{files: make(map[string]io.Reader)}
}

// AddReader registers a CSV reader for a specific filename.
func (l *Loader) AddReader(filename string, r io.Reader) {
	l.files[filename] = r
}

// Load reads all registered CSV files and builds a SchemaModel.
func (l *Loader) Load() (*md.SchemaModel, error) {
	sm := md.NewSchemaModel()

	// Tables — required
	tables, err := l.parseTables()
	if err != nil {
		return nil, err
	}
	for _, tbl := range tables {
		if err := sm.AddTable(tbl); err != nil {
			return nil, err
		}
	}

	// Columns
	columns, err := l.parseColumns()
	if err != nil {
		return nil, fmt.Errorf("columns.csv: %w", err)
	}
	for _, col := range columns {
		tbl := sm.GetTable(col.TableSchema, col.TableName)
		if tbl == nil {
			return nil, fmt.Errorf("column %s references non-existent table %s.%s", col.ColumnName, col.TableSchema, col.TableName)
		}
		tbl.AddColumn(col)
	}

	// Primary Keys
	pks, _ := l.parsePrimaryKeys()
	for _, pk := range pks {
		tbl := sm.GetTable(pk.TableSchema, pk.TableName)
		if tbl != nil {
			tbl.AddPrimaryKey(pk.ConstraintName, pk.ColumnName)
		}
	}

	// Indexes
	indexes, _ := l.parseIndexes()
	for _, idx := range indexes {
		sm.AddIndex(idx)
	}

	// Foreign Keys
	fks, _ := l.parseForeignKeys()
	for _, fk := range fks {
		sm.AddForeignKey(fk)
	}

	// Views
	views, _ := l.parseViews()
	for _, v := range views {
		sm.AddView(v)
	}

	// Triggers
	triggers, _ := l.parseTriggers()
	for _, trg := range triggers {
		sm.AddTrigger(trg)
	}

	// Functions
	fns, _ := l.parseFunctions()
	for _, fn := range fns {
		sm.AddFunction(fn)
	}

	// Sequences
	seqs, _ := l.parseSequences()
	for _, seq := range seqs {
		sm.AddSequence(seq)
	}

	return sm, nil
}

func (l *Loader) parseTables() ([]*md.TableDef, error) {
	r, ok := l.files["tables.csv"]
	if !ok {
		r, ok = l.files["Tables.csv"]
	}
	if !ok {
		return nil, fmt.Errorf("tables.csv is required but not found")
	}
	return ParseTables(r)
}

func (l *Loader) parseColumns() ([]*md.ColumnDef, error) {
	r, ok := l.files["columns.csv"]
	if !ok {
		r, ok = l.files["Columns.csv"]
	}
	if !ok {
		return nil, nil // columns.csv is optional for some operations
	}
	return ParseColumns(r)
}

func (l *Loader) parsePrimaryKeys() ([]*md.PrimaryKeyDef, error) {
	r, ok := l.files["primary_keys.csv"]
	if !ok {
		return nil, nil
	}
	return ParsePrimaryKeys(r)
}

func (l *Loader) parseIndexes() ([]*md.IndexDef, error) {
	r, ok := l.files["indexes.csv"]
	if !ok {
		return nil, nil
	}
	return ParseIndexes(r)
}

func (l *Loader) parseForeignKeys() ([]*md.ForeignKeyDef, error) {
	r, ok := l.files["foreign_keys.csv"]
	if !ok {
		return nil, nil
	}
	return ParseForeignKeys(r)
}

func (l *Loader) parseViews() ([]*md.ViewDef, error) {
	r, ok := l.files["views.csv"]
	if !ok {
		return nil, nil
	}
	return ParseViews(r)
}

func (l *Loader) parseTriggers() ([]*md.TriggerDef, error) {
	r, ok := l.files["triggers.csv"]
	if !ok {
		return nil, nil
	}
	return ParseTriggers(r)
}

func (l *Loader) parseFunctions() ([]*md.FunctionDef, error) {
	r, ok := l.files["functions.csv"]
	if !ok {
		return nil, nil
	}
	return ParseFunctions(r)
}

func (l *Loader) parseSequences() ([]*md.SequenceDef, error) {
	r, ok := l.files["sequences.csv"]
	if !ok {
		return nil, nil
	}
	return ParseSequences(r)
}
