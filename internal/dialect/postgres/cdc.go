package postgres

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// PGCDCBuilder generates changelog table DDL and sync triggers for PostgreSQL
// (and PG-family dialects such as openGauss/PanWei).
type PGCDCBuilder struct{}

// changelogName returns the changelog table name for the source table.
func (PGCDCBuilder) changelogName(t *md.TableDef, opts dialect.CDCOptions) string {
	if opts.ChangelogTable != "" {
		return opts.ChangelogTable
	}
	prefix := opts.ChangelogPrefix
	if prefix == "" {
		prefix = "owl_chg_"
	}
	return prefix + t.TableName
}

func (PGCDCBuilder) quote(schema, name string) string {
	return fmt.Sprintf(`"%s"."%s"`, schema, name)
}

// BuildChangelogTable generates the CREATE TABLE DDL for the changelog table.
func (PGCDCBuilder) BuildChangelogTable(t *md.TableDef, opts dialect.CDCOptions) (string, error) {
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	b := PGCDCBuilder{}
	chg := b.changelogName(t, opts)
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(b.quote(schema, chg))
	sb.WriteString(" (\n")
	sb.WriteString("  \"chg_id\" BIGSERIAL PRIMARY KEY,\n")
	sb.WriteString("  \"shard_id\" INTEGER NOT NULL DEFAULT 0,\n")
	sb.WriteString("  \"op_type\" CHAR(1) NOT NULL,\n")
	sb.WriteString("  \"old_data\" JSONB,\n")
	sb.WriteString("  \"new_data\" JSONB,\n")
	sb.WriteString("  \"chg_time\" TIMESTAMPTZ NOT NULL DEFAULT now()\n")
	sb.WriteString(");")
	return sb.String(), nil
}

// BuildSyncTrigger generates row-level DML triggers and a statement-level
// TRUNCATE trigger. PostgreSQL cannot capture row-level OLD/NEW data and
// TRUNCATE with a single trigger (TRUNCATE only supports FOR EACH STATEMENT),
// so two triggers are emitted against two functions.
func (PGCDCBuilder) BuildSyncTrigger(t *md.TableDef, opts dialect.CDCOptions) (string, error) {
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	b := PGCDCBuilder{}
	chg := b.changelogName(t, opts)
	fnRow := "owl_sync_row_" + t.TableName
	fnTrunc := "owl_sync_trunc_" + t.TableName

	var sb strings.Builder

	// Function 1: row-level DML (I/U/D) — has access to OLD/NEW.
	sb.WriteString("CREATE OR REPLACE FUNCTION ")
	sb.WriteString(b.quote(schema, fnRow))
	sb.WriteString("() RETURNS TRIGGER AS $$\n")
	sb.WriteString("BEGIN\n")
	sb.WriteString("  IF (TG_OP = 'INSERT') THEN\n")
	sb.WriteString("    INSERT INTO ")
	sb.WriteString(b.quote(schema, chg))
	sb.WriteString(" (shard_id, op_type, old_data, new_data) VALUES (0, 'I', NULL, to_jsonb(NEW));\n")
	sb.WriteString("    RETURN NEW;\n")
	sb.WriteString("  ELSIF (TG_OP = 'UPDATE') THEN\n")
	sb.WriteString("    INSERT INTO ")
	sb.WriteString(b.quote(schema, chg))
	sb.WriteString(" (shard_id, op_type, old_data, new_data) VALUES (0, 'U', to_jsonb(OLD), to_jsonb(NEW));\n")
	sb.WriteString("    RETURN NEW;\n")
	sb.WriteString("  ELSE\n")
	sb.WriteString("    INSERT INTO ")
	sb.WriteString(b.quote(schema, chg))
	sb.WriteString(" (shard_id, op_type, old_data, new_data) VALUES (0, 'D', to_jsonb(OLD), NULL);\n")
	sb.WriteString("    RETURN OLD;\n")
	sb.WriteString("  END IF;\n")
	sb.WriteString("END;\n")
	sb.WriteString("$$ LANGUAGE plpgsql;\n\n")

	sb.WriteString("CREATE TRIGGER owl_sync_row_trg_")
	sb.WriteString(t.TableName)
	sb.WriteString(" AFTER INSERT OR UPDATE OR DELETE ON ")
	sb.WriteString(b.quote(schema, t.TableName))
	sb.WriteString(" FOR EACH ROW EXECUTE FUNCTION ")
	sb.WriteString(b.quote(schema, fnRow))
	sb.WriteString("();\n\n")

	// Function 2: statement-level TRUNCATE.
	sb.WriteString("CREATE OR REPLACE FUNCTION ")
	sb.WriteString(b.quote(schema, fnTrunc))
	sb.WriteString("() RETURNS TRIGGER AS $$\n")
	sb.WriteString("BEGIN\n")
	sb.WriteString("  INSERT INTO ")
	sb.WriteString(b.quote(schema, chg))
	sb.WriteString(" (shard_id, op_type, old_data, new_data) VALUES (0, 'T', NULL, NULL);\n")
	sb.WriteString("  RETURN NULL;\n")
	sb.WriteString("END;\n")
	sb.WriteString("$$ LANGUAGE plpgsql;\n\n")

	sb.WriteString("CREATE TRIGGER owl_sync_trunc_trg_")
	sb.WriteString(t.TableName)
	sb.WriteString(" AFTER TRUNCATE ON ")
	sb.WriteString(b.quote(schema, t.TableName))
	sb.WriteString(" FOR EACH STATEMENT EXECUTE FUNCTION ")
	sb.WriteString(b.quote(schema, fnTrunc))
	sb.WriteString("();")
	return sb.String(), nil
}
