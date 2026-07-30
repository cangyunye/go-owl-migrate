package cmd

import (
	"path/filepath"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestNewMigrateState(t *testing.T) {
	ms := newMigrateState("oracle", "postgres")
	if ms.Source != "oracle" {
		t.Errorf("Source = %q, want oracle", ms.Source)
	}
	if ms.Target != "postgres" {
		t.Errorf("Target = %q, want postgres", ms.Target)
	}
	if ms.Tables == nil {
		t.Error("Tables map should be initialized")
	}
	if ms.Version != 1 {
		t.Errorf("Version = %d, want 1", ms.Version)
	}
}

func TestMarkExported_Success(t *testing.T) {
	ms := newMigrateState("ora", "pg")
	tbl := newTable("SCOTT", "EMP")
	key := tableKey(tbl)
	ms.markExported(key, 14, nil)

	ck := ms.Tables[key]
	if !ck.Exported {
		t.Error("expected exported=true")
	}
	if ck.ExportedRows != 14 {
		t.Errorf("rows = %d, want 14", ck.ExportedRows)
	}
	if ck.Status != "" {
		t.Errorf("status = %q, want empty", ck.Status)
	}
	if ck.Error != "" {
		t.Errorf("error = %q, want empty", ck.Error)
	}
}

func TestMarkExported_Failure(t *testing.T) {
	ms := newMigrateState("ora", "pg")
	tbl := newTable("SCOTT", "EMP")
	key := tableKey(tbl)
	ms.markExported(key, 0, errCustom("connection refused"))

	ck := ms.Tables[key]
	if !ck.Exported {
		t.Error("expected exported=true")
	}
	if ck.ExportedRows != 0 {
		t.Errorf("rows = %d, want 0", ck.ExportedRows)
	}
	if ck.Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", ck.Status)
	}
	if ck.Error != "connection refused" {
		t.Errorf("error = %q, want connection refused", ck.Error)
	}
}

func TestMarkImported_Success(t *testing.T) {
	ms := newMigrateState("ora", "pg")
	tbl := newTable("SCOTT", "EMP")
	key := tableKey(tbl)
	ms.markImported(key, 14, nil)

	ck := ms.Tables[key]
	if !ck.Imported {
		t.Error("expected imported=true")
	}
	if ck.Status != "SUCCESS" {
		t.Errorf("status = %q, want SUCCESS", ck.Status)
	}
}

func TestMarkImported_Failure(t *testing.T) {
	ms := newMigrateState("ora", "pg")
	tbl := newTable("SCOTT", "EMP")
	key := tableKey(tbl)
	ms.markImported(key, 0, errCustom("deadlock detected"))

	ck := ms.Tables[key]
	if !ck.Imported {
		t.Error("expected imported=true")
	}
	if ck.Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", ck.Status)
	}
	if ck.Error != "deadlock detected" {
		t.Errorf("error = %q, want deadlock detected", ck.Error)
	}
}

func TestMigrateState_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	ms := newMigrateState("oracle", "postgres")
	tbl1 := newTable("SCOTT", "EMP")
	tbl2 := newTable("SCOTT", "DEPT")

	ms.markExported(tableKey(tbl1), 14, nil)
	ms.markImported(tableKey(tbl1), 14, nil)
	ms.markExported(tableKey(tbl2), 4, nil)

	if err := saveMigrateState(path, ms); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadMigrateState(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Source != "oracle" {
		t.Errorf("Source = %q", loaded.Source)
	}
	if loaded.Target != "postgres" {
		t.Errorf("Target = %q", loaded.Target)
	}

	emp, ok := loaded.Tables["scott.emp"]
	if !ok {
		t.Fatal("scott.emp missing")
	}
	if !emp.Exported || !emp.Imported {
		t.Error("expected both exported and imported")
	}
	if emp.ExportedRows != 14 {
		t.Errorf("rows = %d, want 14", emp.ExportedRows)
	}
	if emp.Status != "SUCCESS" {
		t.Errorf("status = %q, want SUCCESS", emp.Status)
	}

	dept, ok := loaded.Tables["scott.dept"]
	if !ok {
		t.Fatal("scott.dept missing")
	}
	if !dept.Exported {
		t.Error("expected exported")
	}
	if dept.Imported {
		t.Error("expected imported=false")
	}
}

func TestMigrateState_LoadNonexistent(t *testing.T) {
	_, err := loadMigrateState("/nonexistent/state.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMigrateState_ResumeSkipsSuccess(t *testing.T) {
	ms := newMigrateState("ora", "pg")

	// EMP fully done (SUCCESS), DEPT exported but not imported
	emp := newTable("SCOTT", "EMP")
	dept := newTable("SCOTT", "DEPT")
	bonus := newTable("SCOTT", "BONUS")

	ms.markExported(tableKey(emp), 14, nil)
	ms.markImported(tableKey(emp), 14, nil)
	ms.markExported(tableKey(dept), 4, nil)
	// bonus not started

	all := []*md.TableDef{emp, dept, bonus}
	var toProcess []*md.TableDef
	for _, tbl := range all {
		key := tableKey(tbl)
		st := ms.Tables[key]
		if st.Status == "SUCCESS" {
			continue
		}
		toProcess = append(toProcess, tbl)
	}

	if len(toProcess) != 2 {
		t.Errorf("toProcess = %d, want 2 (DEPT + BONUS)", len(toProcess))
	}
	if toProcess[0].TableName != "DEPT" {
		t.Errorf("first = %s, want DEPT", toProcess[0].TableName)
	}
}

func TestTableKey(t *testing.T) {
	tbl := newTable("SCOTT", "EMP")
	key := tableKey(tbl)
	if key != "scott.emp" {
		t.Errorf("key = %q, want scott.emp", key)
	}
}

func TestNewMigrationReport(t *testing.T) {
	r := NewMigrationReport("oracle", "postgres")
	if r.SourceDialect != "oracle" {
		t.Errorf("SourceDialect = %q", r.SourceDialect)
	}
	if r.TargetDialect != "postgres" {
		t.Errorf("TargetDialect = %q", r.TargetDialect)
	}
	if r.Tables != nil {
		t.Error("Tables should be nil until first AddTable")
	}
	if r.GeneratedAt == "" {
		t.Error("GeneratedAt should be set")
	}
}

func TestMigrationReport_AddTable_NoErrors(t *testing.T) {
	r := NewMigrationReport("o", "p")
	r.AddTable("SCOTT", "EMP", 14, 14, 0, 0, "")

	if len(r.Tables) != 1 {
		t.Fatalf("Tables = %d", len(r.Tables))
	}
	tr := r.Tables[0]
	if tr.Schema != "SCOTT" || tr.Table != "EMP" {
		t.Errorf("unexpected table: %s.%s", tr.Schema, tr.Table)
	}
	if tr.Expected != 14 || tr.Actual != 14 {
		t.Errorf("expected=%d actual=%d", tr.Expected, tr.Actual)
	}
	if r.TotalExpected != 14 {
		t.Errorf("TotalExpected = %d", r.TotalExpected)
	}
	if r.TotalActual != 14 {
		t.Errorf("TotalActual = %d", r.TotalActual)
	}
}

func TestMigrationReport_AddTable_WithErrors(t *testing.T) {
	r := NewMigrationReport("o", "p")
	r.AddTable("SCOTT", "EMP", 14, 10, 2, 2, "timeout")
	r.AddTable("SCOTT", "DEPT", 4, 4, 0, 0, "")

	if r.TotalExpected != 18 {
		t.Errorf("TotalExpected = %d", r.TotalExpected)
	}
	if r.TotalActual != 14 {
		t.Errorf("TotalActual = %d", r.TotalActual)
	}
	if r.TotalSkipped != 2 {
		t.Errorf("TotalSkipped = %d", r.TotalSkipped)
	}
	if r.TotalErrors != 2 {
		t.Errorf("TotalErrors = %d", r.TotalErrors)
	}
}

func TestMigrationReport_Print_StatusSuccess(t *testing.T) {
	r := NewMigrationReport("o", "p")
	r.AddTable("SCOTT", "EMP", 14, 14, 0, 0, "")
	r.AddTable("SCOTT", "DEPT", 4, 4, 0, 0, "")
	r.Print()

	if r.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", r.Status)
	}
}

func TestMigrationReport_Print_StatusPartial(t *testing.T) {
	r := NewMigrationReport("o", "p")
	r.AddTable("SCOTT", "EMP", 14, 10, 2, 2, "timeout")
	r.AddTable("SCOTT", "DEPT", 4, 4, 0, 0, "")
	r.Print()

	if r.Status != "PARTIAL" {
		t.Errorf("Status = %q, want PARTIAL", r.Status)
	}
}

func TestMigrationReport_Print_StatusMismatch(t *testing.T) {
	r := NewMigrationReport("o", "p")
	r.AddTable("SCOTT", "EMP", 14, 12, 0, 0, "") // 2 missing
	r.Print()

	if r.Status != "PARTIAL" {
		t.Errorf("Status = %q, want PARTIAL", r.Status)
	}
}

// ── helpers ──

func newTable(schema, name string) *md.TableDef {
	tbl, err := md.NewTableDef(schema, name)
	if err != nil {
		panic(err)
	}
	return tbl
}

type errCustom string

func (e errCustom) Error() string { return string(e) }
