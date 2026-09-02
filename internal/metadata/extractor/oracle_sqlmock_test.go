package extractor

import (
	"errors"
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

// TestQueryColumns_OceanBase_NullOrdinal guards Problem 4: OceanBase Oracle
// tenants return NULL for all_tab_columns.column_id on some columns, which
// used to fail with "converting NULL to int is unsupported". The ordinal must
// be scanned as sql.NullInt64 and fall back to the per-table scan order so the
// column keeps a valid (>=1) ordinal and the migration does not abort.
func TestQueryColumns_OceanBase_NullOrdinal(t *testing.T) {
	db, mock, err := sqlmock.New()
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
		AddRow("EMP", "EMPNO", nil, "NUMBER", 22, 4, 0, "N", nil, nil, "B", "UTF8MB4").
		AddRow("EMP", "ENAME", nil, "VARCHAR2", 10, 0, 0, "N", nil, nil, "B", "UTF8MB4")

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_tab_columns c")).
		WithArgs(schema).WillReturnRows(rows)

	columns, err := q.QueryColumns(db, schema)
	if err != nil {
		t.Fatalf("QueryColumns with NULL column_id: %v", err)
	}
	if len(columns) != 2 {
		t.Fatalf("columns = %+v, want 2 columns", columns)
	}
	if columns[0].OrdinalPosition != 1 || columns[1].OrdinalPosition != 2 {
		t.Errorf("ordinals = %d,%d, want 1,2 (NULL column_id falls back to scan order)",
			columns[0].OrdinalPosition, columns[1].OrdinalPosition)
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

// TestQuerySynonyms_OceanBase_TwoArgs guards Problem 1: the synonyms query
// filters on both owner and table_owner. The wire querier rewrites each :N to a
// separate "?" placeholder, so the query needs two bind arguments. Passing a
// single argument previously threw "not enough query arguments".
func TestQuerySynonyms_OceanBase_TwoArgs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SIT"
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_synonyms")).
		WithArgs(schema, schema).
		WillReturnRows(sqlmock.NewRows([]string{"synonym_name", "owner", "table_owner", "table_name", "is_public"}).
			AddRow("EMP_PUBLIC", "PUBLIC", "SCOTT", "EMP", "YES"))

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	synonyms, err := q.QuerySynonyms(db, schema)
	if err != nil {
		t.Fatalf("QuerySynonyms: %v", err)
	}
	if len(synonyms) != 1 || synonyms[0].SynonymName != "EMP_PUBLIC" {
		t.Fatalf("synonyms = %+v, want single EMP_PUBLIC", synonyms)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQuerySynonyms_NativeOracle_ReusesBind ensures native Oracle, which keeps
// ":"-style named binds, still issues the query with the same schema for both
// :1 and :2 (no regression).
func TestQuerySynonyms_NativeOracle_ReusesBind(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SCOTT"
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_synonyms")).
		WithArgs(schema, schema).
		WillReturnRows(sqlmock.NewRows([]string{"synonym_name", "owner", "table_owner", "table_name", "is_public"}).
			AddRow("EMP_ALIAS", "SCOTT", "SCOTT", "EMP", "NO"))

	q := OracleMetadataQuerier{}
	synonyms, err := q.QuerySynonyms(db, schema)
	if err != nil {
		t.Fatalf("QuerySynonyms: %v", err)
	}
	if len(synonyms) != 1 || synonyms[0].SynonymName != "EMP_ALIAS" {
		t.Fatalf("synonyms = %+v, want single EMP_ALIAS", synonyms)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// stubVersionBanner mocks the v$version banner query that QueryMViews issues on
// OceanBase to gate materialized-view support.
func stubVersionBanner(mock sqlmock.Sqlmock, banner string) {
	mock.ExpectQuery(regexp.QuoteMeta("FROM v$version")).
		WillReturnRows(sqlmock.NewRows([]string{"banner"}).AddRow(banner))
}

// TestQueryMViews_OceanBase_MissingView guards Problem 2: a modern OceanBase
// tenant (>= 4.3.3) that lacks ALL_MVIEWS raises ORA-00942, which must be
// degraded to an empty result instead of aborting the whole metadata
// extraction.
func TestQueryMViews_OceanBase_MissingView(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "CBSPARAM"
	stubVersionBanner(mock, "OceanBase 4.3.3.0 (r100000192024040922) (Built Apr 9 2024)")
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_mviews mv")).
		WithArgs(schema).
		WillReturnError(errors.New("error 942 (42S02): ORA-00942: table or view 'CBSPARAM.ALL_MVIEWS' does not exist"))

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	mviews, err := q.QueryMViews(db, schema)
	if err != nil {
		t.Fatalf("QueryMViews: expected degraded empty result, got error %v", err)
	}
	if len(mviews) != 0 {
		t.Fatalf("mviews = %+v, want empty (missing dictionary view)", mviews)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryMViews_OceanBase_OldVersion_Skips guards the version gate: an OceanBase
// tenant older than 4.3.3 returns nil without ever querying ALL_MVIEWS.
func TestQueryMViews_OceanBase_OldVersion_Skips(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "CBSPARAM"
	stubVersionBanner(mock, "OceanBase 3.2.4.0 (r1000001920230101) (Built Jan 1 2023)")

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	mviews, err := q.QueryMViews(db, schema)
	if err != nil {
		t.Fatalf("QueryMViews: expected skip on old version, got error %v", err)
	}
	if len(mviews) != 0 {
		t.Fatalf("mviews = %+v, want empty on old version", mviews)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (all_mviews should never be queried): %v", err)
	}
}

// TestQueryMViews_OceanBase_ReadsViews ensures a modern OceanBase tenant (>= 4.3.3)
// with ALL_MVIEWS present still reads materialized views.
func TestQueryMViews_OceanBase_ReadsViews(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SIT"
	stubVersionBanner(mock, "OceanBase 4.4.2.0 (r1000001920241107) (Built Nov 7 2024)")
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_mviews mv")).
		WithArgs(schema).
		WillReturnRows(sqlmock.NewRows([]string{"mview_name", "query", "refresh_method", "refresh_mode", "build_mode", "comments"}).
			AddRow("MV_EMP", "SELECT * FROM emp", "COMPLETE", "DEMAND", "IMMEDIATE", "monthly rollup"))

	q := OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}}
	mviews, err := q.QueryMViews(db, schema)
	if err != nil {
		t.Fatalf("QueryMViews: %v", err)
	}
	if len(mviews) != 1 || mviews[0].MViewName != "MV_EMP" {
		t.Fatalf("mviews = %+v, want single MV_EMP", mviews)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestQueryMViews_NativeOracle_ErrPropagates ensures native Oracle still
// propagates real errors instead of degrading them.
func TestQueryMViews_NativeOracle_ErrPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const schema = "SCOTT"
	mock.ExpectQuery(regexp.QuoteMeta("FROM all_mviews mv")).
		WithArgs(schema).
		WillReturnError(errors.New("connection refused"))

	q := OracleMetadataQuerier{}
	if _, err := q.QueryMViews(db, schema); err == nil {
		t.Fatal("QueryMViews: expected error to propagate for native Oracle")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
