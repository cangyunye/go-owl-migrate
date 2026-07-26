package exporter

import (
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func newTestExporter(dbType string) *Exporter {
	return New(nil, Config{DBType: dbType, PageSize: 100})
}

func TestBuildBatchQuery(t *testing.T) {
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}

	tests := []struct {
		name      string
		dbType    string
		colNames  []string
		quotedPKs []string
		pkCols    []string
		useCursor bool
		want      string
	}{
		{
			name:     "postgres no pk uses limit only",
			dbType:   "postgres",
			colNames: []string{`"EMPNO"`, `"ENAME"`},
			want:     `SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP" LIMIT 100`,
		},
		{
			name:      "postgres single pk first page",
			dbType:    "postgres",
			colNames:  []string{`"EMPNO"`, `"ENAME"`},
			quotedPKs: []string{`"EMPNO"`},
			pkCols:    []string{"EMPNO"},
			useCursor: false,
			want:      `SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP" ORDER BY "EMPNO" LIMIT 100`,
		},
		{
			name:      "postgres single pk cursor",
			dbType:    "postgres",
			colNames:  []string{`"EMPNO"`, `"ENAME"`},
			quotedPKs: []string{`"EMPNO"`},
			pkCols:    []string{"EMPNO"},
			useCursor: true,
			want:      `SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP" WHERE "EMPNO" > $1 ORDER BY "EMPNO" LIMIT 100`,
		},
		{
			name:      "postgres composite pk cursor uses row value comparison",
			dbType:    "postgres",
			colNames:  []string{`"EMPNO"`, `"DEPTNO"`, `"ENAME"`},
			quotedPKs: []string{`"EMPNO"`, `"DEPTNO"`},
			pkCols:    []string{"EMPNO", "DEPTNO"},
			useCursor: true,
			want:      `SELECT "EMPNO", "DEPTNO", "ENAME" FROM "SCOTT"."EMP" WHERE ("EMPNO", "DEPTNO") > ($1, $2) ORDER BY "EMPNO", "DEPTNO" LIMIT 100`,
		},
		{
			name:      "mysql composite pk cursor uses backticks and question marks",
			dbType:    "mysql",
			colNames:  []string{"`EMPNO`", "`DEPTNO`"},
			quotedPKs: []string{"`EMPNO`", "`DEPTNO`"},
			pkCols:    []string{"EMPNO", "DEPTNO"},
			useCursor: true,
			want:      "SELECT `EMPNO`, `DEPTNO` FROM `SCOTT`.`EMP` WHERE (`EMPNO`, `DEPTNO`) > (?, ?) ORDER BY `EMPNO`, `DEPTNO` LIMIT 100",
		},
		{
			name:      "oracle composite pk cursor uses named placeholders and fetch next",
			dbType:    "oracle",
			colNames:  []string{`"EMPNO"`, `"DEPTNO"`},
			quotedPKs: []string{`"EMPNO"`, `"DEPTNO"`},
			pkCols:    []string{"EMPNO", "DEPTNO"},
			useCursor: true,
			want:      `SELECT "EMPNO", "DEPTNO" FROM "SCOTT"."EMP" WHERE ("EMPNO", "DEPTNO") > (:1, :2) ORDER BY "EMPNO", "DEPTNO" FETCH NEXT 100 ROWS ONLY`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestExporter(tt.dbType)
			got := e.buildBatchQuery(tbl, tt.colNames, tt.quotedPKs, tt.pkCols, tt.useCursor)
			if got != tt.want {
				t.Errorf("buildBatchQuery() mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestBuildBatchQuery_CompositeCursorNotNaiveConjunction(t *testing.T) {
	e := newTestExporter("postgres")
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	got := e.buildBatchQuery(tbl,
		[]string{`"A"`, `"B"`},
		[]string{`"A"`, `"B"`},
		[]string{"A", "B"},
		true,
	)
	if strings.Contains(got, `"A" > $1 AND "B" > $2`) {
		t.Errorf("composite cursor must not use naive conjunction (drops rows): %s", got)
	}
	if !strings.Contains(got, `("A", "B") > ($1, $2)`) {
		t.Errorf("composite cursor must use row-value comparison: %s", got)
	}
}
