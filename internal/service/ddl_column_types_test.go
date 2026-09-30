package service

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestColumnTypesOverrideWins(t *testing.T) {
	opts := dialect.BuildOptions{
		TargetDialect: "postgres",
		ColumnTypes: map[string]string{
			"OWL_SRC.EMP.SAL": "numeric(12,2)", // 列级
		},
	}
	tbl := &md.TableDef{
		TableSchema: "OWL_SRC", TableName: "EMP",
		Columns: []*md.ColumnDef{
			{ColumnName: "SAL", DataType: "NUMBER", DataPrecision: 8, DataScale: 2},
			{ColumnName: "BONUS", DataType: "NUMBER", DataPrecision: 4, DataScale: 1},
			{ColumnName: "ENAME", DataType: "VARCHAR2", DataLength: 50},
		},
	}
	out := QualifyTableTypes(tbl, opts)
	types := make([]string, 0, 3)
	for _, c := range out.Columns {
		types = append(types, c.ColumnName+"="+c.DataType)
	}
	joined := strings.Join(types, ",")
	// 列级：SAL 用 column_types 模板
	if !strings.Contains(joined, "SAL=numeric(12,2)") {
		t.Errorf("column override missing: %s", joined)
	}
	// 未覆盖列：BONUS 走普通限定（NUMBER 无长度修饰→原样）
	if !strings.Contains(joined, "BONUS=NUMBER") {
		t.Errorf("plain column broken: %s", joined)
	}
	// 无覆盖：普通限定
	if !strings.Contains(joined, "ENAME=VARCHAR2(50)") {
		t.Errorf("plain qualify broken: %s", joined)
	}
	// 查找大小写不敏感：小写键也能命中
	opts2 := dialect.BuildOptions{ColumnTypes: normalizeColumnTypes(map[string]string{"owl_src.emp.sal": "float8"})}
	out2 := QualifyTableTypes(tbl, opts2)
	if out2.Columns[0].DataType != "float8" {
		t.Errorf("case-insensitive lookup broken: %s", out2.Columns[0].DataType)
	}
}
