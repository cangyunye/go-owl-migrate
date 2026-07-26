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
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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

func TestImportOneTable_SkipRow(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "skip_row", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 1 || res.Skipped != 1 || res.Errors != 1 {
		t.Errorf("got actual=%d skipped=%d errors=%d, want 1/1/1", res.Actual, res.Skipped, res.Errors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportOneTable_Stop(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{ErrorPolicy: "stop", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
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
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("dup key"))
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

func TestImportOneTable_TruncateBefore(t *testing.T) {
	imp, mock, tbl := newImportMock(t, Config{TruncateBefore: true, CommitInterval: 100})

	mock.ExpectExec("TRUNCATE TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	res := imp.importOneTable(context.Background(), tbl, "SCOTT")
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Actual != 3 {
		t.Errorf("got actual=%d, want 3", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
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
	mock.ExpectExec("INSERT INTO").WithArgs("1", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WithArgs("2", "bob").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectExec("INSERT INTO").WithArgs("1", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WithArgs("2", "5000").WillReturnResult(sqlmock.NewResult(0, 1))
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
