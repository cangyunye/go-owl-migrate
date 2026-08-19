package oracle

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// OracleCDCBuilder generates changelog table DDL and sync triggers for Oracle.
type OracleCDCBuilder struct{}

func (OracleCDCBuilder) changelogName(t *md.TableDef, opts dialect.CDCOptions) string {
	if opts.ChangelogTable != "" {
		return opts.ChangelogTable
	}
	prefix := opts.ChangelogPrefix
	if prefix == "" {
		prefix = "owl_chg_"
	}
	return strings.ToUpper(prefix) + strings.ToUpper(t.TableName)
}

func (OracleCDCBuilder) q(name string) string {
	return `"` + strings.ToUpper(name) + `"`
}

// BuildChangelogTable generates the CREATE TABLE DDL for the changelog table.
func (OracleCDCBuilder) BuildChangelogTable(t *md.TableDef, opts dialect.CDCOptions) (string, error) {
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	b := OracleCDCBuilder{}
	chg := b.changelogName(t, opts)

	var sb strings.Builder
	sb.WriteString("CREATE TABLE ")
	sb.WriteString(b.q(schema) + "." + b.q(chg))
	sb.WriteString(" (\n")
	sb.WriteString("  " + b.q("CHG_ID") + " NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,\n")
	sb.WriteString("  " + b.q("SHARD_ID") + " NUMBER DEFAULT 0 NOT NULL,\n")
	sb.WriteString("  " + b.q("OP_TYPE") + " CHAR(1) NOT NULL,\n")
	sb.WriteString("  " + b.q("OLD_DATA") + " CLOB,\n")
	sb.WriteString("  " + b.q("NEW_DATA") + " CLOB,\n")
	sb.WriteString("  " + b.q("CHG_TIME") + " TIMESTAMP DEFAULT SYSTIMESTAMP NOT NULL\n")
	sb.WriteString(");")
	return sb.String(), nil
}

// jsonObject builds `JSON_OBJECT(KEY 'COL' VALUE expr, ...)` for the columns.
// Oracle rejects quoted bind qualifiers (`:NEW."COL"`), so the value uses the
// bare `:NEW.COL` / `:OLD.COL` reference.
func (OracleCDCBuilder) jsonObject(prefix string, cols []*md.ColumnDef) string {
	var parts []string
	for _, c := range cols {
		parts = append(parts, fmt.Sprintf("KEY '%s' VALUE %s.%s", c.ColumnName, prefix, c.ColumnName))
	}
	return "JSON_OBJECT(" + strings.Join(parts, ", ") + ")"
}

// BuildSyncTrigger generates the row-level I/U/D trigger and a best-effort
// schema-level TRUNCATE trigger.
func (OracleCDCBuilder) BuildSyncTrigger(t *md.TableDef, opts dialect.CDCOptions) (string, error) {
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	b := OracleCDCBuilder{}
	chg := b.changelogName(t, opts)
	cols := t.GetColumns()
	newJSON := b.jsonObject(":NEW", cols)
	oldJSON := b.jsonObject(":OLD", cols)

	var sb strings.Builder

	// Row-level I/U/D trigger.
	trgRow := "OWL_SYNC_ROW_" + strings.ToUpper(t.TableName)
	sb.WriteString("CREATE OR REPLACE TRIGGER ")
	sb.WriteString(b.q(schema) + "." + b.q(trgRow))
	sb.WriteString("\nAFTER INSERT OR UPDATE OR DELETE ON ")
	sb.WriteString(b.q(schema) + "." + b.q(t.TableName))
	sb.WriteString("\nFOR EACH ROW\n")
	sb.WriteString("BEGIN\n")
	sb.WriteString("  IF INSERTING THEN\n")
	sb.WriteString("    INSERT INTO " + b.q(schema) + "." + b.q(chg))
	sb.WriteString(" (SHARD_ID, OP_TYPE, OLD_DATA, NEW_DATA) VALUES (0, 'I', NULL, " + newJSON + ");\n")
	sb.WriteString("  ELSIF UPDATING THEN\n")
	sb.WriteString("    INSERT INTO " + b.q(schema) + "." + b.q(chg))
	sb.WriteString(" (SHARD_ID, OP_TYPE, OLD_DATA, NEW_DATA) VALUES (0, 'U', " + oldJSON + ", " + newJSON + ");\n")
	sb.WriteString("  ELSIF DELETING THEN\n")
	sb.WriteString("    INSERT INTO " + b.q(schema) + "." + b.q(chg))
	sb.WriteString(" (SHARD_ID, OP_TYPE, OLD_DATA, NEW_DATA) VALUES (0, 'D', " + oldJSON + ", NULL);\n")
	sb.WriteString("  END IF;\n")
	sb.WriteString("END;\n/\n\n")

	// Best-effort schema-level TRUNCATE trigger (Oracle only supports
	// TRUNCATE via a SCHEMA DDL trigger; filter by object name).
	trgTrunc := "OWL_SYNC_TRUNC_" + strings.ToUpper(t.TableName)
	sb.WriteString("CREATE OR REPLACE TRIGGER ")
	sb.WriteString(b.q(schema) + "." + b.q(trgTrunc))
	sb.WriteString("\nAFTER TRUNCATE ON SCHEMA\n")
	sb.WriteString("BEGIN\n")
	sb.WriteString("  IF ORA_DICT_OBJ_TYPE = 'TABLE' AND UPPER(ORA_DICT_OBJ_NAME) = " + fmt.Sprintf("'%s'", strings.ToUpper(t.TableName)) + " THEN\n")
	sb.WriteString("    INSERT INTO " + b.q(schema) + "." + b.q(chg))
	sb.WriteString(" (SHARD_ID, OP_TYPE, OLD_DATA, NEW_DATA) VALUES (0, 'T', NULL, NULL);\n")
	sb.WriteString("  END IF;\n")
	sb.WriteString("END;\n/")

	return sb.String(), nil
}
