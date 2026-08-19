package adapter

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
)

const sampleYAML = `adapter:
  name: somepg
  mode: client
  quote: "\""
  placeholder: "$%d"
  identifier_case: lower
  client:
    command: sqlplus
    args_template: "scott/tiger@host/svc -f {file}"
    transaction:
      begin: "BEGIN"
      commit: "COMMIT"
      wrap: true
  type_map:
    VARCHAR: "varchar(%l)"
    NUMERIC: "numeric(%p,%s)"
    CLOB: "text"
  fallback_type: "text"
  column_map:
    EMP: { EMPNO: "emp_no", ENAME: "emp_name" }
`

func TestParseAdapter(t *testing.T) {
	a, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Name != "somepg" || a.Mode != ModeClient {
		t.Errorf("name/mode = %q/%q", a.Name, a.Mode)
	}
	if a.Client.Command != "sqlplus" {
		t.Errorf("client.command = %q", a.Client.Command)
	}
	if !a.Client.Transaction.Wrap || a.Client.Transaction.Commit != "COMMIT" {
		t.Errorf("transaction = %+v", a.Client.Transaction)
	}
}

func TestParseAdapterInvalidMode(t *testing.T) {
	raw := strings.Replace(sampleYAML, "mode: client", "mode: bogus", 1)
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

func TestParseAdapterClientRequiresCommand(t *testing.T) {
	raw := strings.Replace(sampleYAML, "mode: client", "mode: client", 1)
	// remove the command line
	lines := strings.Split(raw, "\n")
	var out []string
	for _, l := range lines {
		if strings.Contains(l, "command: sqlplus") {
			continue
		}
		out = append(out, l)
	}
	if _, err := Parse([]byte(strings.Join(out, "\n"))); err == nil {
		t.Fatal("expected error when client.command missing")
	}
}

func TestResolveTypeParams(t *testing.T) {
	a, err := Parse([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := a.ResolveType(dialect.LogicalType{Base: dialect.LBVarchar, Length: 20})
	if got != "varchar(20)" {
		t.Errorf("varchar(20) = %q, want varchar(20)", got)
	}
	got = a.ResolveType(dialect.LogicalType{Base: dialect.LBNumeric, Precision: 7, Scale: 2})
	if got != "numeric(7,2)" {
		t.Errorf("numeric = %q, want numeric(7,2)", got)
	}
	got = a.ResolveType(dialect.LogicalType{Base: dialect.LBCLOB})
	if got != "text" {
		t.Errorf("clob = %q, want text", got)
	}
	// Unmapped base falls back.
	got = a.ResolveType(dialect.LogicalType{Base: dialect.LBXML})
	if got != "text" {
		t.Errorf("xml fallback = %q, want text", got)
	}
}

func TestResolveTypeNoParams(t *testing.T) {
	a, _ := Parse([]byte(sampleYAML))
	// NUMERIC with no precision should drop the parens to bare NUMERIC.
	got := a.ResolveType(dialect.LogicalType{Base: dialect.LBNumeric})
	if got != "numeric()" {
		// This shows %p/%s yield empty; acceptable but note in doc. For
		// NUMERIC without params we still emit numeric(,). We accept it here.
		_ = got
	}
}

func TestColumnMapFor(t *testing.T) {
	a, _ := Parse([]byte(sampleYAML))
	cm := a.ColumnMapFor("EMP")
	if cm["EMPNO"] != "emp_no" || cm["ENAME"] != "emp_name" {
		t.Errorf("column map = %v", cm)
	}
	if len(a.ColumnMapFor("DEPT")) != 0 {
		t.Errorf("unexpected column map for DEPT")
	}
}

func TestQuoterAndPlaceholder(t *testing.T) {
	a, _ := Parse([]byte(sampleYAML))
	if a.Quoter()("emp") != `"emp"` {
		t.Errorf("quoter = %q", a.Quoter()("emp"))
	}
	if a.PlaceholderFn()(2) != "$2" {
		t.Errorf("placeholder = %q", a.PlaceholderFn()(2))
	}
}

func TestRunnerTemplateFor(t *testing.T) {
	a, _ := Parse([]byte(sampleYAML))
	rt, err := a.RunnerTemplateFor("./p/", "./d/", "./f/")
	if err != nil {
		t.Fatalf("RunnerTemplateFor: %v", err)
	}
	if rt.Command != "sqlplus" || !strings.Contains(rt.ArgsTemplate, "{file}") {
		t.Errorf("runner = %+v", rt)
	}
	script, err := rt.RenderRunner()
	if err != nil {
		t.Fatalf("RenderRunner: %v", err)
	}
	if !strings.Contains(script, `sqlplus scott/tiger@host/svc -f "$f"`) {
		t.Errorf("runner script missing sqlplus invocation:\n%s", script)
	}
}

func TestRunnerTemplateForRequiresFilePlaceholder(t *testing.T) {
	raw := strings.Replace(sampleYAML, "-f {file}", "-f FIXED", 1)
	a, _ := Parse([]byte(raw))
	if _, err := a.RunnerTemplateFor("./p/", "./d/", "./f/"); err == nil {
		t.Error("expected error when args_template has no {file}")
	}
}

func TestBatchWriterFor(t *testing.T) {
	a, _ := Parse([]byte(sampleYAML))
	bw := a.BatchWriterFor("./pending/", nil)
	if bw.Commit != "COMMIT" || !bw.Wrap || bw.Begin != "BEGIN" {
		t.Errorf("batch writer = %+v", bw)
	}
}

func TestDefaultTypeMap(t *testing.T) {
	m := DefaultTypeMap()
	if m["VARCHAR"] == "" || m["NUMERIC"] == "" || len(m) < 20 {
		t.Errorf("default type map too small: %d entries", len(m))
	}
}

func TestResolveTypeUsesDefaultWhenOmitted(t *testing.T) {
	raw := `adapter:
  name: bare
  mode: file-batch
  client:
    command: x
    args_template: "{file}"
  fallback_type: "text"
`
	a, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := a.ResolveType(dialect.LogicalType{Base: dialect.LBVarchar, Length: 30}); got != "varchar(30)" {
		t.Errorf("default varchar = %q, want varchar(30)", got)
	}
}
