//go:build e2e

package e2edev

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

// TestPhaseA_Connections：四库连通/版本/兼容模式探活 + OB Oracle 测试用户与
// MIGSRC 种子引导（后续 Phase B 的抽取对象基础）。
func TestPhaseA_Connections(t *testing.T) {
	e := loadEnv(t)

	t.Run("ob_oracle_connect", func(t *testing.T) {
		skipNoEnv(t, e.OBOracleDSN, "OWL_E2E_OB_ORACLE_DSN")
		db := connect(t, "oceanbase-oracle", e.OBOracleDSN)
		banner := oracleBanner(t, db)
		t.Logf("banner: %s", banner)

		cfg := config.DBConfig{Type: "oceanbase-oracle", DSN: e.OBOracleDSN}
		if dbconn.OceanBaseOracleUsesMySQLWire(cfg) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			mode := dbconn.ProbeOceanBaseCompatMode(ctx, db)
			if mode == "" {
				t.Errorf("compat_mode probe returned empty")
			} else if mode != "oracle" {
				t.Errorf("compat_mode = %q, want oracle", mode)
			} else {
				t.Logf("compat_mode = oracle")
			}
		} else {
			t.Logf("TNS wire: compat probe skipped")
		}
	})

	t.Run("ob_oracle_bootstrap_users", func(t *testing.T) {
		skipNoEnv(t, e.OBOracleDSN, "OWL_E2E_OB_ORACLE_DSN")
		BootstrapOBOracleUsers(t, e)
	})

	t.Run("migsrc_fixture", func(t *testing.T) {
		skipNoEnv(t, e.OBOracleDSN, "OWL_E2E_OB_ORACLE_DSN")
		db := EnsureMIGSRCFixture(t, e)
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM user_tables`).Scan(&n); err != nil {
			t.Fatalf("user_tables: %v", err)
		}
		if n < 3 {
			t.Errorf("MIGSRC user_tables = %d, want >= 3 (EMP/DEPT/PART_SALES)", n)
		}
		var seq int
		if err := db.QueryRow(`SELECT COUNT(*) FROM user_sequences`).Scan(&seq); err != nil {
			t.Fatalf("user_sequences: %v", err)
		}
		t.Logf("MIGSRC tables=%d seq=%d", n, seq)
	})

	t.Run("ob_mysql_connect", func(t *testing.T) {
		skipNoEnv(t, e.OBMysqlDSN, "OWL_E2E_OB_MYSQL_DSN")
		db := connect(t, "oceanbase-mysql", e.OBMysqlDSN)
		var v string
		if err := db.QueryRow(`SELECT version()`).Scan(&v); err != nil {
			t.Fatalf("version(): %v", err)
		}
		t.Logf("ob-mysql version: %s", v)
	})

	t.Run("mysql_connect", func(t *testing.T) {
		skipNoEnv(t, e.MysqlDSN, "OWL_E2E_MYSQL_DSN")
		db := connect(t, "mysql", e.MysqlDSN)
		var v string
		if err := db.QueryRow(`SELECT version()`).Scan(&v); err != nil {
			t.Fatalf("version(): %v", err)
		}
		t.Logf("mysql version: %s", v)
	})

	t.Run("pg_multiowner_bootstrap", func(t *testing.T) {
		skipNoEnv(t, e.PgDSN, "OWL_E2E_PG_DSN")
		db := BootstrapPGMultiOwner(t, e)
		rows, err := db.Query(fmt.Sprintf(
			`SELECT n.nspname, r.rolname FROM pg_namespace n
			 JOIN pg_roles r ON r.oid = n.nspowner
			 WHERE n.nspname IN ('%s','%s') ORDER BY 1`, pgSchHr, pgSchFn))
		if err != nil {
			t.Fatalf("owner query: %v", err)
		}
		defer rows.Close()
		type owner struct{ schema, role string }
		var got []owner
		for rows.Next() {
			var o owner
			if err := rows.Scan(&o.schema, &o.role); err != nil {
				t.Fatal(err)
			}
			got = append(got, o)
		}
		want := map[string]string{pgSchHr: pgRoleHr, pgSchFn: pgRoleFn}
		for _, o := range got {
			if want[o.schema] != o.role {
				t.Errorf("schema %s owner = %s, want %s", o.schema, o.role, want[o.schema])
			}
			t.Logf("schema %s owned by %s (conn user differs)", o.schema, o.role)
		}
	})
}
