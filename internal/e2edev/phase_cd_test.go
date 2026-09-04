//go:build e2e

package e2edev

import (
	"fmt"
	"strings"
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

// TestPhaseC_SourceExtraction：mysql / ob-mysql / pg 源侧全字典族抽取探针。
// PG 从本机不可达时该子测试 skip（代码就绪，可在可达环境运行）。
func TestPhaseC_SourceExtraction(t *testing.T) {
	e := loadEnv(t)

	t.Run("mysql_src", func(t *testing.T) {
		skipNoEnv(t, e.MysqlDSN, "OWL_E2E_MYSQL_DSN")
		db := BootstrapMySQLSource(t, e)
		sm, err := extractor.Extract(db, "mysql", srcMySQLDB)
		if err != nil {
			t.Fatalf("extract mysql %s: %v", srcMySQLDB, err)
		}
		assertMySQLShape(t, sm, srcMySQLDB)
	})

	t.Run("obmysql_src", func(t *testing.T) {
		skipNoEnv(t, e.OBMysqlDSN, "OWL_E2E_OB_MYSQL_DSN")
		db := BootstrapOBMySQLSource(t, e)
		sm, err := extractor.Extract(db, "oceanbase-mysql", srcOBMDB)
		if err != nil {
			t.Fatalf("extract oceanbase-mysql %s: %v", srcOBMDB, err)
		}
		assertMySQLShape(t, sm, srcOBMDB)
		t.Logf("capabilities(oceanbase-mysql) = %v", extractor.Capabilities("oceanbase-mysql"))
	})

	t.Run("pg_multiowner_src", func(t *testing.T) {
		skipNoEnv(t, e.PgDSN, "OWL_E2E_PG_DSN")
		db := BootstrapPGMultiOwner(t, e)
		for _, sch := range []string{pgSchHr, pgSchFn} {
			sm, err := extractor.Extract(db, "postgres", sch)
			if err != nil {
				t.Fatalf("extract pg %s: %v", sch, err)
			}
			if len(sm.GetTables()) == 0 {
				t.Errorf("pg schema %s has no tables", sch)
				continue
			}
			for _, tbl := range sm.GetTables() {
				t.Logf("pg %s.%s cols=%d owner-schema", tbl.TableSchema, tbl.TableName, len(tbl.GetColumns()))
			}
		}
	})
}

// assertMySQLShape 断言 mysql/ob-mysql 种子抽取形状与关键类型字段。
func assertMySQLShape(t *testing.T, sm *md.SchemaModel, dbName string) {
	t.Helper()
	tables := sm.GetTables()
	if len(tables) != 2 {
		t.Errorf("%s tables = %d, want 2 (dept/emp)", dbName, len(tables))
	}
	var pk, fk, ix int
	for _, tbl := range tables {
		if tbl.TableSchema != dbName {
			t.Errorf("table schema = %q, want %q", tbl.TableSchema, dbName)
		}
		pk += len(tbl.GetPrimaryKeys())
		fk += len(tbl.GetForeignKeys())
		ix += len(tbl.GetIndexes())
		for _, c := range tbl.GetColumns() {
			if c.ColumnName == "" || c.DataType == "" {
				t.Errorf("%s.%s column missing name/type", tbl.TableName, c.ColumnName)
			}
			if c.DataType == "decimal" && c.DataPrecision != 9 {
				t.Errorf("sal decimal precision = %d, want 9", c.DataPrecision)
			}
		}
	}
	if pk < 2 || fk < 1 || ix < 1 {
		t.Errorf("%s pk=%d fk=%d ix=%d, want pk>=2 fk>=1 ix>=1", dbName, pk, fk, ix)
	}
	if len(sm.GetViews()) < 1 {
		t.Errorf("%s views = %d, want >= 1", dbName, len(sm.GetViews()))
	}
	if len(sm.GetFunctions(dbName)) < 1 {
		t.Errorf("%s functions = %d, want >= 1", dbName, len(sm.GetFunctions(dbName)))
	}
	t.Logf("%s: tables=%d pk=%d fk=%d ix=%d views=%d funcs=%d",
		dbName, len(tables), pk, fk, ix, len(sm.GetViews()), len(sm.GetFunctions(dbName)))
}

// TestPhaseD_OBMySQLSequenceProbe：探查 OB-MySQL 租户（4.4）的 SEQUENCE
// 字典查询与 information_schema 暴露情况（HANDOFF §三.2 P6-2）。只探不实现。
func TestPhaseD_OBMySQLSequenceProbe(t *testing.T) {
	e := loadEnv(t)
	skipNoEnv(t, e.OBMysqlDSN, "OWL_E2E_OB_MYSQL_DSN")
	db := BootstrapOBMySQLSource(t, e)

	// 确认租户支持 CREATE SEQUENCE
	if _, err := db.Exec(`DROP SEQUENCE IF EXISTS seq_probe`); err != nil {
		t.Logf("DROP SEQUENCE unsupported: %v", err)
	}
	if _, err := db.Exec(`CREATE SEQUENCE seq_probe START WITH 100 INCREMENT BY 1`); err != nil {
		t.Logf("GAP: OB-MySQL CREATE SEQUENCE failed: %v", err)
	} else {
		defer db.Exec(`DROP SEQUENCE IF EXISTS seq_probe`)
		t.Log("OB-MySQL CREATE SEQUENCE ok")
		var next int64
		if err := db.QueryRow(`SELECT seq_probe.NEXTVAL`).Scan(&next); err != nil {
			t.Logf("NEXTVAL: %v", err)
		} else {
			t.Logf("NEXTVAL = %d", next)
		}
	}

	// 字典候选逐项探查（容忍失败；记录结果供实现 querier 决策）
	candidates := []string{
		`SHOW SEQUENCES`,
		`SELECT * FROM information_schema.sequences`,
		`SELECT sequence_schema, sequence_name FROM information_schema.sequences`,
		`SELECT * FROM oceanbase.__all_sequence_object`,
		`SELECT * FROM information_schema.__all_sequence_object`,
	}
	for _, q := range candidates {
		rows, err := db.Query(q)
		if err != nil {
			t.Logf("dict %-70s -> ERR: %v", q, err)
			continue
		}
		cols, _ := rows.Columns()
		var sample []string
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if rows.Scan(ptrs...) != nil {
				break
			}
			var sb strings.Builder
			for i, v := range vals {
				if i > 0 {
					sb.WriteString("|")
				}
				fmt.Fprintf(&sb, "%v", v)
			}
			sample = append(sample, sb.String())
			if len(sample) >= 2 {
				break
			}
		}
		rows.Close()
		t.Logf("dict %-70s -> OK cols=%v sample=%v", q, cols, sample)
	}

	// 现状确认：OB-MySQL querier（P6-2）应已产出序列
	sm, err := extractor.Extract(db, "oceanbase-mysql", srcOBMDB)
	if err != nil {
		t.Fatalf("extract obmysql: %v", err)
	}
	seqs := sm.GetSequences(srcOBMDB)
	if len(seqs) < 1 {
		t.Errorf("extractor returns %d sequences, want >= 1 (querier P6-2 应已实现)", len(seqs))
	} else {
		for _, s := range seqs {
			t.Logf("extracted sequence %s start=%d incr=%d min=%d max=%d cache=%d cycle=%s order=%s",
				s.SequenceName, s.StartValue, s.IncrementBy, s.MinValue, s.MaxValue, s.CacheSize, s.Cycle, s.OrderFlag)
			if s.SequenceName == "seq_probe" && s.StartValue != 100 {
				t.Errorf("seq_probe start = %d, want 100", s.StartValue)
			}
		}
	}
}
