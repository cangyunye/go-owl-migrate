package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
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
		"functions.csv":      "FN1",
		"packages.csv":       "PKG1",
		"package_bodies.csv": "PKG1",
		"mviews.csv":         "MV1",
		"synonyms.csv":       "EMP_SYN",
		"sequences.csv":      "SEQ1",
		"triggers.csv":       "TRG1",
		"views.csv":          "EMP_V",
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

// ── 往返自检：规范 CSV 导出 → csv loader 读回一致 ──

func TestExportMetadataRoundtrip(t *testing.T) {
	sm := exportFixture(t)
	dir := t.TempDir()
	if _, err := ExportMetadataFiles(dir, sm, nil); err != nil {
		t.Fatal(err)
	}

	// loader 支持目录 = 直接指向导出目录；校验载入的表/列核心字段
	cfg := &config.Config{Metadata: config.MetadataConfig{Type: "csv", CSV: config.CSVConfig{
		Path: dir, Delimiter: ",", Encoding: "utf-8", HasHeader: true, ColumnNameMatching: "case_insensitive"}}}
	got, err := LoadMetadata(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.GetTables()); n != 1 {
		t.Fatalf("tables after roundtrip = %d, want 1", n)
	}
	back := got.GetTables()[0]
	if back.TableName != "EMP" || back.Owner != "SCOTT" || back.TableComment != "员工表" {
		t.Errorf("table fidelity lost: %+v", back)
	}
	if back.Partitioned != "YES" || !strings.Contains(back.PartitionInfo, "PARTITION BY HASH") {
		t.Errorf("partition fidelity lost: %+v", back)
	}
	cols := back.GetColumns()
	if len(cols) != 1 || cols[0].ColumnName != "EMPNO" || cols[0].IsIdentity != "YES" {
		t.Errorf("column fidelity lost: %+v", cols)
	}
	if len(back.GetPrimaryKeys()) != 1 {
		t.Error("primary key fidelity lost")
	}
	// 对象文件回读
	for _, check := range []struct{ name, want string }{
		{"views", "EMP_V"}, {"sequences", "SEQ1"}, {"synonyms", "EMP_SYN"},
		{"triggers", "TRG1"}, {"functions", "FN1"}, {"packages", "PKG1"},
		{"mviews", "MV1"}, {"package_bodies", "PKG1"},
	} {
		if !loaderHas(got, check.name, check.want) {
			t.Errorf("roundtrip lost %s (%s)", check.name, check.want)
		}
	}
}

func loaderHas(sm *md.SchemaModel, kind, name string) bool {
	switch kind {
	case "views":
		for _, v := range sm.Views {
			if v.ViewName == name {
				return true
			}
		}
	case "mviews":
		for _, v := range sm.GetMViews() {
			if v.MViewName == name {
				return true
			}
		}
	case "sequences":
		for _, sch := range sm.Schemas() {
			for _, s := range sm.GetSequences(sch) {
				if s.SequenceName == name {
					return true
				}
			}
		}
	case "synonyms":
		for _, s := range sm.Synonyms {
			if s.SynonymName == name {
				return true
			}
		}
	case "triggers":
		for _, tbl := range sm.GetTables() {
			for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
				if trg.TriggerName == name {
					return true
				}
			}
		}
	case "functions":
		for _, sch := range sm.Schemas() {
			for _, f := range sm.GetFunctions(sch) {
				if f.FunctionName == name {
					return true
				}
			}
		}
	case "packages":
		for _, sch := range sm.Schemas() {
			for _, p := range sm.GetPackages(sch) {
				if p.PackageName == name {
					return true
				}
			}
		}
	case "package_bodies":
		for _, sch := range sm.Schemas() {
			for _, p := range sm.GetPackageBodies(sch) {
				if p.PackageName == name {
					return true
				}
			}
		}
	}
	return false
}
