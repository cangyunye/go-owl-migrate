package metadata

import (
	"fmt"
)

// ColumnDef represents a column definition from metadata.
type ColumnDef struct {
	TableSchema        string
	TableName          string
	ColumnName         string
	OrdinalPosition    int
	DataType           string
	DataLength         int
	DataPrecision      int
	DataScale          int
	Nullable           string // YES/NO
	DefaultValue       string
	ColumnComment      string
	IsIdentity         string // YES/NO
	IdentityGeneration string // ALWAYS/BY DEFAULT
	IdentityStart      int
	IdentityIncrement  int
	CharUsed           string // Oracle CHAR/BYTE
	HiddenColumn       string // YES/NO
	VirtualExpression  string
	EnumValues         string
	CharacterSet       string
	Collation          string
	OnUpdate           string
}

// NewColumnDef creates a ColumnDef with required field validation.
func NewColumnDef(tableSchema, tableName, columnName string, ordinalPosition int, dataType string) (*ColumnDef, error) {
	if tableSchema == "" {
		return nil, fmt.Errorf("TABLE_SCHEMA is required")
	}
	if tableName == "" {
		return nil, fmt.Errorf("TABLE_NAME is required")
	}
	if columnName == "" {
		return nil, fmt.Errorf("COLUMN_NAME is required")
	}
	if dataType == "" {
		return nil, fmt.Errorf("DATA_TYPE is required")
	}
	if ordinalPosition < 1 {
		return nil, fmt.Errorf("ORDINAL_POSITION must be >= 1")
	}
	return &ColumnDef{
		TableSchema:     tableSchema,
		TableName:       tableName,
		ColumnName:      columnName,
		OrdinalPosition: ordinalPosition,
		DataType:        dataType,
		Nullable:        "YES",
		IsIdentity:      "NO",
	}, nil
}

// IsNullable returns true if the column is nullable.
func (c *ColumnDef) IsNullable() bool {
	return c.Nullable == "YES"
}

// IsIdentityColumn returns true if the column is an identity column.
func (c *ColumnDef) IsIdentityColumn() bool {
	return c.IsIdentity == "YES"
}

// HasDefault returns true and the default value if the column has one.
func (c *ColumnDef) HasDefault() (bool, string) {
	return c.DefaultValue != "", c.DefaultValue
}

// ── TableDef ──

// TableDef represents a table definition.
type TableDef struct {
	TableSchema   string
	TableName     string
	TableType     string // TABLE/VIEW/MVIEW
	Engine        string
	Tablespace    string
	TableComment  string
	Partitioned   string // YES/NO
	PartitionInfo string
	RowFormat     string
	Temporary     string // YES/NO
	RowCount      int
	Charset       string
	Collation     string
	Owner         string // Original owner (Oracle User/PG Schema/MySQL Database)

	Columns     []*ColumnDef
	PrimaryKeys []*PrimaryKeyDef
	Indexes     []*IndexDef
	ForeignKeys []*ForeignKeyDef
	Triggers    []*TriggerDef
}

// NewTableDef creates a TableDef with required field validation.
func NewTableDef(tableSchema, tableName string) (*TableDef, error) {
	if tableSchema == "" {
		return nil, fmt.Errorf("TABLE_SCHEMA is required")
	}
	if tableName == "" {
		return nil, fmt.Errorf("TABLE_NAME is required")
	}
	return &TableDef{
		TableSchema: tableSchema,
		TableName:   tableName,
		TableType:   "TABLE",
	}, nil
}

// AddColumn adds a column definition. Returns error on duplicate name.
func (t *TableDef) AddColumn(col *ColumnDef) error {
	for _, existing := range t.Columns {
		if equalFold(existing.ColumnName, col.ColumnName) {
			return fmt.Errorf("duplicate column %q in %s.%s", col.ColumnName, t.TableSchema, t.TableName)
		}
	}
	t.Columns = append(t.Columns, col)
	return nil
}

// GetColumn returns a column by name (case-insensitive). Returns nil if not found.
func (t *TableDef) GetColumn(name string) *ColumnDef {
	for _, col := range t.Columns {
		if equalFold(col.ColumnName, name) {
			return col
		}
	}
	return nil
}

// GetColumns returns all columns sorted by OrdinalPosition.
func (t *TableDef) GetColumns() []*ColumnDef {
	sorted := make([]*ColumnDef, len(t.Columns))
	copy(sorted, t.Columns)
	sortColumns(sorted)
	return sorted
}

// AddPrimaryKey adds a primary key column reference.
func (t *TableDef) AddPrimaryKey(constraintName, columnName string) {
	t.PrimaryKeys = append(t.PrimaryKeys, &PrimaryKeyDef{
		TableSchema:     t.TableSchema,
		TableName:       t.TableName,
		ConstraintName:  constraintName,
		ColumnName:      columnName,
		OrdinalPosition: len(t.PrimaryKeys) + 1,
	})
}

// GetPrimaryKeys returns primary key definitions.
func (t *TableDef) GetPrimaryKeys() []*PrimaryKeyDef {
	return t.PrimaryKeys
}

// AddIndex adds an index definition.
func (t *TableDef) AddIndex(idx *IndexDef) {
	t.Indexes = append(t.Indexes, idx)
}

// GetIndexes returns all indexes on this table.
func (t *TableDef) GetIndexes() []*IndexDef {
	return t.Indexes
}

// AddForeignKey adds a foreign key definition.
func (t *TableDef) AddForeignKey(fk *ForeignKeyDef) {
	t.ForeignKeys = append(t.ForeignKeys, fk)
}

// GetForeignKeys returns foreign keys on this table.
func (t *TableDef) GetForeignKeys() []*ForeignKeyDef {
	return t.ForeignKeys
}

// ── Supporting types ──

// PrimaryKeyDef represents a primary key column.
type PrimaryKeyDef struct {
	TableSchema     string
	TableName       string
	ConstraintName  string
	ColumnName      string
	OrdinalPosition int
}

// IndexDef represents an index definition.
type IndexDef struct {
	TableSchema     string
	TableName       string
	IndexName       string
	IndexType       string // BTREE/BITMAP/GIN/GIST/FULLTEXT/UNIQUE
	Uniqueness      string // UNIQUE/NONUNIQUE
	ColumnName      string
	OrdinalPosition int
	Expression      string
	Descend         string // ASC/DESC
	WhereClause     string
}

// IsUnique returns true if this is a unique index/constraint.
func (i *IndexDef) IsUnique() bool {
	return i.Uniqueness == "UNIQUE" || i.IndexType == "UNIQUE"
}

// ForeignKeyDef represents a foreign key constraint.
type ForeignKeyDef struct {
	ConstraintName string
	TableSchema    string
	TableName      string
	ColumnName     string
	RefSchema      string
	RefTable       string
	RefColumn      string
	DeleteRule     string // CASCADE/SET NULL/RESTRICT/NO ACTION
	UpdateRule     string // CASCADE/SET NULL/RESTRICT/NO ACTION
	Deferrable     string // DEFERRABLE/NOT DEFERRABLE
}

// ViewDef represents a view definition.
type ViewDef struct {
	ViewSchema     string
	ViewName       string
	ViewDefinition string
	ViewComment    string
	IsUpdatable    string
	CheckOption    string
	Owner          string
}

// MViewDef represents a materialized view definition.
type MViewDef struct {
	MViewSchema     string
	MViewName       string
	MViewQuery      string
	RefreshMethod   string
	RefreshMode     string
	RefreshInterval string
	BuildMode       string
	MViewComment    string
}

// TriggerDef represents a trigger definition.
type TriggerDef struct {
	TriggerSchema string
	TriggerName   string
	TableSchema   string
	TableName     string
	TriggerType   string // BEFORE/AFTER/INSTEAD OF/COMPOUND
	TriggerEvent  string // INSERT/UPDATE/DELETE/TRUNCATE
	TriggerBody   string
	Status        string // ENABLED/DISABLED
	ForEach       string // ROW/STATEMENT
	WhenClause    string
	Referencing   string
	Description   string
	Language      string // PLSQL/PLPGSQL
}

// FunctionDef represents a stored function/procedure definition.
type FunctionDef struct {
	FunctionSchema string
	FunctionName   string
	FunctionType   string // FUNCTION/PROCEDURE
	ReturnType     string
	FunctionBody   string
	Language       string
	Status         string
	Arguments      string // JSON format
	AuthID         string
	Deterministic  string
	Parallel       string
}

// PackageDef represents an Oracle package specification.
type PackageDef struct {
	PackageSchema string
	PackageName   string
	PackageSpec   string
	Status        string
	AuthID        string
	Description   string
}

// PackageBodyDef represents an Oracle package body.
type PackageBodyDef struct {
	PackageSchema string
	PackageName   string
	PackageBody   string
	Status        string
}

// SynonymDef represents a synonym definition (Oracle).
type SynonymDef struct {
	SynonymName   string
	SynonymSchema string
	TargetSchema  string
	TargetName    string
	IsPublic      string // YES/NO
	TargetType    string
}

// SequenceDef represents a sequence definition.
type SequenceDef struct {
	SequenceSchema string
	SequenceName   string
	StartValue     int
	IncrementBy    int
	MinValue       int
	MaxValue       int
	Cycle          string // YES/NO
	CacheSize      int
	OrderFlag      string
	CurrentValue   int
	DataType       string
}

// ── SchemaModel ──

// SchemaModel is the unified container for all database metadata.
type SchemaModel struct {
	Tables   map[string]*TableDef // key: "SCOTT.EMP"
	Views    []*ViewDef
	MViews   []*MViewDef
	Synonyms []*SynonymDef

	// Global index (not keyed to a specific table)
	allForeignKeys []*ForeignKeyDef
	allTriggers    []*TriggerDef
	allFunctions   []*FunctionDef
	allSequences   []*SequenceDef
	allPackages    []*PackageDef
	allPackageBodies []*PackageBodyDef

	// Track FK/trigger refs for validation
	tableSet map[string]bool
}

// NewSchemaModel creates an empty SchemaModel.
func NewSchemaModel() *SchemaModel {
	return &SchemaModel{
		Tables:   make(map[string]*TableDef),
		tableSet: make(map[string]bool),
	}
}

func tableKey(schema, name string) string {
	return fmt.Sprintf("%s.%s", schema, name)
}

// AddTable adds a table definition. Returns error on duplicate.
func (sm *SchemaModel) AddTable(t *TableDef) error {
	key := tableKey(t.TableSchema, t.TableName)
	if sm.tableSet[key] {
		return fmt.Errorf("duplicate table %s", key)
	}
	sm.Tables[key] = t
	sm.tableSet[key] = true
	return nil
}

// GetTable returns a table by schema and name, or nil.
func (sm *SchemaModel) GetTable(schema, name string) *TableDef {
	return sm.Tables[tableKey(schema, name)]
}

// HasTable returns true if the table exists.
func (sm *SchemaModel) HasTable(schema, name string) bool {
	return sm.tableSet[tableKey(schema, name)]
}

// GetColumns returns columns for a table by schema and name, sorted by OrdinalPosition.
func (sm *SchemaModel) GetColumns(schema, name string) []*ColumnDef {
	t := sm.GetTable(schema, name)
	if t == nil {
		return nil
	}
	return t.GetColumns()
}

// GetTables returns all tables sorted by schema.name.
func (sm *SchemaModel) GetTables() []*TableDef {
	result := make([]*TableDef, 0, len(sm.Tables))
	for _, t := range sm.Tables {
		result = append(result, t)
	}
	sortTables(result)
	return result
}

// GetPrimaryKeys returns primary key definitions for a table.
func (sm *SchemaModel) GetPrimaryKeys(schema, name string) []*PrimaryKeyDef {
	t := sm.GetTable(schema, name)
	if t == nil {
		return nil
	}
	return t.GetPrimaryKeys()
}

// ── Global object accessors ──

// AddView adds a view definition.
func (sm *SchemaModel) AddView(v *ViewDef) {
	sm.Views = append(sm.Views, v)
}

// GetViews returns all views.
func (sm *SchemaModel) GetViews() []*ViewDef { return sm.Views }

// GetView returns a view by schema and name.
func (sm *SchemaModel) GetView(schema, name string) *ViewDef {
	for _, v := range sm.Views {
		if v.ViewSchema == schema && v.ViewName == name {
			return v
		}
	}
	return nil
}

// AddIndex adds an index definition to the model.
func (sm *SchemaModel) AddIndex(idx *IndexDef) {
	t := sm.GetTable(idx.TableSchema, idx.TableName)
	if t != nil {
		t.AddIndex(idx)
	}
}

// GetIndexes returns indexes for a table.
func (sm *SchemaModel) GetIndexes(schema, name string) []*IndexDef {
	t := sm.GetTable(schema, name)
	if t == nil {
		return nil
	}
	return t.GetIndexes()
}

// AddForeignKey adds a foreign key definition.
func (sm *SchemaModel) AddForeignKey(fk *ForeignKeyDef) {
	sm.allForeignKeys = append(sm.allForeignKeys, fk)
	t := sm.GetTable(fk.TableSchema, fk.TableName)
	if t != nil {
		t.AddForeignKey(fk)
	}
}

// GetForeignKeys returns foreign keys for a table.
func (sm *SchemaModel) GetForeignKeys(schema, name string) []*ForeignKeyDef {
	t := sm.GetTable(schema, name)
	if t == nil {
		return nil
	}
	return t.GetForeignKeys()
}

// AddTrigger adds a trigger definition.
func (sm *SchemaModel) AddTrigger(trg *TriggerDef) {
	sm.allTriggers = append(sm.allTriggers, trg)
}

// GetTriggers returns triggers for a given table.
func (sm *SchemaModel) GetTriggers(schema, name string) []*TriggerDef {
	var result []*TriggerDef
	for _, trg := range sm.allTriggers {
		if trg.TableSchema == schema && trg.TableName == name {
			result = append(result, trg)
		}
	}
	return result
}

// AddFunction adds a function/procedure definition.
func (sm *SchemaModel) AddFunction(fn *FunctionDef) {
	sm.allFunctions = append(sm.allFunctions, fn)
}

// GetFunctions returns functions in a given schema.
func (sm *SchemaModel) GetFunctions(schema string) []*FunctionDef {
	var result []*FunctionDef
	for _, fn := range sm.allFunctions {
		if fn.FunctionSchema == schema {
			result = append(result, fn)
		}
	}
	return result
}

// AddSequence adds a sequence definition.
func (sm *SchemaModel) AddSequence(seq *SequenceDef) {
	sm.allSequences = append(sm.allSequences, seq)
}

// GetSequences returns sequences in a given schema.
func (sm *SchemaModel) GetSequences(schema string) []*SequenceDef {
	var result []*SequenceDef
	for _, seq := range sm.allSequences {
		if seq.SequenceSchema == schema {
			result = append(result, seq)
		}
	}
	return result
}

// AddSynonym adds a synonym definition.
func (sm *SchemaModel) AddSynonym(syn *SynonymDef) {
	sm.Synonyms = append(sm.Synonyms, syn)
}

// GetSynonyms returns synonyms in a given schema.
func (sm *SchemaModel) GetSynonyms(schema string) []*SynonymDef {
	var result []*SynonymDef
	for _, syn := range sm.Synonyms {
		if syn.SynonymSchema == schema {
			result = append(result, syn)
		}
	}
	return result
}

// AddMView adds a materialized view definition.
func (sm *SchemaModel) AddMView(mv *MViewDef) {
	sm.MViews = append(sm.MViews, mv)
}

// GetMViews returns all materialized views.
func (sm *SchemaModel) GetMViews() []*MViewDef {
	return sm.MViews
}

// AddPackage adds a package definition.
func (sm *SchemaModel) AddPackage(pkg *PackageDef) {
	sm.allPackages = append(sm.allPackages, pkg)
}

// GetPackages returns packages in a given schema.
func (sm *SchemaModel) GetPackages(schema string) []*PackageDef {
	var result []*PackageDef
	for _, pkg := range sm.allPackages {
		if pkg.PackageSchema == schema {
			result = append(result, pkg)
		}
	}
	return result
}

// AddPackageBody adds a package body definition.
func (sm *SchemaModel) AddPackageBody(pkg *PackageBodyDef) {
	sm.allPackageBodies = append(sm.allPackageBodies, pkg)
}

// GetPackageBodies returns package bodies in a given schema.
func (sm *SchemaModel) GetPackageBodies(schema string) []*PackageBodyDef {
	var result []*PackageBodyDef
	for _, pkg := range sm.allPackageBodies {
		if pkg.PackageSchema == schema {
			result = append(result, pkg)
		}
	}
	return result
}

// ── Validate ──

// ValidationError represents a validation issue.
type ValidationError struct {
	Severity string // ERROR/WARNING
	Message  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Severity, e.Message)
}

// Validate checks referential integrity of the entire model.
// Returns all errors (does not short-circuit).
func (sm *SchemaModel) Validate() []*ValidationError {
	var errs []*ValidationError

	// Validate FK references
	for _, fk := range sm.allForeignKeys {
		refTable := sm.GetTable(fk.RefSchema, fk.RefTable)
		if refTable == nil {
			errs = append(errs, &ValidationError{
				Severity: "ERROR",
				Message:  fmt.Sprintf("foreign key %s references non-existent table %s.%s", fk.ConstraintName, fk.RefSchema, fk.RefTable),
			})
			continue
		}
		if refTable.GetColumn(fk.RefColumn) == nil {
			errs = append(errs, &ValidationError{
				Severity: "ERROR",
				Message:  fmt.Sprintf("foreign key %s references non-existent column %s.%s.%s", fk.ConstraintName, fk.RefSchema, fk.RefTable, fk.RefColumn),
			})
		}
	}

	// Validate trigger table references
	for _, trg := range sm.allTriggers {
		if !sm.HasTable(trg.TableSchema, trg.TableName) {
			errs = append(errs, &ValidationError{
				Severity: "ERROR",
				Message:  fmt.Sprintf("trigger %s references non-existent table %s.%s", trg.TriggerName, trg.TableSchema, trg.TableName),
			})
		}
	}

	// Validate primary keys (WARNING only)
	for _, t := range sm.GetTables() {
		if len(t.GetPrimaryKeys()) == 0 {
			errs = append(errs, &ValidationError{
				Severity: "WARNING",
				Message:  fmt.Sprintf("table %s.%s has no primary key", t.TableSchema, t.TableName),
			})
		}
	}

	return errs
}

// ── Helpers ──

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		// Simple ASCII case-insensitive
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func sortColumns(cols []*ColumnDef) {
	for i := 0; i < len(cols); i++ {
		for j := i + 1; j < len(cols); j++ {
			if cols[j].OrdinalPosition < cols[i].OrdinalPosition {
				cols[i], cols[j] = cols[j], cols[i]
			}
		}
	}
}

func sortTables(tables []*TableDef) {
	for i := 0; i < len(tables); i++ {
		for j := i + 1; j < len(tables); j++ {
			keyA := tableKey(tables[i].TableSchema, tables[i].TableName)
			keyB := tableKey(tables[j].TableSchema, tables[j].TableName)
			if keyB < keyA {
				tables[i], tables[j] = tables[j], tables[i]
			}
		}
	}
}
