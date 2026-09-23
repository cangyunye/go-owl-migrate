package extractor

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// tableRows is the result set of the PostgreSQL tables query.
func tableRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"table_name", "table_type", "table_comment", "partitioned"}).
		AddRow("EMP", "BASE TABLE", "", "NO").
		AddRow("EMPTY_T", "BASE TABLE", "", "NO")
}

// TestQueryTablesEnrichesRowCounts covers the source-table list showing "0 行"
// for every table: the PG querier left RowCount unset while the MySQL and
// Oracle queriers fill it from planner statistics.
func TestQueryTablesEnrichesRowCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`FROM information_schema\.tables`).WithArgs("public").WillReturnRows(tableRows())
	mock.ExpectQuery(`FROM pg_class c`).WithArgs("public").WillReturnRows(
		sqlmock.NewRows([]string{"relname", "estimate"}).
			AddRow("EMP", 14).
			// A never-analyzed table reports -1; it must not surface as -1 行.
			AddRow("EMPTY_T", -1))

	tables, err := (PGMetadataQuerier{}).QueryTables(db, "public")
	if err != nil {
		t.Fatalf("QueryTables: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %d, want 2", len(tables))
	}
	if got := tables[0].RowCount; got != 14 {
		t.Errorf("EMP RowCount = %d, want 14", got)
	}
	if got := tables[1].RowCount; got != 0 {
		t.Errorf("EMPTY_T RowCount = %d, want 0 (clamped, never analyzed)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryTablesRowCountFailureIsTolerated pins the best-effort contract: the
// row estimate is display-only, so a failing enrichment query must not fail
// extraction (e.g. a server that does not expose pg_class.reltuples).
func TestQueryTablesRowCountFailureIsTolerated(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`FROM information_schema\.tables`).WithArgs("public").WillReturnRows(tableRows())
	mock.ExpectQuery(`FROM pg_class c`).WithArgs("public").
		WillReturnError(errors.New(`pq: column "reltuples" does not exist`))

	tables, err := (PGMetadataQuerier{}).QueryTables(db, "public")
	if err != nil {
		t.Fatalf("QueryTables should tolerate a failed row estimate, got: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %d, want 2", len(tables))
	}
	for _, tbl := range tables {
		if tbl.RowCount != 0 {
			t.Errorf("%s RowCount = %d, want 0", tbl.TableName, tbl.RowCount)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
