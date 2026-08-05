package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestStatementBatchRows(t *testing.T) {
	imp := New(nil, Config{CommitInterval: 1000})

	if got := imp.statementBatchRows(10, true); got != 1000 {
		t.Errorf("batch = %d, want 1000", got)
	}
	// 100 columns caps the batch at 65535/100 = 655
	if got := imp.statementBatchRows(100, true); got != 655 {
		t.Errorf("batch = %d, want 655 (args cap)", got)
	}
	if got := imp.statementBatchRows(10, false); got != 1 {
		t.Errorf("single-row engine batch = %d, want 1", got)
	}
	if got := imp.statementBatchRows(0, true); got != 1000 {
		t.Errorf("zero-column batch = %d, want commit interval", got)
	}
}

func TestBuildMultiRowInsert(t *testing.T) {
	t.Run("postgres numbers continue across rows", func(t *testing.T) {
		imp := New(nil, Config{TargetDBType: "postgres"})
		got := imp.buildMultiRowInsert("scott", "emp", []string{`"id"`, `"name"`}, 2, 2)
		want := `INSERT INTO "scott"."emp" ("id", "name") VALUES ($1, $2), ($3, $4)`
		if got != want {
			t.Errorf("multi-row insert\n got: %s\nwant: %s", got, want)
		}
	})
	t.Run("mysql repeats qmark", func(t *testing.T) {
		imp := New(nil, Config{TargetDBType: "mysql"})
		got := imp.buildMultiRowInsert("scott", "emp", []string{"`id`", "`name`"}, 2, 2)
		want := "INSERT INTO `scott`.`emp` (`id`, `name`) VALUES (?, ?), (?, ?)"
		if got != want {
			t.Errorf("multi-row insert\n got: %s\nwant: %s", got, want)
		}
	})
	t.Run("oracle colon family", func(t *testing.T) {
		imp := New(nil, Config{TargetDBType: "oracle"})
		got := imp.buildMultiRowInsert("scott", "emp", []string{`"id"`}, 2, 1)
		want := `INSERT INTO "scott"."emp" ("id") VALUES (:1), (:2)`
		if got != want {
			t.Errorf("multi-row insert\n got: %s\nwant: %s", got, want)
		}
	})
}

func TestUseMultiRowInsert(t *testing.T) {
	tests := []struct {
		target string
		family string
		want   bool
	}{
		{"mysql", "", true},
		{"goldendb-mysql", "", true},
		{"postgres", "", true},
		{"oracle", "", false},
		{"goldendb-oracle", "", false},
		{"oceanbase-oracle", "qmark", true},
	}
	for _, tt := range tests {
		imp := New(nil, Config{TargetDBType: tt.target, PlaceholderFamily: tt.family})
		if got := imp.useMultiRowInsert(); got != tt.want {
			t.Errorf("useMultiRowInsert(%q, %q) = %v, want %v", tt.target, tt.family, got, tt.want)
		}
	}
}

func TestIsPlaceholderLimitError(t *testing.T) {
	for _, msg := range []string{
		"pq: got 131072 parameters but cannot exceed 65535",
		"prepared statement contains too many placeholders",
	} {
		if !isPlaceholderLimitError(fmt.Errorf("%s", msg)) {
			t.Errorf("should detect placeholder limit: %q", msg)
		}
	}
	if isPlaceholderLimitError(fmt.Errorf("duplicate key")) {
		t.Error("should not match unrelated error")
	}
}

func TestImportOneTable_PlaceholderBisect(t *testing.T) {
	dir := t.TempDir()
	content := "ID,NAME\n1,foo\n2,bar\n"
	if err := os.WriteFile(filepath.Join(dir, "scott.emp.csv"), []byte(content), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	imp := New(db, Config{SourceDir: dir, TargetDBType: "postgres", CommitInterval: 100})

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("too many placeholders"))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("RELEASE SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SAVEPOINT owl_batch").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
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

func TestUseCopyFastPath(t *testing.T) {
	tests := []struct {
		target  string
		useCopy bool
		want    bool
	}{
		{"postgres", true, true},
		{"postgres", false, false},
		{"mysql", true, false},
		{"oracle", true, false},
		{"oceanbase-oracle", true, false},
		{"opengaussdb", true, true},
	}
	for _, tt := range tests {
		imp := New(nil, Config{TargetDBType: tt.target, UseCopy: tt.useCopy})
		if got := imp.useCopyFastPath(); got != tt.want {
			t.Errorf("useCopyFastPath(%q, %v) = %v, want %v", tt.target, tt.useCopy, got, tt.want)
		}
	}
}

func TestImportViaCopy_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	imp := New(db, Config{TargetDBType: "postgres", UseCopy: true})

	mock.ExpectBegin()
	prep := mock.ExpectPrepare("COPY")
	prep.ExpectExec().WillReturnResult(sqlmock.NewResult(0, 1))
	prep.ExpectExec().WillReturnResult(sqlmock.NewResult(0, 1))
	prep.ExpectExec().WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	res := ImportResult{}
	err = imp.importViaCopy(context.Background(), db, "scott", "emp",
		[]string{"id", "name"}, [][]any{{"1", "foo"}, {"2", nil}}, []int{0, 1}, &res)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Actual != 2 {
		t.Errorf("got actual=%d, want 2", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestImportViaCopy_PrepFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	imp := New(db, Config{TargetDBType: "postgres", UseCopy: true})

	mock.ExpectBegin()
	mock.ExpectPrepare("COPY").WillReturnError(fmt.Errorf("copy not supported"))
	mock.ExpectRollback()

	res := ImportResult{}
	err = imp.importViaCopy(context.Background(), db, "scott", "emp",
		[]string{"id"}, [][]any{{"1"}}, []int{0}, &res)
	if err == nil {
		t.Fatal("expected error when prepare fails")
	}
	if res.Actual != 0 {
		t.Errorf("got actual=%d, want 0", res.Actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestConvertRow_HexBinaryError(t *testing.T) {
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col, _ := md.NewColumnDef("SCOTT", "EMP", "DATA", 1, "BLOB")
	tbl.AddColumn(col)

	imp := New(nil, Config{TargetDBType: "postgres"})
	_, err := imp.convertRow(tbl, []string{"DATA"}, []string{"nothex"})
	if err == nil || !strings.Contains(err.Error(), "hex") {
		t.Errorf("expected hex decode error, got: %v", err)
	}
}
