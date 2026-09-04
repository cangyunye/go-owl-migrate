//go:build e2e

// 迁移主线 M1/M2/M3/M4/M5：mysql / pg / ob-mysql → OB Oracle / OB MySQL 租户。
// 与 docker 版 e2e 同构（复用 cmd 包 e2e helper），DSN 走环境变量或
// testdata/db/.local-dev.env（凭据不入库）。PG 不可达时对应用例自动 skip。
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/service"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/importer"
)

const devPWDotenv = "testdata/db/.local-dev.env"

func devEnv(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, k := range []string{"OWL_E2E_OB_ORACLE_DSN", "OWL_E2E_OB_MYSQL_DSN", "OWL_E2E_MYSQL_DSN", "OWL_E2E_PG_DSN", "OWL_E2E_DEV_PW"} {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	dir, _ := os.Getwd()
	for i := 0; i < 4 && dir != "/" && dir != "."; i++ {
		p := filepath.Join(dir, devPWDotenv)
		if b, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				line = strings.TrimPrefix(line, "export ")
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), "'\"")
				if _, seen := out[k]; !seen {
					out[k] = v
				}
			}
			return out
		}
		dir = filepath.Dir(dir)
	}
	return out
}

func devGet(t *testing.T, env map[string]string, key string) string {
	t.Helper()
	v := strings.TrimSpace(env[key])
	if v == "" {
		t.Skipf("%s 未配置（set env or testdata/db/.local-dev.env）", key)
	}
	return v
}

// obOracleParts 从 oceanbase-oracle DSN 提取 host/tenant，用于构造测试用户 DSN。
func obOracleParts(t *testing.T, dsn string) (host string, tenant string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		t.Fatalf("bad ob oracle DSN: %v", err)
	}
	user := u.User.Username() // 形如 sys@oratest
	if i := strings.Index(user, "@"); i >= 0 {
		tenant = user[i+1:]
	} else {
		tenant = user
	}
	host = u.Host
	return host, tenant
}

func encodePW(pw string) string {
	return strings.NewReplacer("%", "%25", "@", "%40", "#", "%23", " ", "%20").Replace(pw)
}

func obUserDSNFor(host, tenant, user, pw string) string {
	return "oceanbase-oracle://" + user + "@" + tenant + ":" + encodePW(pw) + "@" + host + "/"
}

// ensureOBTestUser 幂等重建 OB Oracle 测试用户。
func ensureOBTestUser(t *testing.T, sys *sql.DB, user, pw string) {
	t.Helper()
	drop := fmt.Sprintf(`BEGIN EXECUTE IMMEDIATE 'DROP USER "%s" CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`, user)
	if _, err := sys.Exec(drop); err != nil {
		t.Fatalf("drop user %s: %v", user, err)
	}
	if _, err := sys.Exec(fmt.Sprintf(`CREATE USER "%s" IDENTIFIED BY "%s"`, user, pw)); err != nil {
		t.Fatalf("create user %s: %v", user, err)
	}
	for _, g := range []string{
		fmt.Sprintf(`GRANT CONNECT, RESOURCE TO "%s"`, user),
		fmt.Sprintf(`GRANT CREATE VIEW TO "%s"`, user),
		fmt.Sprintf(`GRANT CREATE SYNONYM TO "%s"`, user),
	} {
		if _, err := sys.Exec(g); err != nil {
			t.Fatalf("grant %s: %v", user, err)
		}
	}
}

const devSeedMySQL = `
CREATE TABLE dept (deptno INT PRIMARY KEY, dname VARCHAR(30) NOT NULL, loc VARCHAR(30)) ENGINE=InnoDB;
CREATE TABLE emp (
	empno INT NOT NULL, ename VARCHAR(20) NOT NULL, job VARCHAR(20), sal DECIMAL(9,2),
	hiredate DATETIME, deptno INT, PRIMARY KEY (empno),
	CONSTRAINT fk_emp_dept FOREIGN KEY (deptno) REFERENCES dept (deptno)) ENGINE=InnoDB;
CREATE INDEX idx_emp_ename ON emp (ename);
INSERT INTO dept (deptno, dname, loc) VALUES (10, 'ACCOUNTING', 'NEW YORK'), (20, 'RESEARCH', 'DALLAS');
INSERT INTO emp (empno, ename, job, sal, hiredate, deptno) VALUES
	(7369, 'SMITH', 'CLERK', 800.00, '1980-12-17 00:00:00', 20),
	(7782, 'CLARK', 'MANAGER', 2450.00, '1981-06-09 00:00:00', 10),
	(7839, 'KING', 'PRESIDENT', 5000.00, '1981-11-17 00:00:00', 10);
CREATE VIEW v_emp AS SELECT empno, ename, sal, deptno FROM emp;
CREATE FUNCTION fn_bonus (p_sal DECIMAL(9,2)) RETURNS DECIMAL(9,2) DETERMINISTIC RETURN p_sal * 0.1;
`

// seedMySQLSource 在 root DSN（无库名）上建/重建 db 并灌种子，返回连到该库的 conn。
func seedMySQLSource(t *testing.T, dbType, rootDSN, db string) *sql.DB {
	t.Helper()
	admin := connectE2E(t, dbType, rootDSN)
	if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db)); err != nil {
		t.Fatalf("drop db %s: %v", db, err)
	}
	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", db)); err != nil {
		t.Fatalf("create db %s: %v", db, err)
	}
	admin.Close()
	db2 := connectE2E(t, dbType, rootDSN+db)
	for _, stmt := range strings.Split(devSeedMySQL, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db2.Exec(stmt); err != nil {
			t.Fatalf("seed exec: %v\n%s", err, stmt)
		}
	}
	return db2
}

// runDevMigratePipeline 与 docker runMigratePipeline 同构，但目标行数不写死：
// 每表目标计数 == 源计数，并抽查一条值。
func runDevMigratePipeline(t *testing.T, srcType, srcDSN, srcSchema, tgtType, tgtDSN string, mapping map[string]string) {
	t.Helper()
	tic := time.Now()

	metaCfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: srcType, DSN: srcDSN, Schema: srcSchema},
		Target:   config.DBConfig{Type: tgtType, DSN: tgtDSN},
		DDL: config.DDLConfig{
			TargetDialect:      tgtType,
			SchemaMapping:      mapping,
			IncludeIfNotExists: !strings.EqualFold(tgtType, "oracle"),
		},
	}
	srcDB := connectE2E(t, srcType, srcDSN)
	tgtDB := connectE2E(t, tgtType, tgtDSN)

	sm, err := loadSchemaModel(metaCfg)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables from source metadata")
	}
	pkMap := buildPKMap(sm)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := ensureTablesForMigrate(ctx, tgtDB, sm, metaCfg); err != nil {
		t.Fatalf("create target tables: %v", err)
	}

	tmpDir := t.TempDir()
	exp := exporter.New(srcDB, exporter.Config{
		OutputDir: tmpDir, PageSize: 100, MaxWorkers: 1,
		CSVDelimiter: ",", CSVHeader: true, CSVNullRep: "\\N",
		DBType: srcType, Logger: nopLogger(),
		PlaceholderFamily: placeholderFamilyFor(config.DBConfig{Type: srcType, DSN: srcDSN}),
	})
	res, err := exp.ExportTables(ctx, tables, pkMap)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, r := range res {
		if r.Error != nil {
			t.Errorf("export %s.%s: %v", r.Schema, r.Table, r.Error)
		}
	}

	impCfg := importer.Config{
		SourceDir: tmpDir, CSVDelimiter: ",", CSVNullMarker: "\\N",
		CommitInterval: 100, ErrorPolicy: "stop", MaxWorkers: 1,
		DateTimeFormat: "yyyyMMddHHmmss", TrimStrings: true,
		RespectForeignKeys: false, TargetDBType: tgtType, Logger: nopLogger(),
		PlaceholderFamily: placeholderFamilyFor(config.DBConfig{Type: tgtType, DSN: tgtDSN}),
	}
	if !strings.EqualFold(tgtType, "oracle") {
		impCfg.TruncateBefore = true
	}
	imp := importer.New(tgtDB, impCfg)
	ires, err := imp.ImportTables(ctx, tables, metaCfg.DDL.SchemaMapping)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, r := range ires {
		if r.Err != nil {
			t.Errorf("import %s.%s: %v", r.Schema, r.Table, r.Err)
		}
	}

	for _, tbl := range tables {
		tgtSchema := mapping[tbl.TableSchema]
		got := countTargetRows(t, tgtDB, tgtSchema, tbl.TableName, tgtType)
		want := countTargetRows(t, srcDB, tbl.TableSchema, tbl.TableName, srcType)
		if got != want {
			t.Errorf("%s rows target=%d source=%d", tbl.TableName, got, want)
		}
		t.Logf("%s: source=%d target=%d ok", tbl.TableName, want, got)
	}
	// 值保真抽查：首表首列（ORDER BY 1）首值，源/目标一致。
	spotTbl := tables[0]
	spotCol := spotTbl.GetColumns()[0].ColumnName
	spotVal := func(db *sql.DB, schema, dbType string) string {
		q := fmt.Sprintf(`SELECT "%s" FROM "%s"."%s" ORDER BY 1`, spotCol, schema, spotTbl.TableName)
		if strings.Contains(strings.ToLower(dbType), "mysql") {
			q = fmt.Sprintf("SELECT `%s` FROM `%s`.`%s` ORDER BY 1", spotCol, schema, spotTbl.TableName)
		}
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			q2 := fmt.Sprintf("SELECT %s FROM %s.%s ORDER BY 1", spotCol, schema, spotTbl.TableName)
			if err2 := db.QueryRow(q2).Scan(&v); err2 != nil {
				t.Fatalf("spot %s.%s: %v", schema, spotTbl.TableName, err)
			}
		}
		return v
	}
	tgtSchema := mapping[spotTbl.TableSchema]
	sv, tv := spotVal(srcDB, spotTbl.TableSchema, srcType), spotVal(tgtDB, tgtSchema, tgtType)
	if sv != tv {
		t.Errorf("spot %s.%s(%s): source=%q target=%q", tgtSchema, spotTbl.TableName, spotCol, sv, tv)
	}
	t.Logf("pipeline ok in %v", time.Since(tic))
}

// TestDevMigrate_M1_MySQLToObOracle：mysql → OB Oracle（MIG_MYSQL 用户）。
func TestDevMigrate_M1_MySQLToObOracle(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	host, tenant := obOracleParts(t, obDsn)

	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_mysql")
	t.Cleanup(func() { src.Close() })

	sys := connectE2E(t, "oceanbase-oracle", obDsn)
	ensureOBTestUser(t, sys, "MIG_MYSQL", pw)

	tgtDSN := obUserDSNFor(host, tenant, "MIG_MYSQL", pw)
	runDevMigratePipeline(t, "mysql", mysqlRoot+"migsrc_mysql", "migsrc_mysql",
		"oceanbase-oracle", tgtDSN, map[string]string{"migsrc_mysql": "MIG_MYSQL"})
}

// TestDevMigrate_M5_OBMySQLToObOracle：OB MySQL → OB Oracle（MIG_OBM 用户）。
func TestDevMigrate_M5_OBMySQLToObOracle(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	obmRoot := devGet(t, env, "OWL_E2E_OB_MYSQL_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	host, tenant := obOracleParts(t, obDsn)

	src := seedMySQLSource(t, "oceanbase-mysql", obmRoot, "migsrc_obm")
	t.Cleanup(func() { src.Close() })

	sys := connectE2E(t, "oceanbase-oracle", obDsn)
	ensureOBTestUser(t, sys, "MIG_OBM", pw)

	tgtDSN := obUserDSNFor(host, tenant, "MIG_OBM", pw)
	runDevMigratePipeline(t, "oceanbase-mysql", obmRoot+"migsrc_obm", "migsrc_obm",
		"oceanbase-oracle", tgtDSN, map[string]string{"migsrc_obm": "MIG_OBM"})
}

// TestDevMigrate_M2_MySQLToObMySQL：mysql → OB MySQL（同库名目标）。
func TestDevMigrate_M2_MySQLToObMySQL(t *testing.T) {
	env := devEnv(t)
	obmRoot := devGet(t, env, "OWL_E2E_OB_MYSQL_DSN")
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")

	src := seedMySQLSource(t, "mysql", mysqlRoot, "migsrc_mysql")
	t.Cleanup(func() { src.Close() })

	admin := connectE2E(t, "oceanbase-mysql", obmRoot)
	const db = "migtgt_m2"
	if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db)); err != nil {
		t.Fatalf("drop target db: %v", err)
	}
	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", db)); err != nil {
		t.Fatalf("create target db: %v", err)
	}
	admin.Close()

	runDevMigratePipeline(t, "mysql", mysqlRoot+"migsrc_mysql", "migsrc_mysql",
		"oceanbase-mysql", obmRoot+db, map[string]string{"migsrc_mysql": db})
}

// TestDevMigrate_M3_M4_PG：PG（多用户 schema）→ OB 两租户；PG 不可达时 skip。
func TestDevMigrate_M3_M4_PG(t *testing.T) {
	env := devEnv(t)
	pgDsn := strings.TrimSpace(env["OWL_E2E_PG_DSN"])
	obDsn := strings.TrimSpace(env["OWL_E2E_OB_ORACLE_DSN"])
	obmRoot := strings.TrimSpace(env["OWL_E2E_OB_MYSQL_DSN"])
	if pgDsn == "" {
		t.Skip("OWL_E2E_PG_DSN 未配置")
	}
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	pg := connectE2E(t, "postgres", pgDsn)
	seedPGForDevMigrate(t, pg, pw)
	if obDsn != "" {
		t.Run("pg_hr_to_ob_oracle", func(t *testing.T) {
			host, tenant := obOracleParts(t, obDsn)
			sys := connectE2E(t, "oceanbase-oracle", obDsn)
			ensureOBTestUser(t, sys, "MIG_PG_HR", pw)
			runDevMigratePipeline(t, "postgres", pgDsn, "src_hr",
				"oceanbase-oracle", obUserDSNFor(host, tenant, "MIG_PG_HR", pw),
				map[string]string{"src_hr": "MIG_PG_HR"})
		})
	}
	if obmRoot != "" {
		t.Run("pg_fin_to_ob_mysql", func(t *testing.T) {
			admin := connectE2E(t, "oceanbase-mysql", obmRoot)
			const db = "migtgt_pgfin"
			admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", db))
			admin.Exec(fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", db))
			admin.Close()
			runDevMigratePipeline(t, "postgres", pgDsn, "src_fin",
				"oceanbase-mysql", obmRoot+db, map[string]string{"src_fin": db})
		})
	}
}

// seedPGForDevMigrate 建 src_hr/src_fin（owner 为独立 role）与表；非超管则 skip。
func seedPGForDevMigrate(t *testing.T, db *sql.DB, pw string) {
	t.Helper()
	var isSuper bool
	if err := db.QueryRow("SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&isSuper); err != nil || !isSuper {
		t.Skip("pg 用户非超管，跳过种子")
	}
	cmds := []string{
		`DROP SCHEMA IF EXISTS src_hr CASCADE`, `DROP SCHEMA IF EXISTS src_fin CASCADE`,
		`DROP ROLE IF EXISTS hr_owner`, `DROP ROLE IF EXISTS fin_owner`,
		fmt.Sprintf(`CREATE ROLE hr_owner LOGIN PASSWORD '%s'`, pw),
		fmt.Sprintf(`CREATE ROLE fin_owner LOGIN PASSWORD '%s'`, pw),
		`CREATE SCHEMA src_hr AUTHORIZATION hr_owner`,
		`CREATE SCHEMA src_fin AUTHORIZATION fin_owner`,
		`CREATE TABLE src_hr.dept (deptno INT PRIMARY KEY, dname TEXT NOT NULL, loc TEXT)`,
		`CREATE TABLE src_hr.emp (empno INT PRIMARY KEY, ename TEXT NOT NULL, sal NUMERIC(9,2), deptno INT REFERENCES src_hr.dept(deptno))`,
		`INSERT INTO src_hr.dept VALUES (10,'ACCOUNTING','NY'),(20,'RESEARCH','DAL')`,
		`INSERT INTO src_hr.emp VALUES (7369,'SMITH',800,20),(7839,'KING',5000,10)`,
		`CREATE TABLE src_fin.ledger (id BIGSERIAL PRIMARY KEY, amount NUMERIC(14,2), note TEXT)`,
		`INSERT INTO src_fin.ledger (amount,note) VALUES (12.34,'a'),(0.0,'b')`,
	}
	for _, c := range cmds {
		if _, err := db.Exec(c); err != nil {
			t.Fatalf("pg seed: %v\n%s", err, c)
		}
	}
}

// seedMIGSRConOB 在 OB Oracle 租户重建 MIGSRC（含大写 EMP/DEPT + 视图/序列），
// MIGTGT 由 ensureOBTestUser 提供。M6 复用统一管线跑内部迁移。
func seedMIGSRConOB(t *testing.T, sys *sql.DB, pw string) string {
	t.Helper()
	drop := `BEGIN EXECUTE IMMEDIATE 'DROP USER "MIGSRC" CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`
	if _, err := sys.Exec(drop); err != nil {
		t.Fatalf("drop MIGSRC: %v", err)
	}
	if _, err := sys.Exec(`CREATE USER "MIGSRC" IDENTIFIED BY "` + pw + `"`); err != nil {
		t.Fatalf("create MIGSRC: %v", err)
	}
	for _, g := range []string{
		`GRANT CONNECT, RESOURCE TO "MIGSRC"`,
		`GRANT CREATE VIEW TO "MIGSRC"`,
		`GRANT CREATE SYNONYM TO "MIGSRC"`,
	} {
		if _, err := sys.Exec(g); err != nil {
			t.Fatalf("grant MIGSRC: %v", err)
		}
	}
	return drop
}

// ensureMIGSRCWithObjects 重建 MIGSRC 用户并以之建种子对象（EMP/DEPT+视图/序列）。
func ensureMIGSRCWithObjects(t *testing.T, obDsn, pw string) (host, tenant, srcDSN string) {
	t.Helper()
	host, tenant = obOracleParts(t, obDsn)
	sys := connectE2E(t, "oceanbase-oracle", obDsn)
	seedMIGSRConOB(t, sys, pw)
	srcDSN = obUserDSNFor(host, tenant, "MIGSRC", pw)
	src := connectE2E(t, "oceanbase-oracle", srcDSN)
	for _, stmt := range []string{
		`CREATE TABLE DEPT (DEPTNO NUMBER(4) PRIMARY KEY, DNAME VARCHAR2(30), LOC VARCHAR2(30))`,
		`CREATE TABLE EMP (EMPNO NUMBER(4) PRIMARY KEY, ENAME VARCHAR2(20) NOT NULL, JOB VARCHAR2(20), SAL NUMBER(9,2), DEPTNO NUMBER(4))`,
		`ALTER TABLE EMP ADD CONSTRAINT FK_EMP_DEPT FOREIGN KEY (DEPTNO) REFERENCES DEPT (DEPTNO)`,
		`CREATE INDEX IDX_EMP_ENAME ON EMP (ENAME)`,
		`INSERT INTO DEPT VALUES (10, 'ACCOUNTING', 'NEW YORK'), (20, 'RESEARCH', 'DALLAS')`,
		`INSERT INTO EMP (EMPNO, ENAME, JOB, SAL, DEPTNO) VALUES (7369, 'SMITH', 'CLERK', 800, 20), (7839, 'KING', 'PRESIDENT', 5000, 10)`,
		`COMMIT`,
		`CREATE SEQUENCE SEQ_EMP START WITH 100 INCREMENT BY 1 NOCACHE`,
		`CREATE VIEW V_EMP AS SELECT EMPNO, ENAME, SAL FROM EMP`,
	} {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("MIGSRC seed: %v\n%s", err, stmt)
		}
	}
	return host, tenant, srcDSN
}

// TestDevMigrate_M6_ObOracleInternal：OB Oracle 租户内部 MIGSRC → MIGTGT 新 schema。
func TestDevMigrate_M6_ObOracleInternal(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	host, tenant := obOracleParts(t, obDsn)

	_, _, srcDSN := ensureMIGSRCWithObjects(t, obDsn, pw)

	sys := connectE2E(t, "oceanbase-oracle", obDsn)
	ensureOBTestUser(t, sys, "MIGTGT", pw)
	tgtDSN := obUserDSNFor(host, tenant, "MIGTGT", pw)
	runDevMigratePipeline(t, "oceanbase-oracle", srcDSN, "MIGSRC",
		"oceanbase-oracle", tgtDSN, map[string]string{"MIGSRC": "MIGTGT"})
}

// TestDevMigrate_M7_ObOracleToPostgres：OB Oracle(MIGSRC) → PostgreSQL。
func TestDevMigrate_M7_ObOracleToPostgres(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	pgDsn := devGet(t, env, "OWL_E2E_PG_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	_, _, srcDSN := ensureMIGSRCWithObjects(t, obDsn, pw)
	// 目标 schema 由 ensureTablesForMigrate 对 postgres 自动创建
	const tgtSchema = "obtgt_m7"
	tgtPG := connectE2E(t, "postgres", pgDsn)
	tgtPG.Exec("DROP SCHEMA IF EXISTS " + tgtSchema + " CASCADE")
	tgtPG.Close()
	runDevMigratePipeline(t, "oceanbase-oracle", srcDSN, "MIGSRC",
		"postgres", pgDsn, map[string]string{"MIGSRC": tgtSchema})
}

// TestDevMigrate_M8_ObOracleToMySQL：OB Oracle(MIGSRC) → MySQL。
func TestDevMigrate_M8_ObOracleToMySQL(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	mysqlRoot := devGet(t, env, "OWL_E2E_MYSQL_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	_, _, srcDSN := ensureMIGSRCWithObjects(t, obDsn, pw)

	const tgtDB = "migtgt_m8"
	admin := connectE2E(t, "mysql", mysqlRoot)
	admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", tgtDB))
	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", tgtDB)); err != nil {
		t.Fatalf("create mysql target: %v", err)
	}
	admin.Close()

	runDevMigratePipeline(t, "oceanbase-oracle", srcDSN, "MIGSRC",
		"mysql", mysqlRoot+tgtDB, map[string]string{"MIGSRC": tgtDB})
}

// TestExportMetadata_Live_ObOracle：实库 OB-Oracle 的 export-metadata CSV(13文件)
// 与 SQL(dba_* INSERT) 双格式场景；含 --objects 过滤（仅 views,sequences）。
func TestExportMetadata_Live_ObOracle(t *testing.T) {
	env := devEnv(t)
	obDsn := devGet(t, env, "OWL_E2E_OB_ORACLE_DSN")
	pw := devGet(t, env, "OWL_E2E_DEV_PW")
	_, _, srcDSN := ensureMIGSRCWithObjects(t, obDsn, pw)

	cfg := &config.Config{
		Metadata: config.MetadataConfig{Type: "database"},
		Source:   config.DBConfig{Type: "oceanbase-oracle", DSN: srcDSN, Schema: "MIGSRC"},
	}
	sm, err := loadSchemaModel(cfg)
	if err != nil {
		t.Fatalf("load OB oracle metadata: %v", err)
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		t.Fatal("no tables")
	}

	t.Run("csv_13_files", func(t *testing.T) {
		out := t.TempDir()
		files, err := service.ExportMetadataFiles(out, sm, nil)
		if err != nil {
			t.Fatalf("export csv: %v", err)
		}
		if len(files) != 13 {
			t.Fatalf("csv files = %d, want 13: %v", len(files), files)
		}
		names := map[string]bool{}
		for _, f := range files {
			names[filepath.Base(f)] = true
		}
		for _, stem := range []string{"tables", "columns", "primary_keys", "indexes", "foreign_keys",
			"views", "mviews", "sequences", "synonyms", "triggers", "functions", "packages", "package_bodies"} {
			if !names[stem+".csv"] {
				t.Errorf("missing %s.csv (have %d files)", stem, len(files))
			}
		}
		b, _ := os.ReadFile(filepath.Join(out, "tables.csv"))
		if !strings.Contains(string(b), "MIGSRC,EMP") || !strings.Contains(string(b), "MIGSRC,DEPT") {
			t.Errorf("tables.csv missing MIGSRC tables:\n%s", b)
		}
		seq, _ := os.ReadFile(filepath.Join(out, "sequences.csv"))
		if !strings.Contains(string(seq), "SEQ_EMP") {
			t.Errorf("sequences.csv missing SEQ_EMP:\n%s", seq)
		}
		vw, _ := os.ReadFile(filepath.Join(out, "views.csv"))
		if !strings.Contains(string(vw), "V_EMP") {
			t.Errorf("views.csv missing V_EMP:\n%s", vw)
		}
	})

	t.Run("sql_format", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "metadata.sql")
		if err := exportMetadataSQL(out, "oceanbase-oracle", sm, tables, "MIGSRC"); err != nil {
			t.Fatalf("export sql: %v", err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		content := string(b)
		for _, want := range []string{"INSERT INTO dba_tables", "INSERT INTO dba_tab_columns", "MIGSRC", "EMP"} {
			if !strings.Contains(content, want) {
				t.Errorf("sql export missing %q:\n%s", want, content)
			}
		}
	})

	t.Run("objects_filter_views_sequences", func(t *testing.T) {
		out := t.TempDir()
		set, err := md.ParseObjectTypes("views,sequences")
		if err != nil {
			t.Fatal(err)
		}
		files, err := service.ExportMetadataFiles(out, sm, set)
		if err != nil {
			t.Fatalf("export filtered: %v", err)
		}
		if len(files) != 2 {
			t.Fatalf("filtered files = %v, want views.csv+sequences.csv", files)
		}
		for _, f := range files {
			base := filepath.Base(f)
			if base != "views.csv" && base != "sequences.csv" {
				t.Errorf("unexpected filtered file %s", base)
			}
		}
	})
}
