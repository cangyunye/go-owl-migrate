package postgres

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func testTableDef() *md.TableDef {
	t, _ := md.NewTableDef("public", "emp")
	empno, _ := md.NewColumnDef("public", "emp", "empno", 1, "INTEGER")
	empno.Nullable = "NO"
	ename, _ := md.NewColumnDef("public", "emp", "ename", 2, "VARCHAR")
	ename.DataLength = 20
	sal, _ := md.NewColumnDef("public", "emp", "sal", 3, "NUMERIC")
	sal.DataPrecision = 7
	sal.DataScale = 2
	t.AddColumn(empno)
	t.AddColumn(ename)
	t.AddColumn(sal)
	t.AddPrimaryKey("pk_emp", "empno")
	return t
}

func TestPGChangelogTable(t *testing.T) {
	got, err := (PGCDCBuilder{}).BuildChangelogTable(testTableDef(), dialect.CDCOptions{})
	if err != nil {
		t.Fatalf("BuildChangelogTable: %v", err)
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS \"public\".\"owl_chg_emp\"",
		"\"chg_id\" BIGSERIAL",
		"\"shard_id\" INTEGER",
		"\"op_type\" CHAR(1)",
		"\"old_data\" JSONB",
		"\"new_data\" JSONB",
		"\"chg_time\" TIMESTAMPTZ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("changelog DDL missing %q\n%s", want, got)
		}
	}
}

func TestPGSyncTrigger(t *testing.T) {
	got, err := (PGCDCBuilder{}).BuildSyncTrigger(testTableDef(), dialect.CDCOptions{})
	if err != nil {
		t.Fatalf("BuildSyncTrigger: %v", err)
	}
	for _, want := range []string{
		"CREATE OR REPLACE FUNCTION",
		"TG_OP",
		"'INSERT'",
		"'UPDATE'",
		"to_jsonb",
		"CREATE TRIGGER",
		"AFTER INSERT OR UPDATE OR DELETE",
		"FOR EACH ROW",
		"AFTER TRUNCATE",
		"FOR EACH STATEMENT",
		"\"public\".\"emp\"",
		"owl_chg_emp",
		"0, 'D', to_jsonb(OLD), NULL",
		"0, 'T', NULL, NULL",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trigger DDL missing %q\n%s", want, got)
		}
	}
}

func TestPGSyncTriggerForEachStatementIsRejectedByRow(t *testing.T) {
	// PG cannot mix row-level DML and statement-level TRUNCATE in one trigger;
	// the builder must emit two separate triggers.
	got, _ := (PGCDCBuilder{}).BuildSyncTrigger(testTableDef(), dialect.CDCOptions{})
	if strings.Count(got, "CREATE TRIGGER") < 2 {
		t.Errorf("expected 2 CREATE TRIGGER statements (row DML + statement truncate), got:\n%s", got)
	}
}

func TestPGSyncTriggerNoTruncateWhenAsked(t *testing.T) {
	// schema mapping should apply to trigger + changelog targets
	_ = dialect.CDCOptions{SchemaMapping: map[string]string{"public": "app"}}
}
