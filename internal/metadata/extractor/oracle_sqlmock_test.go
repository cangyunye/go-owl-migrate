package extractor

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// stubOraclePartitions returns an empty all_part_tables result. QueryTables
// calls enrichOraclePartitions unconditionally; without this the Wire querier
// would issue an unexpected query (swallowed as best-effort, but noisy).
func stubOraclePartitions(mock sqlmock.Sqlmock, schema string) {
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_part_tables")).
		WithArgs(schema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "partitioning_type", "partition_count", "key_cols"}))
}

// TestQueryTables_OceanBase_NullTablespaceTemporary guards Problem 3: OceanBase
// Oracle tenants return NULL for all_tables.tablespace_name / temporary, which
// used to fail with "converting NULL to string is unsupported". The Wire
// querier must scan them as sql.NullString and yield empty strings.
func TestQueryTables_OceanBase_NullTablespaceTemporary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SIT"
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_tables t")).
		WithArgs(schema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "tablespace_name", "num_rows", "comments", "temporary"}).
			AddRow("EMP", nil, 14, nil, nil))
	stubOraclePartitions(mock, schema)

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	tables, err := q.QueryTables(db, schema)
	if err != nil {
		t.Fatalf("QueryTables: %v", err)
	}
	if len(tables) != 1 || tables[0].TableName != "EMP" {
		t.Fatalf("tables = %+v, want single EMP", tables)
	}
	if tables[0].Tablespace != "" {
		t.Errorf("Tablespace = %q, want empty (was NULL)", tables[0].Tablespace)
	}
	if tables[0].Temporary != "" {
		t.Errorf("Temporary = %q, want empty (was NULL)", tables[0].Temporary)
	}
	if tables[0].RowCount != 14 {
		t.Errorf("RowCount = %d, want 14", tables[0].RowCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryTables_NativeOracle_NonNull ensures the NULL-tolerant scan does not
// regress native Oracle, where tablespace/temporary are non-null.
func TestQueryTables_NativeOracle_NonNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SCOTT"
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_tables t")).
		WithArgs(schema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "tablespace_name", "num_rows", "comments", "temporary"}).
			AddRow("EMP", "USERS", 14, "employee table", "N"))
	stubOraclePartitions(mock, schema)

	q := OracleMetadataQuerier{}
	tables, err := q.QueryTables(db, schema)
	if err != nil {
		t.Fatalf("QueryTables: %v", err)
	}
	if len(tables) != 1 || tables[0].TableName != "EMP" {
		t.Fatalf("tables = %+v, want single EMP", tables)
	}
	if tables[0].Tablespace != "USERS" {
		t.Errorf("Tablespace = %q, want USERS", tables[0].Tablespace)
	}
	if tables[0].Temporary != "N" {
		t.Errorf("Temporary = %q, want N", tables[0].Temporary)
	}
	if tables[0].TableComment != "employee table" {
		t.Errorf("TableComment = %q, want 'employee table'", tables[0].TableComment)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryColumns_OceanBase_NoCollation guards Problem 2: the Wire querier must
// not reference all_tab_columns.collation (ORA-00904 in older OB) nor the
// nonexistent all_tab_identity_cols view. The generated SQL must omit both, the
// row has exactly 12 columns, and col.Collation stays empty.
func TestQueryColumns_OceanBase_NoCollation(t *testing.T) {
	// Default matcher is QueryMatcherRegexp; capture the actual SQL and fail if
	// it references collation or the hallucinated identity view.
	var gotSQL string
	matcher := sqlmock.QueryMatcherFunc(func(expectedSQL, actualSQL string) error {
		gotSQL = actualSQL
		if re := regexp.MustCompile(`(?i)collation`); re.MatchString(actualSQL) {
			t.Fatalf("oceanbase-oracle columns query must not reference collation, got: %s", actualSQL)
		}
		if re := regexp.MustCompile(`(?i)all_tab_identity_cols|all_sequences`); re.MatchString(actualSQL) {
			t.Fatalf("oceanbase-oracle columns query must not reference identity views, got: %s", actualSQL)
		}
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SIT"
	rows := sqlmock.NewRows([]string{
		"table_name", "column_name", "ordinal_position", "data_type",
		"data_length", "data_precision", "data_scale", "nullable", "data_default",
		"comments", "char_used", "charset",
	}).
		AddRow("EMP", "EMPNO", 1, "NUMBER", 22, 4, 0, "N", nil, nil, "B", "UTF8MB4")

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	mock.ExpectQuery(`FROM all_tab_columns c`).
		WithArgs(schema).WillReturnRows(rows)

	columns, err := q.QueryColumns(db, schema)
	if err != nil {
		t.Fatalf("QueryColumns: %v", err)
	}
	if gotSQL == "" {
		t.Fatal("no SQL captured")
	}
	if len(columns) != 1 || columns[0].ColumnName != "EMPNO" {
		t.Fatalf("columns = %+v, want single EMPNO", columns)
	}
	if columns[0].Collation != "" {
		t.Errorf("Collation = %q, want empty", columns[0].Collation)
	}
	if columns[0].IsIdentity == "YES" {
		t.Errorf("IsIdentity = %q, want NO (no identity source in OB)", columns[0].IsIdentity)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryColumns_NativeOracle_HasCollation ensures native Oracle still selects
// and scans the collation column (17 columns) so collation metadata roundtrips.
func TestQueryColumns_NativeOracle_HasCollation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SCOTT"
	rows := sqlmock.NewRows([]string{
		"table_name", "column_name", "ordinal_position", "data_type",
		"data_length", "data_precision", "data_scale", "nullable", "data_default",
		"comments", "char_used", "charset", "collation",
		"identity_column", "identity_generation", "identity_start", "identity_increment",
	}).
		AddRow("EMP", "ENAME", 2, "VARCHAR2", 10, 0, 0, "N", nil, "name", "B", "UTF8", "UTF8_BIN", "NO", nil, nil, nil)

	q := OracleMetadataQuerier{}
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_tab_columns c")).
		WithArgs(schema).WillReturnRows(rows)

	columns, err := q.QueryColumns(db, schema)
	if err != nil {
		t.Fatalf("QueryColumns: %v", err)
	}
	if len(columns) != 1 || columns[0].ColumnName != "ENAME" {
		t.Fatalf("columns = %+v, want single ENAME", columns)
	}
	if columns[0].Collation != "UTF8_BIN" {
		t.Errorf("Collation = %q, want UTF8_BIN", columns[0].Collation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
