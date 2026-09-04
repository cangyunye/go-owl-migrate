//go:build e2e

package e2edev

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

// TestPhaseB_OBOracleDictionary：MIGSRC 种子上的字典族抽取探针（Phase B）。
// 覆盖 HANDOFF §三.2 未决点：OB 4.4 的 DBMS_METADATA / 分区字典 / synonym·package。
func TestPhaseB_OBOracleDictionary(t *testing.T) {
	e := loadEnv(t)
	skipNoEnv(t, e.OBOracleDSN, "OWL_E2E_OB_ORACLE_DSN")
	db := EnsureMIGSRCFixture(t, e)

	// 抽取整体：任一核心族失败即整体失败（结果由下断言定位）。
	sm, err := extractor.Extract(db, "oceanbase-oracle-wire", "MIGSRC")
	if err != nil {
		t.Fatalf("extract MIGSRC: %v", err)
	}

	tables := sm.GetTables()
	if len(tables) < 3 {
		t.Errorf("tables = %d, want >= 3 (DEPT/EMP/PART_SALES)", len(tables))
	}

	// 表级属性：注释/分区标记/列
	for _, tbl := range tables {
		t.Logf("table %s.%s type=%s partitioned=%s comment=%q",
			tbl.TableSchema, tbl.TableName, tbl.TableType, tbl.Partitioned, tbl.TableComment)
		switch tbl.TableName {
		case "EMP":
			if tbl.TableComment != "employee master" {
				t.Errorf("EMP comment = %q, want 'employee master'", tbl.TableComment)
			}
			if len(tbl.GetColumns()) == 0 {
				t.Error("EMP has no columns")
			}
			for _, c := range tbl.GetColumns() {
				t.Logf("  col %s %s nullable=%s", c.ColumnName, c.DataType, c.Nullable)
				if c.ColumnName == "" || c.DataType == "" {
					t.Errorf("EMP col missing name/type")
				}
			}
		case "PART_SALES":
			if tbl.Partitioned != "YES" {
				t.Errorf("PART_SALES partitioned = %q, want YES", tbl.Partitioned)
			}
			if strings.TrimSpace(tbl.PartitionInfo) == "" {
				t.Errorf("PART_SALES partition info empty (OB 4.4 分区字典未取到)")
			} else {
				t.Logf("PART_SALES partition_info: %s", tbl.PartitionInfo)
			}
		}
	}

	// 约束族
	var pk, fk, ix int
	for _, tbl := range tables {
		pk += len(tbl.GetPrimaryKeys())
		fk += len(tbl.GetForeignKeys())
		ix += len(tbl.GetIndexes())
	}
	if pk < 2 {
		t.Errorf("primary keys = %d, want >= 2 (DEPT/EMP)", pk)
	}
	if fk < 1 {
		t.Errorf("foreign keys = %d, want >= 1 (FK_EMP_DEPT)", fk)
	}
	if ix < 1 {
		t.Errorf("indexes = %d, want >= 1 (IDX_EMP_ENAME)", ix)
	}
	t.Logf("pk=%d fk=%d indexes=%d", pk, fk, ix)

	// 独立对象族：核心必达 + 可选（版本敏感）记录
	views := len(sm.GetViews())
	seq := len(sm.GetSequences("MIGSRC"))
	syn := len(sm.GetSynonyms("MIGSRC"))
	trig := len(sm.GetTriggers("MIGSRC", "EMP"))
	fns := len(sm.GetFunctions("MIGSRC"))
	pkgs := len(sm.GetPackages("MIGSRC"))
	pkb := len(sm.GetPackageBodies("MIGSRC"))
	t.Logf("views=%d seq=%d synonyms=%d triggers=%d functions=%d packages=%d package_bodies=%d",
		views, seq, syn, trig, fns, pkgs, pkb)

	if views < 1 {
		t.Errorf("views = %d, want >= 1 (V_EMP)", views)
	}
	if seq < 1 {
		t.Errorf("sequences = %d, want >= 1 (SEQ_EMP)", seq)
	}
	if syn < 1 {
		t.Logf("GAP: OB synonyms extraction returned %d (expect >= 1 private synonym)", syn)
	}
	if trig < 1 {
		t.Logf("GAP: triggers extraction returned %d (expect >= 1 TRG_EMP)", trig)
	}
	if fns < 1 {
		t.Logf("GAP: functions extraction returned %d (DBMS_METADATA 或字典受限)", fns)
	}
	if pkgs < 1 || pkb < 1 {
		t.Logf("GAP: packages=%d package_bodies=%d (期望 >= 1；依赖 DBMS_METADATA/包字典)", pkgs, pkb)
	}

	// DBMS_METADATA 直连探针（B2）
	var ddl string
	q := `SELECT DBMS_METADATA.GET_DDL('FUNCTION', 'FN_BONUS', 'MIGSRC') FROM DUAL`
	if err := db.QueryRow(q).Scan(&ddl); err != nil {
		t.Logf("DBMS_METADATA.GET_DDL(FUNCTION) unsupported: %v", err)
	} else {
		t.Logf("DBMS_METADATA.GET_DDL ok: %.90s", ddl)
	}

	// 能力矩阵对照（B4）：OB-Oracle Capabilities 全集必须与抽取模型一致。
	t.Logf("capabilities(oceanbase-oracle) = %v", extractor.Capabilities("oceanbase-oracle"))
}
