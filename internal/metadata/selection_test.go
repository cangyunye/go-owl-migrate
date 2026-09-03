package metadata

import (
	"strings"
	"testing"
)

// ── Slice 1 (tracer): ObjectType 枚举 / 解析 / 附随分类 ──

func TestParseObjectTypes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []ObjectType
		wantErr bool
	}{
		{"empty is empty set", "", nil, false},
		{"single table", "tables", []ObjectType{"tables"}, false},
		{"case insensitive", "Tables,COLUMNS", []ObjectType{"tables", "columns"}, false},
		{"dedup", "views,views", []ObjectType{"views"}, false},
		{"all 13 file stems", "tables,columns,primary_keys,indexes,foreign_keys,views,mviews,sequences,synonyms,triggers,functions,packages,package_bodies", []ObjectType{"tables", "columns", "primary_keys", "indexes", "foreign_keys", "views", "mviews", "sequences", "synonyms", "triggers", "functions", "packages", "package_bodies"}, false},
		{"unknown errors", "tables,foo", nil, true},
		{"blank entries skipped", "tables, ,views", []ObjectType{"tables", "views"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := ParseObjectTypes(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseObjectTypes(%q): want error, got %v", tt.in, set)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseObjectTypes(%q): %v", tt.in, err)
			}
			if len(set) != len(tt.want) {
				t.Fatalf("ParseObjectTypes(%q) = %v, want %v", tt.in, set, tt.want)
			}
			for _, w := range tt.want {
				if !set.Contains(w) {
					t.Errorf("ParseObjectTypes(%q): missing %q in %v", tt.in, w, set)
				}
			}
		})
	}
}

func TestObjectTypeAttachment(t *testing.T) {
	attached := []string{"columns", "primary_keys", "indexes", "foreign_keys", "triggers"}
	for _, name := range attached {
		if !IsAttached(ObjectType(name)) {
			t.Errorf("%s should be attached to its table", name)
		}
	}
	standalone := []string{"views", "mviews", "sequences", "synonyms", "functions", "packages", "package_bodies"}
	for _, name := range standalone {
		if IsAttached(ObjectType(name)) {
			t.Errorf("%s should be standalone, not attached", name)
		}
	}
	if IsAttached("tables") {
		t.Error("tables itself is not an attached type")
	}
	// The full enum is exactly: table + attached + standalone, no drift.
	if got := len(AllObjectTypes()); got != 13 {
		t.Errorf("AllObjectTypes() = %d entries, want 13", got)
	}
	var names []string
	for _, o := range AllObjectTypes() {
		names = append(names, string(o))
	}
	if strings.Join(names, ",") != "tables,columns,primary_keys,indexes,foreign_keys,views,mviews,sequences,synonyms,triggers,functions,packages,package_bodies" {
		t.Errorf("unexpected AllObjectTypes order: %v", names)
	}
}

// ── Slice 2: scope 语法解析 ──

func TestParseScope(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []SchemaPattern
		wantErr bool
	}{
		{"empty = all schemas/all tables", "", []SchemaPattern{{}}, false},
		{"all", "all", []SchemaPattern{{}}, false},
		{"legacy schema", "schema:SCOTT", []SchemaPattern{{Schema: "SCOTT"}}, false},
		{"legacy table", "table:EMP", []SchemaPattern{{TablePattern: "EMP"}}, false},
		{"legacy multi-table", "table:EMP,DEPT", []SchemaPattern{{TablePattern: "EMP"}, {TablePattern: "DEPT"}}, false},
		{"schema+table grammar", "schema:SCOTT:table:EMP", []SchemaPattern{{Schema: "SCOTT", TablePattern: "EMP"}}, false},
		{"multi schema single table", "schema:SCOTT,HR:table:EMP", []SchemaPattern{{Schema: "SCOTT", TablePattern: "EMP"}, {Schema: "HR", TablePattern: "EMP"}}, false},
		{"glob table wildcard", "schema:SCOTT:table:T_*", []SchemaPattern{{Schema: "SCOTT", TablePattern: "T_*"}}, false},
		{"case folded schema", "schema:scott:table:emp", []SchemaPattern{{Schema: "scott", TablePattern: "emp"}}, false},
		{"bare invalid errors", "scott.emp", nil, true},
		{"unknown prefix errors", "foo:EMP", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScope(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseScope(%q): want error, got %v", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseScope(%q): %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseScope(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("ParseScope(%q)[%d] = %+v, want %+v", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ── Slice 3: 表匹配语义（ADR-003：显式点名 > exclude > glob include）──

func TestSelectorMatches(t *testing.T) {
	ex := ExcludeFilter{
		Glob:    []string{"*_LOG"},
		Regex:   []string{`^TMP`},
		Schemas: []string{"HR"},
		Tables:  []string{"SCOTT.SECRET"},
	}
	tests := []struct {
		name string
		sel  ObjectSelector
		sch  string
		tbl  string
		want bool
	}{
		{"empty selector = all", ObjectSelector{}, "SCOTT", "EMP", true},
		{"schema-wide include", ObjectSelector{Schemas: []SchemaPattern{{Schema: "SCOTT"}}}, "SCOTT", "EMP", true},
		{"other schema excluded by schema scope", ObjectSelector{Schemas: []SchemaPattern{{Schema: "SCOTT"}}}, "HR", "EMP", false},
		{"exclude schema wins over schema-wide", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{Schema: "HR"}}}, "HR", "EMP", false},
		{"exclude glob wins over wildcard include", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{TablePattern: "T_*"}}}, "SCOTT", "T_LOG", false},
		{"exclude glob allows other matches", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{TablePattern: "T_*"}}}, "SCOTT", "T_USER", true},
		{"explicit bare name overrides exclude", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{TablePattern: "T_LOG"}}}, "SCOTT", "T_LOG", true},
		{"explicit schema.table overrides exclude", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{Schema: "SCOTT", TablePattern: "T_LOG"}}}, "SCOTT", "T_LOG", true},
		{"exclude exact table overrides glob all", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{TablePattern: "*"}}}, "SCOTT", "SECRET", false},
		{"explicit exact name beats exclude-exact", ObjectSelector{Exclude: ex, Schemas: []SchemaPattern{{TablePattern: "SCOTT.SECRET"}}}, "SCOTT", "SECRET", true},
		{"case folded glob", ObjectSelector{Schemas: []SchemaPattern{{Schema: "scott", TablePattern: "e*"}}}, "SCOTT", "EMP", true},
		{"case folded exclude regex", ObjectSelector{Exclude: ExcludeFilter{Regex: []string{`^tmp`}}, Schemas: []SchemaPattern{{TablePattern: "*"}}}, "SCOTT", "TMP_X", false},
		{"pattern EMP only matches EMP by name", ObjectSelector{Schemas: []SchemaPattern{{TablePattern: "EMP"}}}, "SCOTT", "DEPT", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sel.Matches(tt.sch, tt.tbl); got != tt.want {
				t.Errorf("Matches(%q,%q) = %v, want %v (sel=%+v)", tt.sch, tt.tbl, got, tt.want, tt.sel)
			}
		})
	}
}

// ── Slice 4: Select 范围语义（ADR-002）──

func selFixture(t *testing.T) *SchemaModel {
	t.Helper()
	sm := NewSchemaModel()
	addTbl := func(schema, name string) {
		tbl, err := NewTableDef(schema, name)
		if err != nil {
			t.Fatal(err)
		}
		col, _ := NewColumnDef(schema, name, "ID", 1, "NUMBER")
		tbl.AddColumn(col)
		if err := sm.AddTable(tbl); err != nil {
			t.Fatal(err)
		}
	}
	addTbl("SCOTT", "EMP")
	addTbl("SCOTT", "DEPT")
	addTbl("HR", "EMP") // 跨 owner 同名
	sm.AddView(&ViewDef{ViewSchema: "SCOTT", ViewName: "EMP_VIEW", ViewDefinition: "SELECT 1"})
	sm.AddSequence(&SequenceDef{SequenceSchema: "SCOTT", SequenceName: "SEQ_EMP"})
	sm.AddFunction(&FunctionDef{FunctionSchema: "SCOTT", FunctionName: "FN_X"})
	sm.AddView(&ViewDef{ViewSchema: "HR", ViewName: "HR_V", ViewDefinition: "SELECT 1"})
	return sm
}

func tableNames(sm *SchemaModel) []string {
	var out []string
	for _, t := range sm.GetTables() {
		out = append(out, t.TableSchema+"."+t.TableName)
	}
	return out
}

func TestSelectTableScope(t *testing.T) {
	sm := selFixture(t)
	tests := []struct {
		name     string
		scope    string
		wantTbl  []string
		wantView int // 独立对象仅整 schema 时进
	}{
		{"table EMP across owners", "table:EMP", []string{"HR.EMP", "SCOTT.EMP"}, 0},
		{"single owner table", "schema:SCOTT:table:EMP", []string{"SCOTT.EMP"}, 0},
		{"whole schema", "schema:SCOTT", []string{"SCOTT.DEPT", "SCOTT.EMP"}, 1},
		{"whole schema folded", "schema:scott", []string{"SCOTT.DEPT", "SCOTT.EMP"}, 1},
		{"glob pattern no standalone", "schema:SCOTT:table:E*", []string{"SCOTT.EMP"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, err := ParseScope(tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			got, err := (ObjectSelector{Schemas: patterns}).Select(sm)
			if err != nil {
				t.Fatalf("Select(%q): %v", tt.scope, err)
			}
			names := tableNames(got)
			if len(names) != len(tt.wantTbl) {
				t.Fatalf("Select(%q) tables = %v, want %v", tt.scope, names, tt.wantTbl)
			}
			for i := range names {
				if names[i] != tt.wantTbl[i] {
					t.Errorf("Select(%q) tables[%d] = %s, want %s", tt.scope, i, names[i], tt.wantTbl[i])
				}
			}
			if n := len(got.Views); n != tt.wantView {
				t.Errorf("Select(%q) views = %d, want %d (%v)", tt.scope, n, tt.wantView, viewNames(got.Views))
			}
		})
	}
	// original model unchanged (view semantics)
	if len(sm.Views) != 2 {
		t.Errorf("original model mutated: views = %d", len(sm.Views))
	}
}

func viewNames(vs []*ViewDef) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ViewName)
	}
	return out
}

func TestSelectZeroHitErrors(t *testing.T) {
	sm := selFixture(t)
	patterns, err := ParseScope("schema:SCOTT:table:EMPX")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ObjectSelector{Schemas: patterns}).Select(sm); err == nil {
		t.Fatal("Select with no match should error")
	}
	// unknown schema yields an error mentioning available schemas
	patterns2, _ := ParseScope("schema:NOPE")
	if _, err := (ObjectSelector{Schemas: patterns2}).Select(sm); err == nil {
		t.Fatal("Select with unknown schema should error")
	}
}

func TestSelectExcludeApplies(t *testing.T) {
	sm := selFixture(t)
	patterns, _ := ParseScope("schema:SCOTT")
	sel := ObjectSelector{Schemas: patterns, Exclude: ExcludeFilter{Tables: []string{"SCOTT.DEPT"}}}
	got, err := sel.Select(sm)
	if err != nil {
		t.Fatal(err)
	}
	names := tableNames(got)
	if len(names) != 1 || names[0] != "SCOTT.EMP" {
		t.Fatalf("tables = %v, want [SCOTT.EMP]", names)
	}
}

// ── Slice 5: 零命中错误文案含相近名建议 ──

func TestSelectZeroHitSuggestion(t *testing.T) {
	sm := NewSchemaModel()
	for _, name := range []string{"EMPLOYEES", "DEPT"} {
		tbl, _ := NewTableDef("SCOTT", name)
		sm.AddTable(tbl)
	}
	patterns, _ := ParseScope("schema:SCOTT:table:EMPLOYEE")
	_, err := (ObjectSelector{Schemas: patterns}).Select(sm)
	if err == nil {
		t.Fatal("want error for no-match selection")
	}
	msg := err.Error()
	if !strings.Contains(msg, "EMPLOYEES") || !strings.Contains(msg, "SCOTT") {
		t.Errorf("error should suggest close name and schema, got: %s", msg)
	}
}

// ── Slice 6: Schemas() owner 聚合 ──

func TestSchemaModelSchemas(t *testing.T) {
	sm := selFixture(t)
	got := sm.Schemas()
	want := []string{"HR", "SCOTT"}
	if len(got) != len(want) {
		t.Fatalf("Schemas() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Schemas()[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// ── Slice 7: include 列表 → Selector（config/CLI/Web 收敛共用）──

func TestSelectorFromInclude(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		want    []SchemaPattern
	}{
		{"nil = all", nil, []SchemaPattern{{}}},
		{"empty = all", []string{}, []SchemaPattern{{}}},
		{"star = all", []string{"*"}, []SchemaPattern{{}}},
		{"bare name any schema", []string{"DEPT"}, []SchemaPattern{{TablePattern: "DEPT"}}},
		{"schema.table exact", []string{"SCOTT.DEPT"}, []SchemaPattern{{Schema: "SCOTT", TablePattern: "DEPT"}}},
		{"schema wildcard", []string{"SCOTT.*"}, []SchemaPattern{{Schema: "SCOTT", TablePattern: "*"}}},
		{"any-schema glob", []string{"*.EMP"}, []SchemaPattern{{TablePattern: "EMP"}}},
		{"glob bare", []string{"T_*"}, []SchemaPattern{{TablePattern: "T_*"}}},
		{"multiple", []string{"SCOTT.DEPT", "EMP"}, []SchemaPattern{{Schema: "SCOTT", TablePattern: "DEPT"}, {TablePattern: "EMP"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := SelectorFromInclude(tt.include)
			if len(sel.Schemas) != len(tt.want) {
				t.Fatalf("SelectorFromInclude(%v).Schemas = %v, want %v", tt.include, sel.Schemas, tt.want)
			}
			for i := range tt.want {
				if sel.Schemas[i] != tt.want[i] {
					t.Errorf("Schemas[%d] = %+v, want %+v", i, sel.Schemas[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilterTablesByInclude(t *testing.T) {
	sm := selFixture(t) // SCOTT.EMP/DEPT, HR.EMP
	tables := sm.GetTables()
	got := FilterTablesByInclude(tables, []string{"scott.emp"}) // 大小写不敏感
	if len(got) != 1 || got[0].TableName != "EMP" {
		t.Fatalf("case-insensitive exact include = %v", tableNamesOf(got))
	}
	if len(FilterTablesByInclude(tables, []string{"*"})) != 3 {
		t.Fatal("star include should keep all")
	}
	if len(FilterTablesByInclude(tables, nil)) != 3 {
		t.Fatal("nil include should keep all")
	}
	got = FilterTablesByInclude(tables, []string{"E*"})
	if len(got) != 2 { // SCOTT.EMP + HR.EMP
		t.Fatalf("glob E* = %v", tableNamesOf(got))
	}
	got = FilterTablesByInclude(tables, []string{"SCOTT.*"})
	if len(got) != 2 {
		t.Fatalf("SCOTT.* = %v", tableNamesOf(got))
	}
}

func tableNamesOf(ts []*TableDef) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.TableSchema+"."+t.TableName)
	}
	return out
}
