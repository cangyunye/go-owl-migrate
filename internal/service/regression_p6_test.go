package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── P6 回归固化：首轮复现用例固化为测试 ──
// 用例对应 docs/plans/2026-09-02-metadata-object-redesign.md §1 的 R2/R4/R5/R1。

// ddlOwnerFixture：跨 owner 的"表 + schema 级对象"完整模型，用于断言
// 单表 DDL 不会带出无关 owner 的独立对象（R2 回归）。
func ddlOwnerFixture(t *testing.T) *md.SchemaModel {
	t.Helper()
	sm := md.NewSchemaModel()
	add := func(schema, name string, cols [][2]string) {
		tbl, err := md.NewTableDef(schema, name)
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range cols {
			col, err := md.NewColumnDef(schema, name, c[0], i+1, c[1])
			if err != nil {
				t.Fatal(err)
			}
			tbl.AddColumn(col)
		}
		if err := sm.AddTable(tbl); err != nil {
			t.Fatal(err)
		}
	}
	add("SCOTT", "EMP", [][2]string{{"EMPNO", "NUMBER"}})
	add("HR", "EMP", [][2]string{{"ID", "NUMBER"}})
	sm.AddView(&md.ViewDef{ViewSchema: "SCOTT", ViewName: "V_EMP", ViewDefinition: "SELECT 1"})
	sm.AddView(&md.ViewDef{ViewSchema: "HR", ViewName: "V_HR", ViewDefinition: "SELECT 1"})
	sm.AddMView(&md.MViewDef{MViewSchema: "SCOTT", MViewName: "MV_EMP", MViewQuery: "SELECT 1"})
	sm.AddMView(&md.MViewDef{MViewSchema: "HR", MViewName: "MV_HR", MViewQuery: "SELECT 1"})
	sm.AddSequence(&md.SequenceDef{SequenceSchema: "SCOTT", SequenceName: "SEQ_EMP", StartValue: 1, IncrementBy: 1, MaxValue: 999, CacheSize: 10})
	sm.AddSequence(&md.SequenceDef{SequenceSchema: "HR", SequenceName: "SEQ_HR", StartValue: 1, IncrementBy: 1, MaxValue: 999, CacheSize: 10})
	return sm
}

// R2：单表 DDL 不含无关对象——include 只选中 SCOTT.EMP 时，只输出
// SCOTT 表与其 owner 的序列/视图/物化视图；HR 的任何对象都不出现。
func TestGenerateDDLSingleTableNoUnrelatedObjects(t *testing.T) {
	sm := ddlOwnerFixture(t)
	dir := t.TempDir()
	files, err := GenerateDDL(sm, ddlCfg("oracle"), []string{"SCOTT.EMP"}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"scott.emp.table.sql":        true,
		"scott.seq_emp.sequence.sql": true,
		"scott.v_emp.view.sql":       true,
		"scott.mv_emp.mview.sql":     true,
	}
	got := fileNames(files)
	if len(got) != len(want) {
		t.Fatalf("files = %v, want exactly %d files", got, len(want))
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected file %s (have %v)", n, got)
		}
		if strings.HasPrefix(n, "hr.") {
			t.Errorf("single-table DDL leaked other owner's object: %s", n)
		}
	}
	content := readAll(t, files)
	for _, forbidden := range []string{"V_HR", "MV_HR", "SEQ_HR"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("single-table DDL must not contain HR object %s:\n%s", forbidden, content)
		}
	}
}

// R4：同一 owner（SCOTT）在表级与 schema 级 DDL 中引号+大小写渲染一致，
// 无 "scott"/"SCOTT" 混用（默认保真引号，ADR-001）。
func TestGenerateDDLOwnerCaseConsistent(t *testing.T) {
	sm := ddlFixture(t) // SCOTT.EMP + SCOTT.SEQ_EMP（表级与 schema 级同现）
	dir := t.TempDir()
	files, err := GenerateDDL(sm, ddlCfg("oracle"), []string{"SCOTT.EMP"}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	content := readAll(t, files)
	if !strings.Contains(content, `"SCOTT"."EMP"`) {
		t.Errorf("CREATE TABLE should quote-preserve \"SCOTT\".\"EMP\":\n%s", content)
	}
	if !strings.Contains(content, `"SCOTT"."SEQ_EMP"`) {
		t.Errorf("CREATE SEQUENCE should quote-preserve \"SCOTT\".\"SEQ_EMP\":\n%s", content)
	}
	if strings.Contains(content, `"scott"`) || strings.Contains(content, `scott."`) {
		t.Errorf("owner case must not flip to lowercase:\n%s", content)
	}
}

// R5/R1（引用一致性）：同一模型下 SELECT 与 DDL 对标识符的渲染一致——
// 默认路径均 quote 保真（"SCOTT"."EMP"），no-quote 路径均输出裸名。
func TestGenerateSelectAndDDLQuoteParity(t *testing.T) {
	sm := ddlFixture(t)
	cfg := ddlCfg("oracle")
	tbl := "SCOTT.EMP"

	// 默认：两路输出同一限定标识符形态。
	ddlDir := t.TempDir()
	ddlFiles, err := GenerateDDL(sm, cfg, []string{tbl}, nil, ddlDir)
	if err != nil {
		t.Fatal(err)
	}
	selDir := t.TempDir()
	selFiles, err := GenerateSelect(sm, cfg, []string{tbl}, "", 0, nil, selDir)
	if err != nil {
		t.Fatal(err)
	}
	ddlText, selText := readAll(t, ddlFiles), readAll(t, selFiles)
	if !strings.Contains(selText, `FROM "SCOTT"."EMP"`) {
		t.Errorf("SELECT should quote-preserve FROM \"SCOTT\".\"EMP\":\n%s", selText)
	}
	if !strings.Contains(ddlText, `"SCOTT"."EMP"`) {
		t.Errorf("DDL should quote-preserve \"SCOTT\".\"EMP\":\n%s", ddlText)
	}
	for name, text := range map[string]string{"select": selText, "ddl": ddlText} {
		if strings.Contains(text, `"scott"`) {
			t.Errorf("%s output mixes lowercase owner: %s", name, text)
		}
	}

	// no-quote：两路均裸名、无引号。
	ddlDir2 := t.TempDir()
	ddlFiles2, err := GenerateDDL(sm, cfg, []string{tbl}, boolPtr(true), ddlDir2)
	if err != nil {
		t.Fatal(err)
	}
	selDir2 := t.TempDir()
	selFiles2, err := GenerateSelect(sm, cfg, []string{tbl}, "", 0, boolPtr(true), selDir2)
	if err != nil {
		t.Fatal(err)
	}
	ddlText2, selText2 := readAll(t, ddlFiles2), readAll(t, selFiles2)
	if !strings.Contains(selText2, "FROM SCOTT.EMP") || !strings.Contains(ddlText2, "SCOTT.EMP") {
		t.Errorf("no-quote parity broken:\nSELECT:\n%s\nDDL:\n%s", selText2, ddlText2)
	}
	if strings.Contains(selText2, `"`) || strings.Contains(ddlText2, `"`) {
		t.Errorf("no_quote_identifiers should omit quotes:\nSELECT:\n%s\nDDL:\n%s", selText2, ddlText2)
	}
}

// ── csv-format 一致性（P6）：导出列 == docs/csv-format.md 列、loader 可读回 ──

// specColumnsFromDoc 解析 docs/csv-format.md：每个 "### <stem>.csv" 小节下
// 紧邻的 markdown 表中第一列的 `TOKEN` 即为该文件的规范表头（顺序保持）。
// 首个匹配小节为准（Column Reference 区在 Example 区之前）。
func specColumnsFromDoc(t *testing.T, doc string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	heading := func(line string) (string, bool) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "### ") {
			return "", false
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "### "))
		if stem, ok := strings.CutSuffix(rest, ".csv"); ok && stem != "" {
			return stem, true
		}
		return "", false
	}
	lines := strings.Split(doc, "\n")
	for i := 0; i < len(lines); i++ {
		stem, ok := heading(lines[i])
		if !ok {
			continue
		}
		if _, seen := out[stem]; seen {
			continue // 只取首次出现（列参考区）
		}
		j := i + 1
		var cols []string
		for ; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				continue
			}
			if !strings.HasPrefix(trimmed, "|") {
				break
			}
			cells := strings.Split(trimmed, "|")
			if len(cells) >= 2 {
				first := strings.TrimSpace(cells[1])
				if start, end := strings.Index(first, "`"), strings.LastIndex(first, "`"); start >= 0 && end > start {
					cols = append(cols, first[start+1:end])
				}
			}
		}
		if len(cols) > 0 {
			out[stem] = cols
		}
	}
	if len(out) == 0 {
		t.Fatal("no ### <stem>.csv spec tables parsed from docs/csv-format.md")
	}
	return out
}

// TestCSVSpecMatchesDoc：writer 实际表头 == 文档规范列，双向无漂移。
func TestCSVSpecMatchesDoc(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "csv-format.md"))
	if err != nil {
		t.Fatalf("read docs/csv-format.md: %v", err)
	}
	// 触发一次导出以注册 exportedColumnSets（writer 单一事实源）。
	if _, err := ExportMetadataFiles(t.TempDir(), exportFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	doc := specColumnsFromDoc(t, string(b))
	for stem, writerCols := range exportedColumnSets {
		docCols, ok := doc[stem]
		if !ok {
			t.Errorf("docs/csv-format.md missing spec table for %s.csv", stem)
			continue
		}
		if !reflect.DeepEqual(docCols, writerCols) {
			t.Errorf("%s.csv header drift: doc=%v writer=%v", stem, docCols, writerCols)
		}
	}
	for stem := range doc {
		if _, ok := exportedColumnSets[stem]; !ok {
			t.Errorf("docs/csv-format.md documents %s.csv which the exporter does not produce", stem)
		}
	}
}
