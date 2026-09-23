package serve

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// newRowCountServer builds a server with loaded metadata and an injected
// database handle, so the handler runs without a live source.
func newRowCountServer(t *testing.T, sourceType string, db *sql.DB) *Server {
	t.Helper()
	srv := newTestServer(t)
	t.Cleanup(func() { db.Close() })

	srv.openDB = func(config.DBConfig) (*sql.DB, error) { return db, nil }

	sm := md.NewSchemaModel()
	emp, err := md.NewTableDef("public", "emp")
	if err != nil {
		t.Fatal(err)
	}
	dept, err := md.NewTableDef("public", "dept")
	if err != nil {
		t.Fatal(err)
	}
	sm.AddTable(emp)
	sm.AddTable(dept)
	srv.schemaModel = sm
	srv.cfg = &config.Config{Source: config.DBConfig{Type: sourceType, DSN: "host=x dbname=y", Schema: "public"}}
	return srv
}

// rowCountRecords decodes the NDJSON stream into its records.
func rowCountRecords(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record %q is not JSON: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestHandler_RowCount_ExactCountsInRequestOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	srv := newRowCountServer(t, "panweidb-mysql", db)

	// B-mode PanWeiDB is read over the PostgreSQL wire, so identifiers are
	// double-quoted even though the dialect inherits MySQL DDL quoting.
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."emp"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(14))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."dept"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	w := doJSON(t, srv, "POST", "/api/v1/metadata/row-count",
		`{"tables":[{"schema":"public","name":"emp"},{"schema":"public","name":"dept"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}

	recs := rowCountRecords(t, w.Body.String())
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3 (two tables + summary): %s", len(recs), w.Body.String())
	}
	if recs[0]["name"] != "emp" || recs[0]["rows"] != float64(14) {
		t.Errorf("first record = %v, want emp/14", recs[0])
	}
	if recs[1]["name"] != "dept" || recs[1]["rows"] != float64(4) {
		t.Errorf("second record = %v, want dept/4 (request order)", recs[1])
	}
	if recs[2]["done"] != true || recs[2]["counted"] != float64(2) || recs[2]["failed"] != float64(0) {
		t.Errorf("summary = %v, want done/counted 2/failed 0", recs[2])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestHandler_RowCount_QuotesMySQLWireSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	srv := newRowCountServer(t, "mysql", db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `public`\\.`emp`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	w := doJSON(t, srv, "POST", "/api/v1/metadata/row-count", `{"tables":[{"schema":"public","name":"emp"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestHandler_RowCount_RejectsUnknownTable pins the injection guard: only
// tables from the loaded metadata are counted, and the SQL never carries a
// client-supplied identifier.
func TestHandler_RowCount_RejectsUnknownTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	srv := newRowCountServer(t, "postgres", db)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."emp"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	w := doJSON(t, srv, "POST", "/api/v1/metadata/row-count",
		`{"tables":[{"schema":"public","name":"emp"},{"schema":"public","name":"emp; DROP TABLE x"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	recs := rowCountRecords(t, w.Body.String())
	if errMsg, _ := recs[1]["error"].(string); !strings.Contains(errMsg, "元数据") {
		t.Errorf("unknown table record = %v, want an error record", recs[1])
	}
	if recs[2]["failed"] != float64(1) {
		t.Errorf("summary = %v, want failed 1", recs[2])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (a crafted identifier reached SQL?): %v", err)
	}
}

// TestHandler_RowCount_TableErrorDoesNotAbortStream keeps one unwritable table
// from hiding the rest of the run.
func TestHandler_RowCount_TableErrorDoesNotAbortStream(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	srv := newRowCountServer(t, "postgres", db)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."emp"`).
		WillReturnError(errors.New(`pq: permission denied for table emp`))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."dept"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	w := doJSON(t, srv, "POST", "/api/v1/metadata/row-count",
		`{"tables":[{"schema":"public","name":"emp"},{"schema":"public","name":"dept"}]}`)
	recs := rowCountRecords(t, w.Body.String())
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3: %s", len(recs), w.Body.String())
	}
	if recs[0]["error"] == nil {
		t.Errorf("emp record = %v, want an error", recs[0])
	}
	if recs[1]["rows"] != float64(4) {
		t.Errorf("dept record = %v, want rows 4 after the failed table", recs[1])
	}
	if recs[2]["counted"] != float64(1) || recs[2]["failed"] != float64(1) {
		t.Errorf("summary = %v, want counted 1 / failed 1", recs[2])
	}
}

func TestHandler_RowCount_RequiresMetadataAndSource(t *testing.T) {
	srv := newTestServer(t)

	// No metadata loaded yet.
	w := doJSON(t, srv, "POST", "/api/v1/metadata/row-count", `{"tables":[{"schema":"public","name":"emp"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("no metadata: status = %d, want 400", w.Code)
	}

	// Metadata present but no configured source.
	sm := md.NewSchemaModel()
	emp, _ := md.NewTableDef("public", "emp")
	sm.AddTable(emp)
	srv.schemaModel = sm
	srv.cfg = &config.Config{}
	w = doJSON(t, srv, "POST", "/api/v1/metadata/row-count", `{"tables":[{"schema":"public","name":"emp"}]}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "source") {
		t.Errorf("no source: status = %d body = %s, want 400 mentioning source", w.Code, w.Body.String())
	}

	// Empty table list.
	w = doJSON(t, srv, "POST", "/api/v1/metadata/row-count", `{"tables":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty list: status = %d, want 400", w.Code)
	}
}

func TestQuoteSourceIdent(t *testing.T) {
	tests := []struct {
		dbType, in, want string
	}{
		{"postgres", "emp", `"emp"`},
		{"opengaussdb", "emp", `"emp"`},
		{"panweidb-mysql", "emp", `"emp"`},
		{"opengaussdb-oracle", "EMP", `"EMP"`},
		{"mysql", "emp", "`emp`"},
		{"goldendb-mysql", "emp", "`emp`"},
		{"oceanbase-mysql", "emp", "`emp`"},
		{"postgres", `we"ird`, `"we""ird"`},
		{"mysql", "we`ird", "`we``ird`"},
	}
	for _, tt := range tests {
		if got := quoteSourceIdent(tt.dbType, tt.in); got != tt.want {
			t.Errorf("quoteSourceIdent(%q, %q) = %s, want %s", tt.dbType, tt.in, got, tt.want)
		}
	}
}

// flushWatcher records the write/flush order so the streaming contract can be
// asserted deterministically (sqlite counts are far too fast to time).
type flushWatcher struct {
	header http.Header
	buf    bytes.Buffer
	events []string
	status int
}

func (f *flushWatcher) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *flushWatcher) WriteHeader(code int) { f.status = code }
func (f *flushWatcher) Write(p []byte) (int, error) {
	f.events = append(f.events, "write:"+strings.TrimSpace(string(p)))
	return f.buf.Write(p)
}
func (f *flushWatcher) Flush() { f.events = append(f.events, "flush") }

// TestHandler_RowCount_FlushesEachRecord pins the incremental refresh: every
// table's line is flushed before the next count starts, so the picker updates
// top-to-bottom instead of waiting for the whole run.
func TestHandler_RowCount_FlushesEachRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	srv := newRowCountServer(t, "postgres", db)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."emp"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(14))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "public"\."dept"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	w := &flushWatcher{}
	req := httptest.NewRequest("POST", "/api/v1/metadata/row-count",
		strings.NewReader(`{"tables":[{"schema":"public","name":"emp"},{"schema":"public","name":"dept"}]}`))
	srv.handleRowCount(w, req)

	empIdx, deptIdx, firstFlush := -1, -1, -1
	for i, ev := range w.events {
		switch {
		case strings.HasPrefix(ev, "write:") && strings.Contains(ev, `"emp"`):
			empIdx = i
		case strings.HasPrefix(ev, "write:") && strings.Contains(ev, `"dept"`):
			deptIdx = i
		case ev == "flush" && firstFlush < 0:
			firstFlush = i
		}
	}
	if empIdx < 0 || deptIdx < 0 {
		t.Fatalf("records not both written: %v", w.events)
	}
	if firstFlush < 0 {
		t.Fatal("handler never flushed; the stream would arrive in one batch")
	}
	if !(empIdx < firstFlush && firstFlush < deptIdx) {
		t.Errorf("flush order wrong: want emp(%d) < flush(%d) < dept(%d); events=%v", empIdx, firstFlush, deptIdx, w.events)
	}
	if flushes := strings.Count(strings.Join(w.events, ","), "flush"); flushes < 3 {
		t.Errorf("flush count = %d, want one per record plus the summary", flushes)
	}
}
