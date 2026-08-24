package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func newImportMock(t *testing.T, cfg Config) (*Importer, sqlmock.Sqlmock, *md.TableDef) {
	t.Helper()
	dir := t.TempDir()
	content := "ID,NAME\n1,foo\n2,bar\n3,baz\n"
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte(content), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	cfg.SourceDir = dir
	if cfg.TargetDBType == "" {
		cfg.TargetDBType = "postgres"
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	return New(db, cfg), mock, tbl
}

func TestImportOneTable_HappyPath_CommitInterval(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{CommitInterval: 2})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Expected != 3 || res.Actual != 3 || res.Errors != 0 {
		t.Errorf("got expected=%d actual=%d errors=%d, want 3/3/0", res.Expected, res.Actual, res.Errors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// expectRowSalvage queues the savepoint-wrapped row-by-row re-insert of a failed
// batch. failsAt lists chunk-relative indexes that should error; the rest succeed.
func expectRowSalvage(mock sqlmock.Sqlmock, numRows int, failsAt ...int) {
	fail := make(map[int]bool)
	for _, i := range failsAt {
		fail[i] = true
	}
	for i := 0; i < numRows; i++ {
		mock.ExpectExec("SAVEPOINT owl_row").WillReturnResult(sqlmock.NewResult(0, 0))
		if fail[i] {
			mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
			mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_row").WillReturnResult(sqlmock.NewResult(0, 0))
		} else {
			mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("RELEASE SAVEPOINT owl_row").WillReturnResult(sqlmock.NewResult(0, 0))
		}
	}
}

func TestImportOneTable_SkipRow(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "skip_row", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	expectRowSalvage(mock, 3, 1)
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	// Salvage re-inserts the whole failed batch row by row, so only the bad
	// row is lost (rows before it are preserved).
	if res.Actual != 2 || res.Skipped != 1 || res.Errors != 1 {
		t.Errorf("got actual=%d skipped=%d errors=%d, want 2/1/1", res.Actual, res.Skipped, res.Errors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_Stop(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "stop", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err == nil {
		t.Fatal("expected error for stop policy, got nil")
	}
	if res.Actual != 0 || res.Errors != 1 {
		t.Errorf("got actual=%d errors=%d, want 0/1", res.Actual, res.Errors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_LogOnly(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "log_only", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	expectRowSalvage(mock, 3, 1)
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 2 || res.Errors != 1 || res.Skipped != 0 {
		t.Errorf("got actual=%d errors=%d skipped=%d, want 2/1/0", res.Actual, res.Errors, res.Skipped)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_SkipRow_MaxErrors(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "skip_row", MaxErrors: 1, CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	// Salvage starts; the first row fails and hits the error ceiling.
	mock.ExpectExec("SAVEPOINT owl_row").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_row").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err == nil {
		t.Fatal("expected error when max errors reached, got nil")
	}
	if res.Skipped != 1 || res.Errors != 1 {
		t.Errorf("got skipped=%d errors=%d, want 1/1", res.Skipped, res.Errors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportTables_TruncateFKBatch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"scott.dept.csv": "DEPTNO,DNAME\n10,ACCOUNTING\n",
		"scott.emp.csv":  "EMPNO,DEPTNO\n7369,10\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write csv: %v", err)
		}
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	dept, err := md.NewTableDef("SCOTT", "DEPT")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	emp, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}

	imp := New(db, Config{
		SourceDir:          dir,
		TruncateBefore:     true,
		RespectForeignKeys: true,
		CommitInterval:     100,
		TargetDBType:       "postgres",
		MaxWorkers:         2,
	})

	fkRows := sqlmock.NewRows([]string{"child_schema", "child_table", "parent_schema", "parent_table"}).
		AddRow("public", "EMP", "public", "DEPT")
	mock.ExpectQuery("pg_constraint").WillReturnRows(fkRows)

	// PG family: one multi-table TRUNCATE covers the FK cluster
	mock.ExpectExec(`TRUNCATE TABLE "public"\."DEPT", "public"\."EMP"`).WillReturnResult(sqlmock.NewResult(0, 0))

	// inserts respect FK order: parent (DEPT) before child (EMP)
	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO "public"\."DEPT"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO "public"\."EMP"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	results, err := imp.ImportTables(context.Background(), []*md.TableDef{dept, emp}, map[string]string{"SCOTT": "public"})
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("unexpected error for %s.%s: %v", r.Schema, r.Table, r.Err)
		}
		if r.Actual != 1 {
			t.Errorf("%s.%s: actual=%d, want 1", r.Schema, r.Table, r.Actual)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportTables_TruncateFallbackDelete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte("EMPNO\n1\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	emp, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}

	imp := New(db, Config{
		SourceDir:      dir,
		TruncateBefore: true,
		CommitInterval: 100,
		TargetDBType:   "postgres",
	})

	mock.ExpectQuery("pg_constraint").WillReturnRows(
		sqlmock.NewRows([]string{"child_schema", "child_table", "parent_schema", "parent_table"}))
	// batch statement fails (referenced by a table outside the batch), then
	// per-table TRUNCATE fails too and falls back to DELETE FROM
	fkErr := fmt.Errorf("pq: cannot truncate a table referenced in a foreign key constraint (0A000)")
	mock.ExpectExec(`TRUNCATE TABLE "public"\."EMP"`).WillReturnError(fkErr)
	mock.ExpectExec(`TRUNCATE TABLE "public"\."EMP"`).WillReturnError(fkErr)
	mock.ExpectExec(`DELETE FROM "public"\."EMP"`).WillReturnResult(sqlmock.NewResult(0, 5))

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO "public"\."EMP"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	results, err := imp.ImportTables(context.Background(), []*md.TableDef{emp}, map[string]string{"SCOTT": "public"})
	if err != nil {
		t.Fatalf("ImportTables: %v", err)
	}
	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestTopoOrder(t *testing.T) {
	order := topoOrder(3, [][2]int{{2, 0}, {2, 1}})
	if !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Errorf("got %v, want [0 1 2]", order)
	}

	cyclic := topoOrder(2, [][2]int{{0, 1}, {1, 0}})
	if len(cyclic) != 2 {
		t.Errorf("cyclic: got %v, want both indexes", cyclic)
	}
}

func TestImportOneTable_NullIf(t *testing.T) {
	dir := t.TempDir()
	content := "ID,NAME\n1,NULL\n2,bob\n"
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte(content), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	imp := New(db, Config{
		SourceDir:      dir,
		TargetDBType:   "postgres",
		CommitInterval: 100,
		NullIf:         []string{"NULL"},
	})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WithArgs("1", nil, "2", "bob").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 2 {
		t.Errorf("got actual=%d, want 2", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func newGuardImportMock(t *testing.T, cfg Config) (*Importer, sqlmock.Sqlmock, *md.TableDef) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte("ID,NAME\n1,foo\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	cfg.SourceDir = dir
	if cfg.CommitInterval == 0 {
		cfg.CommitInterval = 100
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	return New(db, cfg), mock, tbl
}

func TestUnit_GuardStatements(t *testing.T) {
	tests := []struct {
		name          string
		target        string
		disableConstr bool
		disableTrig   bool
		wantDisable   []string
		wantEnable    []string
	}{
		{
			name:          "postgres constraints",
			target:        "postgres",
			disableConstr: true,
			wantDisable:   []string{`ALTER TABLE "SCOTT"."EMP" DISABLE TRIGGER ALL`},
			wantEnable:    []string{`ALTER TABLE "SCOTT"."EMP" ENABLE TRIGGER ALL`},
		},
		{
			name:        "postgres triggers",
			target:      "postgres",
			disableTrig: true,
			wantDisable: []string{`ALTER TABLE "SCOTT"."EMP" DISABLE TRIGGER ALL`},
			wantEnable:  []string{`ALTER TABLE "SCOTT"."EMP" ENABLE TRIGGER ALL`},
		},
		{
			name:          "mysql constraints",
			target:        "mysql",
			disableConstr: true,
			wantDisable:   []string{"SET FOREIGN_KEY_CHECKS=0"},
			wantEnable:    []string{"SET FOREIGN_KEY_CHECKS=1"},
		},
		{
			name:        "mysql triggers unsupported",
			target:      "mysql",
			disableTrig: true,
			wantDisable: nil,
			wantEnable:  nil,
		},
		{
			name:        "oracle triggers",
			target:      "oracle",
			disableTrig: true,
			wantDisable: []string{`ALTER TABLE "SCOTT"."EMP" DISABLE ALL TRIGGERS`},
			wantEnable:  []string{`ALTER TABLE "SCOTT"."EMP" ENABLE ALL TRIGGERS`},
		},
		{
			name:        "none requested",
			target:      "postgres",
			wantDisable: nil,
			wantEnable:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := New(nil, Config{TargetDBType: tt.target, DisableConstraints: tt.disableConstr, DisableTriggers: tt.disableTrig})
			disable, enable := imp.guardStatements(context.Background(), nil, "SCOTT", "EMP")
			if !reflect.DeepEqual(disable, tt.wantDisable) {
				t.Errorf("disable = %v, want %v", disable, tt.wantDisable)
			}
			if !reflect.DeepEqual(enable, tt.wantEnable) {
				t.Errorf("enable = %v, want %v", enable, tt.wantEnable)
			}
		})
	}
}

func TestImportOneTable_PG_DisableTriggers(t *testing.T) {
	imp, mock, tbl := newGuardImportMock(t, Config{TargetDBType: "postgres", DisableTriggers: true})
	mock.ExpectExec("DISABLE TRIGGER ALL").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectExec("ENABLE TRIGGER ALL").WillReturnResult(sqlmock.NewResult(0, 0))

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 1 {
		t.Errorf("got actual=%d, want 1", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_MySQL_DisableConstraints(t *testing.T) {
	imp, mock, tbl := newGuardImportMock(t, Config{TargetDBType: "mysql", DisableConstraints: true})
	mock.ExpectExec("FOREIGN_KEY_CHECKS=0").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectExec("FOREIGN_KEY_CHECKS=1").WillReturnResult(sqlmock.NewResult(0, 0))

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 1 {
		t.Errorf("got actual=%d, want 1", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_Oracle_DisableConstraints(t *testing.T) {
	imp, mock, tbl := newGuardImportMock(t, Config{TargetDBType: "oracle", DisableConstraints: true})
	mock.ExpectExec("ALTER SESSION SET NLS_DATE_FORMAT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER SESSION SET NLS_TIMESTAMP_FORMAT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT").WillReturnResult(sqlmock.NewResult(0, 0))
	rows := sqlmock.NewRows([]string{"constraint_name"}).AddRow("FK_DEPTNO")
	mock.ExpectQuery("all_constraints").WithArgs("SCOTT", "EMP").WillReturnRows(rows)
	mock.ExpectExec("DISABLE CONSTRAINT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO").ExpectExec().WithArgs("1", "foo").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("ENABLE CONSTRAINT").WillReturnResult(sqlmock.NewResult(0, 0))

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 1 {
		t.Errorf("got actual=%d, want 1", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_GuardsReEnabledOnCancel(t *testing.T) {
	imp, mock, tbl := newGuardImportMock(t, Config{TargetDBType: "postgres", DisableTriggers: true})
	mock.ExpectExec("ENABLE TRIGGER ALL").WillReturnResult(sqlmock.NewResult(0, 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := imp.importOneTable(ctx, tbl, "SCOTT")
	if res.Err == nil {
		t.Error("expected context cancellation error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("enable guard must still be issued on cancel: %v", err)
	}
}

func TestImportOneTable_NumericZeroNotNull(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte("ID,SAL\n1,0\n2,5000\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	id, _ := md.NewColumnDef("SCOTT", "EMP", "ID", 1, "NUMBER")
	sal, _ := md.NewColumnDef("SCOTT", "EMP", "SAL", 2, "NUMBER")
	tbl.AddColumn(id)
	tbl.AddColumn(sal)

	imp := New(db, Config{SourceDir: dir, TargetDBType: "postgres", CommitInterval: 100, NumericZeroNotNull: true})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WithArgs("1", nil, "2", "5000").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 2 {
		t.Errorf("got actual=%d, want 2", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_DropIndexes(t *testing.T) {
	imp, mock, tbl := newGuardImportMock(t, Config{
		TargetDBType: "postgres",
		DropIndexes:  true,
		IndexDDL: func(*md.TableDef) ([]string, []string) {
			return []string{`DROP INDEX "scott"."idx_emp"`}, []string{`CREATE INDEX "idx_emp" ON "scott"."emp" ("ename")`}
		},
	})
	mock.ExpectExec("DROP INDEX").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectExec("CREATE INDEX").WillReturnResult(sqlmock.NewResult(0, 0))

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 1 {
		t.Errorf("got actual=%d, want 1", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
