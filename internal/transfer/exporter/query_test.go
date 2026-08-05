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
			got := e.buildBatchQuery(tbl, tt.colNames, tt.quotedPKs, tt.pkCols, tt.useCursor, 0)
			if got != tt.want {
				t.Errorf("buildBatchQuery() mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestBuildBatchQuery_NoPKOffsetPagination(t *testing.T) {
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	colNames := []string{`"EMPNO"`, `"ENAME"`}

	tests := []struct {
		name   string
		dbType string
		offset int
		want   string
	}{
		{
			name:   "postgres offset uses limit offset",
			dbType: "postgres",
			offset: 200,
			want:   `SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP" LIMIT 100 OFFSET 200`,
		},
		{
			name:   "mysql offset uses limit offset",
			dbType: "mysql",
			offset: 200,
			want:   "SELECT `EMPNO`, `ENAME` FROM `SCOTT`.`EMP` LIMIT 100 OFFSET 200",
		},
		{
			name:   "oracle offset uses offset rows fetch next",
			dbType: "oracle",
			offset: 200,
			want:   `SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP" OFFSET 200 ROWS FETCH NEXT 100 ROWS ONLY`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestExporter(tt.dbType)
			var quotedCols []string
			if tt.dbType == "mysql" {
				quotedCols = []string{"`EMPNO`", "`ENAME`"}
			} else {
				quotedCols = colNames
			}
			got := e.buildBatchQuery(tbl, quotedCols, nil, nil, false, tt.offset)
			if got != tt.want {
				t.Errorf("buildBatchQuery() no-pk offset mismatch\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestBuildBatchQuery_OracleLegacyPagination(t *testing.T) {
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	newLegacy := func() *Exporter {
		e := newTestExporter("oracle")
		e.oracleLegacy = true
		return e
	}

	t.Run("no pk offset uses rownum wrap", func(t *testing.T) {
		e := newLegacy()
		got := e.buildBatchQuery(tbl, []string{`"EMPNO"`, `"ENAME"`}, nil, nil, false, 200)
		want := `SELECT "EMPNO", "ENAME" FROM (SELECT owl_pg__.*, ROWNUM AS owl_rn__ FROM (SELECT "EMPNO", "ENAME" FROM "SCOTT"."EMP") owl_pg__ WHERE ROWNUM <= 300) WHERE owl_rn__ > 200`
		if got != want {
			t.Errorf("no-pk legacy mismatch\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("composite pk cursor uses rownum limit", func(t *testing.T) {
		e := newLegacy()
		got := e.buildBatchQuery(tbl,
			[]string{`"EMPNO"`, `"DEPTNO"`},
			[]string{`"EMPNO"`, `"DEPTNO"`},
			[]string{"EMPNO", "DEPTNO"},
			true, 0)
		want := `SELECT "EMPNO", "DEPTNO" FROM (SELECT "EMPNO", "DEPTNO" FROM "SCOTT"."EMP" WHERE ("EMPNO", "DEPTNO") > (:1, :2) ORDER BY "EMPNO", "DEPTNO") WHERE ROWNUM <= 100`
		if got != want {
			t.Errorf("cursor legacy mismatch\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("modern oracle uses fetch next", func(t *testing.T) {
		e := newTestExporter("oracle")
		got := e.buildBatchQuery(tbl, []string{`"EMPNO"`}, []string{`"EMPNO"`}, []string{"EMPNO"}, false, 0)
		if !strings.Contains(got, "FETCH NEXT 100 ROWS ONLY") {
			t.Errorf("modern oracle should use FETCH NEXT, got: %s", got)
		}
	})
}

func TestBuildBatchQuery_CompositeCursorNotNaiveConjunction(t *testing.T) {
	e := newTestExporter("postgres")
	tbl := &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
	got := e.buildBatchQuery(tbl,
		[]string{`"A"`, `"B"`},
		[]string{`"A"`, `"B"`},
		[]string{"A", "B"},
		true,
		0,
	)
	if strings.Contains(got, `"A" > $1 AND "B" > $2`) {
		t.Errorf("composite cursor must not use naive conjunction (drops rows): %s", got)
	}
	if !strings.Contains(got, `("A", "B") > ($1, $2)`) {
		t.Errorf("composite cursor must use row-value comparison: %s", got)
	}
}
