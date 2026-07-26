package cmd

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestIndexDropRecreate(t *testing.T) {
	newTbl := func() *md.TableDef {
		tbl, _ := md.NewTableDef("scott", "emp")
		tbl.AddIndex(&md.IndexDef{TableSchema: "scott", TableName: "emp", IndexName: "idx_emp", ColumnName: "ename"})
		return tbl
	}

	t.Run("postgres", func(t *testing.T) {
		drop, recreate := indexDropRecreate(newTbl(), "postgres", dialect.BuildOptions{})
		if len(drop) != 1 || !strings.Contains(drop[0], `DROP INDEX "scott"."idx_emp"`) {
			t.Errorf("postgres drop = %v", drop)
		}
		if len(recreate) != 1 || !strings.Contains(recreate[0], "CREATE INDEX") {
			t.Errorf("postgres recreate = %v", recreate)
		}
	})
	t.Run("mysql uses ON clause", func(t *testing.T) {
		drop, _ := indexDropRecreate(newTbl(), "mysql", dialect.BuildOptions{})
		if len(drop) != 1 || !strings.Contains(drop[0], " ON ") {
			t.Errorf("mysql drop = %v", drop)
		}
	})
	t.Run("schema mapping applied", func(t *testing.T) {
		drop, _ := indexDropRecreate(newTbl(), "postgres", dialect.BuildOptions{SchemaMapping: map[string]string{"scott": "public"}})
		if len(drop) != 1 || !strings.Contains(drop[0], `"public"."idx_emp"`) {
			t.Errorf("mapped drop = %v", drop)
		}
	})
}
