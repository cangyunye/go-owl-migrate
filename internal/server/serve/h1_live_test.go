//go:build e2e

// H1：serve 层（HTTP API）走真库的最小 e2e。区别于既有 serve 测试（只喂
// testdata/csv 假元数据），这里用 .local-dev.env 的真 DSN 走：
//
//	/metadata/load(database) → /metadata/tables → 详情(大小写) →
//	/ddl/generate → /metadata/export(csv / sql 能力门)。
package serve

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/helingjun/obconnector-go"
	_ "github.com/lib/pq"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func h1Env(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, k := range []string{"OWL_E2E_OB_ORACLE_DSN", "OWL_E2E_MYSQL_DSN", "OWL_E2E_DEV_PW"} {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	dir, _ := os.Getwd()
	for i := 0; i < 5 && dir != "/" && dir != "."; i++ {
		p := filepath.Join(dir, "testdata", "db", ".local-dev.env")
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

func h1Get(t *testing.T, env map[string]string, key string) string {
	t.Helper()
	v := strings.TrimSpace(env[key])
	if v == "" {
		t.Skipf("%s 未配置（set env or testdata/db/.local-dev.env）", key)
	}
	return v
}

// startLiveServer 以给定 yaml 配置（含推荐映射）起 serve，带 JobStore。
func startLiveServer(t *testing.T, yamlCfg string) *httptest.Server {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "owl-live.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store, ConfigPath: cfgPath, TempDir: t.TempDir()})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ensureMySQLH1 幂等建库/表/数据（CREATE IF NOT EXISTS + INSERT IGNORE）。
func ensureMySQLH1(t *testing.T, rootDSN, db string) string {
	t.Helper()
	dbh, err := sql.Open("mysql", rootDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer dbh.Close()
	for _, stmt := range []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` DEFAULT CHARACTER SET utf8mb4", db),
	} {
		if _, err := dbh.Exec(stmt); err != nil {
			t.Fatalf("ensure db: %v", err)
		}
	}
	dsn := rootDSN + db
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS dept (deptno INT PRIMARY KEY, dname VARCHAR(30) NOT NULL, loc VARCHAR(30)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS emp (empno INT NOT NULL, ename VARCHAR(20) NOT NULL, job VARCHAR(20), sal DECIMAL(9,2), deptno INT, PRIMARY KEY (empno), KEY idx_emp_ename (ename)) ENGINE=InnoDB`,
		`INSERT IGNORE INTO dept (deptno,dname,loc) VALUES (10,'ACCOUNTING','NEW YORK'),(20,'RESEARCH','DALLAS')`,
		`INSERT IGNORE INTO emp (empno,ename,job,sal,deptno) VALUES (7369,'SMITH','CLERK',800,20),(7839,'KING','PRESIDENT',5000,10)`,
	} {
		if _, err := db2.Exec(stmt); err != nil {
			t.Fatalf("ensure tables: %v", err)
		}
	}
	return dsn
}

// h1Open 经仓库 dbconn.Open 建连并注册清理。
func h1Open(t *testing.T, dbType, dsn string) *sql.DB {
	t.Helper()
	db, err := dbconn.Open(config.DBConfig{Type: dbType, DSN: dsn, ConnectTimeout: "15s"})
	if err != nil {
		t.Fatalf("open %s: %v", dbType, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// obOraclePartsLive 从 oceanbase-oracle DSN 取 host/tenant（与 cmd 测试同规则）。
func obOraclePartsLive(t *testing.T, dsn string) (host, tenant string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		t.Fatalf("bad ob oracle DSN: %v", err)
	}
	user := u.User.Username()
	if i := strings.Index(user, "@"); i >= 0 {
		tenant = user[i+1:]
	} else {
		tenant = user
	}
	return u.Host, tenant
}

// ensureOBOracleMIGSRC 幂等建 OB Oracle MIGSRC 用户与一张表（SQL 导出场景用），
// 返回 MIGSRC 身份连接 DSN。
func ensureOBOracleMIGSRC(t *testing.T, obDsn, pw string) string {
	t.Helper()
	host, tenant := obOraclePartsLive(t, obDsn)
	sys := h1Open(t, "oceanbase-oracle", obDsn)
	drop := `BEGIN EXECUTE IMMEDIATE 'DROP USER "MIGSRC" CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`
	if _, err := sys.Exec(drop); err != nil {
		t.Fatalf("drop MIGSRC: %v", err)
	}
	if _, err := sys.Exec(fmt.Sprintf(`CREATE USER "MIGSRC" IDENTIFIED BY "%s"`, pw)); err != nil {
		t.Fatalf("create MIGSRC: %v", err)
	}
	for _, g := range []string{`GRANT CONNECT, RESOURCE TO "MIGSRC"`, `GRANT CREATE VIEW TO "MIGSRC"`} {
		if _, err := sys.Exec(g); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	enc := strings.NewReplacer("%", "%25", "@", "%40", "#", "%23").Replace(pw)
	userDSN := "oceanbase-oracle://MIGSRC@" + tenant + ":" + enc + "@" + host + "/"
	u2 := h1Open(t, "oceanbase-oracle", userDSN)
	for _, stmt := range []string{
		`CREATE TABLE DEPT (DEPTNO NUMBER(4) PRIMARY KEY, DNAME VARCHAR2(30), LOC VARCHAR2(30))`,
		`INSERT INTO DEPT VALUES (10, 'ACCOUNTING', 'NEW YORK')`,
		`COMMIT`,
	} {
		if _, err := u2.Exec(stmt); err != nil {
			t.Fatalf("seed MIGSRC: %v\n%s", err, stmt)
		}
	}
	return userDSN
}

// TestH1_LiveServe_MetadataFlows：MySQL 真库走 serve 全链路。
func TestH1_LiveServe_MetadataFlows(t *testing.T) {
	env := h1Env(t)
	mysqlRoot := h1Get(t, env, "OWL_E2E_MYSQL_DSN")
	const db = "migsrc_mysql"
	dsn := ensureMySQLH1(t, mysqlRoot, db)

	yamlCfg := fmt.Sprintf(`metadata:
  type: database
  source:
    type: mysql
    dsn: %q
    schema: %q
ddl:
  target_dialect: oceanbase-oracle
  schema_mapping:
    migsrc_mysql: MIG_MYSQL
`, dsn, db)
	ts := startLiveServer(t, yamlCfg)

	// 1) metadata/load：database 类型直连真库
	loadBody := fmt.Sprintf(`{"metadata":{"type":"database"},"source":{"type":"mysql","dsn":%q,"schema":%q}}`, dsn, db)
	resp, out := e2ePost(t, ts, "/api/v1/metadata/load", loadBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load status=%d body=%v", resp.StatusCode, out)
	}

	// 2) 表清单
	lr, err := http.Get(ts.URL + "/api/v1/metadata/tables")
	if err != nil {
		t.Fatal(err)
	}
	var tables []map[string]any
	if err := json.NewDecoder(lr.Body).Decode(&tables); err != nil {
		t.Fatalf("decode tables: %v", err)
	}
	lr.Body.Close()
	names := map[string]bool{}
	for _, tb := range tables {
		if n, _ := tb["name"].(string); n != "" {
			names[n] = true
		}
	}
	if !names["dept"] || !names["emp"] {
		t.Fatalf("tables list missing dept/emp: %v", names)
	}

	// 3) 表详情：精确 + 大小写折叠（A4 在真库 HTTP 层复核）
	for _, path := range []string{
		"/api/v1/metadata/tables/migsrc_mysql/dept",
		"/api/v1/metadata/tables/MIGSRC_MYSQL/DePt",
	} {
		dr, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Name    string `json:"name"`
			Columns []any  `json:"columns"`
		}
		if err := json.NewDecoder(dr.Body).Decode(&d); err != nil {
			dr.Body.Close()
			t.Fatalf("decode detail %s: %v", path, err)
		}
		dr.Body.Close()
		if dr.StatusCode != http.StatusOK {
			t.Errorf("detail %s status=%d", path, dr.StatusCode)
			continue
		}
		if d.Name != "dept" || len(d.Columns) == 0 {
			t.Errorf("detail %s name=%q cols=%d, want dept/>0", path, d.Name, len(d.Columns))
		}
	}

	// 4) /ddl/generate：按配置推荐映射 MIG_MYSQL（init 同源语义）
	resp, out = e2ePost(t, ts, "/api/v1/ddl/generate", `{"tables":"dept"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ddl generate status=%d body=%v", resp.StatusCode, out)
	}
	files := genOutFiles(t, out)
	if len(files) == 0 {
		t.Fatalf("ddl generate returned no files: %v", out)
	}
	joined := strings.Join(files, "\n")
	lw := strings.ToLower(joined)
	if !strings.Contains(lw, "create table") || !strings.Contains(lw, `"mig_mysql"."dept"`) {
		t.Errorf("ddl content missing mapped create table:\n%s", joined)
	}

	// 5) /metadata/export：csv 输出
	resp, out = e2ePost(t, ts, "/api/v1/metadata/export",
		fmt.Sprintf(`{"source":{"type":"mysql","dsn":%q,"schema":%q},"format":"csv","scope":"all","objects":"tables,columns,views"}`, dsn, db))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export csv status=%d body=%v", resp.StatusCode, out)
	}
	fm := genFilesMap(t, out)
	if fm["tables.csv"] == "" || fm["columns.csv"] == "" {
		t.Errorf("export files missing tables/columns.csv: got %d files", len(fm))
	}
	if !strings.Contains(fm["tables.csv"], "migsrc_mysql,dept") {
		t.Errorf("tables.csv content missing row:\n%.200s", fm["tables.csv"])
	}

	// 4b) /select/generate：同一已加载模型，逐表 SELECT（引用保真）
	sresp, sout := e2ePost(t, ts, "/api/v1/select/generate", `{"tables":"dept"}`)
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("select generate status=%d body=%v", sresp.StatusCode, sout)
	}
	sfiles := genFilesMap(t, sout)
	var hasSelect bool
	for _, c := range sfiles {
		if strings.Contains(strings.ToUpper(c), "SELECT") {
			hasSelect = true
		}
	}
	if !hasSelect {
		t.Errorf("select generate produced no SELECT content: %d files", len(sfiles))
	}

	// 4c) /metadata/validate：真库模型校验 0 错误
	vresp, err := http.Get(ts.URL + "/api/v1/metadata/validate")
	if err != nil {
		t.Fatal(err)
	}
	var vout map[string]any
	if err := json.NewDecoder(vresp.Body).Decode(&vout); err != nil {
		vresp.Body.Close()
		t.Fatalf("decode validate: %v", err)
	}
	vresp.Body.Close()
	if vresp.StatusCode != http.StatusOK {
		t.Fatalf("validate status=%d body=%v", vresp.StatusCode, vout)
	}
	if n, _ := vout["count"].(float64); n != 0 {
		t.Errorf("validate errors = %v, want 0 (真库模型): %v", vout["count"], vout["errors"])
	}

	// 6) mysql 源 --format sql：能力门应拒绝（仅 oracle 族）
	resp, out = e2ePost(t, ts, "/api/v1/metadata/export",
		fmt.Sprintf(`{"source":{"type":"mysql","dsn":%q,"schema":%q},"format":"sql","scope":"all"}`, dsn, db))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("mysql sql export status=%d (want 400 能力门), body=%v", resp.StatusCode, out)
	}
}

// TestH1_LiveServe_ObOracleSQLExport：OB Oracle 真库 --format sql 正向场景。
func TestH1_LiveServe_ObOracleSQLExport(t *testing.T) {
	env := h1Env(t)
	obDsn := h1Get(t, env, "OWL_E2E_OB_ORACLE_DSN")
	pw := h1Get(t, env, "OWL_E2E_DEV_PW")
	userDSN := ensureOBOracleMIGSRC(t, obDsn, pw)

	ts := startLiveServer(t, fmt.Sprintf("metadata:\n  type: database\n"))

	loadBody := fmt.Sprintf(`{"metadata":{"type":"database"},"source":{"type":"oceanbase-oracle","dsn":%q,"schema":"MIGSRC"}}`, userDSN)
	resp, out := e2ePost(t, ts, "/api/v1/metadata/load", loadBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ob load status=%d body=%v", resp.StatusCode, out)
	}

	resp, out = e2ePost(t, ts, "/api/v1/metadata/export",
		fmt.Sprintf(`{"source":{"type":"oceanbase-oracle","dsn":%q,"schema":"MIGSRC"},"format":"sql","scope":"all"}`, userDSN))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ob sql export status=%d body=%v", resp.StatusCode, out)
	}
	joined := strings.Join(genOutFiles(t, out), "\n")
	if !strings.Contains(joined, "INSERT INTO dba_tables") || !strings.Contains(joined, "DEPT") {
		t.Errorf("ob sql export content missing dba insert:\n%.400s", joined)
	}
}

// genOutFiles 从 generation 类响应中提取 files[].content。
func genOutFiles(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, ok := out["files"].([]any)
	if !ok {
		t.Fatalf("response missing files: %v", out)
	}
	var contents []string
	for _, f := range raw {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if c, ok := m["content"].(string); ok {
			contents = append(contents, c)
		}
	}
	return contents
}

// genFilesMap 把 generation 类响应的 files[] 归为 name→content。
func genFilesMap(t *testing.T, out map[string]any) map[string]string {
	t.Helper()
	raw, ok := out["files"].([]any)
	if !ok {
		t.Fatalf("response missing files: %v", out)
	}
	m := map[string]string{}
	for _, f := range raw {
		mm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		n, _ := mm["name"].(string)
		c, _ := mm["content"].(string)
		if n != "" {
			m[n] = c
		}
	}
	return m
}
