package csv

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// recordToMap converts a CSV record (header + values) into a map.
// \N values are converted to empty strings (NULL).
func recordToMap(headers, record []string) map[string]string {
	m := make(map[string]string, len(headers))
	for i, h := range headers {
		if i < len(record) {
			m[h] = nullValue(record[i])
		} else {
			m[h] = ""
		}
	}
	return m
}

func getInt(m map[string]string, key string) int {
	v, _ := strconv.Atoi(m[key])
	return v
}

// skipLine returns true if the line should be skipped (empty or comment).
func skipLine(line []string) bool {
	if len(line) == 0 {
		return true
	}
	if len(line) == 1 && line[0] == "" {
		return true
	}
	if strings.HasPrefix(line[0], "#") {
		return true
	}
	return false
}

func readAllRecords(r io.Reader) ([][]string, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1 // allow variable field counts (comment lines)
	reader.Comment = 0          // handled manually
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return records, nil
}

// nullValue converts a CSV field value to empty string if it represents NULL.
// Supports \N (MySQL convention) and the nullMarker config value.
func nullValue(v string) string {
	if v == "\\N" {
		return ""
	}
	return v
}

// getString returns a null-processed string value from the record map.
func getString(m map[string]string, key string) string {
	return nullValue(m[key])
}

// ParseTables parses a tables.csv reader.
func ParseTables(r io.Reader) ([]*md.TableDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, fmt.Errorf("empty CSV: no header found")
	}
	headers := records[0]
	var tables []*md.TableDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		tbl, err := md.NewTableDef(m["TABLE_SCHEMA"], m["TABLE_NAME"])
		if err != nil {
			return nil, err
		}
		if v := m["TABLE_TYPE"]; v != "" {
			tbl.TableType = v
		}
		tbl.Engine = m["ENGINE"]
		tbl.Tablespace = m["TABLESPACE"]
		tbl.TableComment = m["TABLE_COMMENT"]
		tbl.Partitioned = m["PARTITIONED"]
		tbl.PartitionInfo = m["PARTITION_INFO"]
		tbl.RowFormat = m["ROW_FORMAT"]
		tbl.Temporary = m["TEMPORARY"]
		tbl.RowCount = getInt(m, "ROW_COUNT")
		tbl.Charset = m["CHARSET"]
		tbl.Collation = m["COLLATION"]
		tables = append(tables, tbl)
	}
	return tables, nil
}

// ParseColumns parses a columns.csv reader.
func ParseColumns(r io.Reader) ([]*md.ColumnDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, fmt.Errorf("empty CSV: no header found")
	}
	headers := records[0]
	var cols []*md.ColumnDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		pos := getInt(m, "ORDINAL_POSITION")
		col, err := md.NewColumnDef(m["TABLE_SCHEMA"], m["TABLE_NAME"], m["COLUMN_NAME"], pos, m["DATA_TYPE"])
		if err != nil {
			return nil, fmt.Errorf("row %v: %w", rec, err)
		}
		col.DataLength = getInt(m, "DATA_LENGTH")
		col.DataPrecision = getInt(m, "DATA_PRECISION")
		col.DataScale = getInt(m, "DATA_SCALE")
		if v := m["NULLABLE"]; v != "" {
			col.Nullable = v
		}
		col.DefaultValue = m["DEFAULT_VALUE"]
		col.ColumnComment = m["COLUMN_COMMENT"]
		if v := m["IS_IDENTITY"]; v != "" {
			col.IsIdentity = v
		}
		col.IdentityGeneration = m["IDENTITY_GENERATION"]
		col.IdentityStart = getInt(m, "IDENTITY_START")
		col.IdentityIncrement = getInt(m, "IDENTITY_INCREMENT")
		col.CharUsed = m["CHAR_USED"]
		col.HiddenColumn = m["HIDDEN_COLUMN"]
		col.VirtualExpression = m["VIRTUAL_EXPRESSION"]
		col.EnumValues = m["ENUM_VALUES"]
		col.CharacterSet = m["CHARACTER_SET"]
		col.Collation = m["COLLATION"]
		col.OnUpdate = m["ON_UPDATE"]
		cols = append(cols, col)
	}
	return cols, nil
}

// ParsePrimaryKeys parses a primary_keys.csv reader.
func ParsePrimaryKeys(r io.Reader) ([]*md.PrimaryKeyDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var pks []*md.PrimaryKeyDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		pks = append(pks, &md.PrimaryKeyDef{
			TableSchema:     m["TABLE_SCHEMA"],
			TableName:       m["TABLE_NAME"],
			ConstraintName:  m["CONSTRAINT_NAME"],
			ColumnName:      m["COLUMN_NAME"],
			OrdinalPosition: getInt(m, "ORDINAL_POSITION"),
		})
	}
	return pks, nil
}

// ParseIndexes parses an indexes.csv reader.
func ParseIndexes(r io.Reader) ([]*md.IndexDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var indexes []*md.IndexDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		indexes = append(indexes, &md.IndexDef{
			TableSchema:     m["TABLE_SCHEMA"],
			TableName:       m["TABLE_NAME"],
			IndexName:       m["INDEX_NAME"],
			IndexType:       m["INDEX_TYPE"],
			Uniqueness:      m["UNIQUENESS"],
			ColumnName:      m["COLUMN_NAME"],
			OrdinalPosition: getInt(m, "ORDINAL_POSITION"),
			Expression:      m["EXPRESSION"],
			Descend:         m["DESCEND"],
			WhereClause:     m["WHERE_CLAUSE"],
		})
	}
	return indexes, nil
}

// ParseForeignKeys parses a foreign_keys.csv reader.
func ParseForeignKeys(r io.Reader) ([]*md.ForeignKeyDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var fks []*md.ForeignKeyDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		fks = append(fks, &md.ForeignKeyDef{
			ConstraintName: m["CONSTRAINT_NAME"],
			TableSchema:    m["TABLE_SCHEMA"],
			TableName:      m["TABLE_NAME"],
			ColumnName:     m["COLUMN_NAME"],
			RefSchema:      m["REF_SCHEMA"],
			RefTable:       m["REF_TABLE"],
			RefColumn:      m["REF_COLUMN"],
			DeleteRule:     m["DELETE_RULE"],
			UpdateRule:     m["UPDATE_RULE"],
			Deferrable:     m["DEFERRABLE"],
		})
	}
	return fks, nil
}

// ParseViews parses a views.csv reader.
func ParseViews(r io.Reader) ([]*md.ViewDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var views []*md.ViewDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		views = append(views, &md.ViewDef{
			ViewSchema:     m["VIEW_SCHEMA"],
			ViewName:       m["VIEW_NAME"],
			ViewDefinition: m["VIEW_DEFINITION"],
			ViewComment:    m["VIEW_COMMENT"],
			IsUpdatable:    m["IS_UPDATABLE"],
			CheckOption:    m["CHECK_OPTION"],
			Owner:          m["OWNER"],
		})
	}
	return views, nil
}

// ParseTriggers parses a triggers.csv reader.
func ParseTriggers(r io.Reader) ([]*md.TriggerDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var triggers []*md.TriggerDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		triggers = append(triggers, &md.TriggerDef{
			TriggerSchema: m["TRIGGER_SCHEMA"],
			TriggerName:   m["TRIGGER_NAME"],
			TableSchema:   m["TABLE_SCHEMA"],
			TableName:     m["TABLE_NAME"],
			TriggerType:   m["TRIGGER_TYPE"],
			TriggerEvent:  m["TRIGGER_EVENT"],
			TriggerBody:   m["TRIGGER_BODY"],
			Status:        m["STATUS"],
			ForEach:       m["FOR_EACH"],
			WhenClause:    m["WHEN_CLAUSE"],
			Referencing:   m["REFERENCING"],
			Description:   m["DESCRIPTION"],
			Language:      m["LANGUAGE"],
		})
	}
	return triggers, nil
}

// ParseFunctions parses a functions.csv reader.
func ParseFunctions(r io.Reader) ([]*md.FunctionDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var fns []*md.FunctionDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		fns = append(fns, &md.FunctionDef{
			FunctionSchema: m["FUNCTION_SCHEMA"],
			FunctionName:   m["FUNCTION_NAME"],
			FunctionType:   m["FUNCTION_TYPE"],
			ReturnType:     m["RETURN_TYPE"],
			FunctionBody:   m["FUNCTION_BODY"],
			Language:       m["LANGUAGE"],
			Status:         m["STATUS"],
			Arguments:      m["ARGUMENTS"],
			AuthID:         m["AUTHID"],
			Deterministic:  m["DETERMINISTIC"],
			Parallel:       m["PARALLEL"],
		})
	}
	return fns, nil
}

// ParseSequences parses a sequences.csv reader.
func ParseSequences(r io.Reader) ([]*md.SequenceDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var seqs []*md.SequenceDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		seqs = append(seqs, &md.SequenceDef{
			SequenceSchema: m["SEQUENCE_SCHEMA"],
			SequenceName:   m["SEQUENCE_NAME"],
			StartValue:     getInt(m, "START_VALUE"),
			IncrementBy:    getInt(m, "INCREMENT_BY"),
			MinValue:       getInt(m, "MIN_VALUE"),
			MaxValue:       getInt(m, "MAX_VALUE"),
			Cycle:          m["CYCLE"],
			CacheSize:      getInt(m, "CACHE_SIZE"),
			OrderFlag:      m["ORDER_FLAG"],
			CurrentValue:   getInt(m, "CURRENT_VALUE"),
			DataType:       m["DATA_TYPE"],
		})
	}
	return seqs, nil
}

// ParseSynonyms parses a synonyms.csv reader.
func ParseSynonyms(r io.Reader) ([]*md.SynonymDef, error) {
	records, err := readAllRecords(r)
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	headers := records[0]
	var synonyms []*md.SynonymDef
	for _, rec := range records[1:] {
		if skipLine(rec) {
			continue
		}
		m := recordToMap(headers, rec)
		synonyms = append(synonyms, &md.SynonymDef{
			SynonymName:   m["SYNONYM_NAME"],
			SynonymSchema: m["SYNONYM_SCHEMA"],
			TargetSchema:  m["TARGET_SCHEMA"],
			TargetName:    m["TARGET_NAME"],
			IsPublic:      m["IS_PUBLIC"],
			TargetType:    m["TARGET_TYPE"],
		})
	}
	return synonyms, nil
}
