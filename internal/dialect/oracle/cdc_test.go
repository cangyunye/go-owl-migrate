package oracle

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func oTestTableDef() *md.TableDef {
	t, _ := md.NewTableDef("SCOTT", "emp")
	empno, _ := md.NewColumnDef("SCOTT", "emp", "EMPNO", 1, "NUMBER")
	empno.Nullable = "NO"
	ename, _ := md.NewColumnDef("SCOTT", "emp", "ENAME", 2, "VARCHAR2")
	ename.DataLength = 20
	t.AddColumn(empno)
	t.AddColumn(ename)
	t.AddPrimaryKey("pk_emp", "EMPNO")
	return t
}

func TestOracleChangelogTable(t *testing.T) {
	got, err := (OracleCDCBuilder{}).BuildChangelogTable(oTestTableDef(), dialect.CDCOptions{})
	if err != nil {
		t.Fatalf("BuildChangelogTable: %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE "SCOTT"."OWL_CHG_EMP"`,
		`"CHG_ID" NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY`,
		`"SHARD_ID" NUMBER DEFAULT 0 NOT NULL`,
		`"OP_TYPE" CHAR(1) NOT NULL`,
		`"OLD_DATA" CLOB`,
		`"NEW_DATA" CLOB`,
		`"CHG_TIME" TIMESTAMP DEFAULT SYSTIMESTAMP`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("changelog DDL missing %q\n%s", want, got)
		}
	}
}

func TestOracleSyncTrigger(t *testing.T) {
	got, err := (OracleCDCBuilder{}).BuildSyncTrigger(oTestTableDef(), dialect.CDCOptions{})
	if err != nil {
		t.Fatalf("BuildSyncTrigger: %v", err)
	}
	for _, want := range []string{
		"AFTER INSERT OR UPDATE OR DELETE ON",
		`"SCOTT"."EMP"`,
		"INSERTING",
		"UPDATING",
		"DELETING",
		"JSON_OBJECT",
		"KEY 'EMPNO' VALUE :NEW.EMPNO",
		"AFTER TRUNCATE ON SCHEMA",
		"ORA_DICT_OBJ_NAME",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trigger DDL missing %q\n%s", want, got)
		}
	}
}
