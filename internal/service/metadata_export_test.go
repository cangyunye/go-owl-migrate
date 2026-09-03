package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func exportFixture(t *testing.T) *md.SchemaModel {
	t.Helper()
	sm := md.NewSchemaModel()
	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatal(err)
	}
	tbl.TableComment = "员工表"
	tbl.Owner = "SCOTT"
	tbl.Partitioned = "YES"
	tbl.PartitionInfo = "PARTITION BY HASH (EMPNO) PARTITIONS 4"
	col, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	col.DataPrecision = 4
	col.Nullable = "NO"
	col.IsIdentity = "YES"
	tbl.AddColumn(col)
	tbl.AddPrimaryKey("SYS_C001", "EMPNO")
	tbl.AddIndex(&md.IndexDef{TableSchema: "SCOTT", TableName: "EMP", IndexName: "IDX_E",
		IndexType: "BTREE", Uniqueness: "NONUNIQUE", ColumnName: "EMPNO", OrdinalPosition: 1})
	if err := sm.AddTable(tbl); err != nil {
		t.Fatal(err)
	}
	sm.AddView(&md.ViewDef{ViewSchema: "SCOTT", ViewName: "EMP_V", ViewDefinition: "SELECT 1", ViewComment: "v"})
	sm.AddSequence(&md.SequenceDef{SequenceSchema: "SCOTT", SequenceName: "SEQ1", StartValue: 1, IncrementBy: 1, MaxValue: 99, Cycle: "NO", CacheSize: 20, DataType: "NUMBER"})
	sm.AddTrigger(&md.TriggerDef{TriggerSchema: "SCOTT", TriggerName: "TRG1", TableSchema: "SCOTT", TableName: "EMP",
		TriggerType: "BEFORE", TriggerEvent: "INSERT", Status: "ENABLED", ForEach: "ROW", Language: "PLSQL", TriggerBody: "BEGIN NULL; END;"})
	sm.AddFunction(&md.FunctionDef{FunctionSchema: "SCOTT", FunctionName: "FN1", FunctionType: "FUNCTION",
		ReturnType: "NUMBER", Language: "PLSQL", Status: "ENABLED", FunctionBody: "RETURN 1;"})
	sm.AddSynonym(&md.SynonymDef{SynonymName: "EMP_SYN", SynonymSchema: "SCOTT", TargetSchema: "SCOTT", TargetName: "EMP", IsPublic: "NO"})
	sm.AddMView(&md.MViewDef{MViewSchema: "SCOTT", MViewName: "MV1", MViewQuery: "SELECT 1"})
	sm.AddPackage(&md.PackageDef{PackageSchema: "SCOTT", PackageName: "PKG1", Status: "ENABLED", PackageSpec: "PACKAGE PKG1 IS END;"})
	sm.AddPackageBody(&md.PackageBodyDef{PackageSchema: "SCOTT", PackageName: "PKG1", PackageBody: "PACKAGE BODY PKG1 IS END;", Status: "ENABLED"})
	return sm
}

func headerLine(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.SplitN(string(b), "\n", 2)[0]
}

func TestExportMetadataFilesAll(t *testing.T) {
	sm := exportFixture(t)
	dir := t.TempDir()
	files, err := ExportMetadataFiles(dir, sm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 13 {
		t.Fatalf("files = %d, want 13: %v", len(files), files)
	}
	// tables.csv must carry the full canonical column set (incl OWNER/分区)
	h := headerLine(t, dir, "tables.csv")
	for _, want := range []string{"TABLE_SCHEMA", "OWNER", "PARTITIONED", "PARTITION_INFO", "ENGINE", "TABLE_COMMENT"} {
		if !strings.Contains(h, want) {
			t.Errorf("tables.csv header missing %s: %s", want, h)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "tables.csv"))
	if !strings.Contains(string(b), "PARTITION BY HASH") || !strings.Contains(string(b), "员工表") {
		t.Errorf("tables.csv should carry partition info and comment:\n%s", b)
	}
	// columns.csv canonical incl identity
	hc := headerLine(t, dir, "columns.csv")
	for _, want := range []string{"IS_IDENTITY", "CHARACTER_SET", "COLLATION", "ON_UPDATE"} {
		if !strings.Contains(hc, want) {
			t.Errorf("columns.csv header missing %s: %s", want, hc)
		}
	}
	for _, name := range []string{
		"views.csv", "mviews.csv", "sequences.csv", "synonyms.csv",
		"triggers.csv", "functions.csv", "packages.csv", "package_bodies.csv",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
}

func TestExportMetadataFilesObjectFilter(t *testing.T) {
	sm := exportFixture(t)
	dir := t.TempDir()
	set, err := md.ParseObjectTypes("views,sequences")
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExportMetadataFiles(dir, sm, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want views.csv + sequences.csv only", files)
	}
	for _, name := range []string{"views.csv", "sequences.csv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tables.csv")); err == nil {
		t.Error("tables.csv should not be written when tables not selected")
	}
}

func TestExportMetadataFilesRows(t *testing.T) {
	sm := exportFixture(t)
	dir := t.TempDir()
	if _, err := ExportMetadataFiles(dir, sm, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"functions.csv":     "FN1",
		"packages.csv":      "PKG1",
		"package_bodies.csv": "PKG1",
		"mviews.csv":        "MV1",
		"synonyms.csv":      "EMP_SYN",
		"sequences.csv":     "SEQ1",
		"triggers.csv":      "TRG1",
		"views.csv":         "EMP_V",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s missing %s", name, want)
		}
	}
}
