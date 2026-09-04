//go:build e2e

// Package e2edev 是"其他数据库 → OB MySQL/Oracle 租户"方言矩阵的环境门控
// 验收套件（见 docs/plans/2026-09-04-ob-dialect-matrix-e2e.md）。
//
// 配置（优先级：OS 环境变量 > 仓库根 testdata/db/.local-dev.env，后者 git 忽略）：
//
//	OWL_E2E_OB_ORACLE_DSN   OB Oracle 租户 DSN（oceanbase-oracle:// 或 oracle:// TNS）
//	OWL_E2E_OB_MYSQL_DSN    OB MySQL 租户 DSN（mysql-wire）
//	OWL_E2E_MYSQL_DSN       独立 MySQL DSN
//	OWL_E2E_PG_DSN          PostgreSQL DSN
//	OWL_E2E_DEV_PW          引导新建用户/库的统一口令（须与各 DSN 一致）
//
// 运行：go test -tags e2e -v ./internal/e2edev/
// 依赖实库的连接用例在对应 env 缺失或 ping 失败时 skip（并计入报告），不失败。
package e2edev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envCfg 汇集套件所需连接；口令一律来自环境（凭据不入库）。
type envCfg struct {
	OBOracleDSN string // OB Oracle 租户（sys 引导连接）
	OBMysqlDSN  string // OB MySQL 租户
	MysqlDSN    string // 独立 MySQL
	PgDSN       string // PostgreSQL
	DevPW       string // 引导新建用户/库的统一口令

	// 由租户 DSN 派生的引导参数（可被 *_USER 覆盖变量替换）
	OBOracleUser   string // 如 sys@oratest
	OBOracleTenant string // 如 oratest
	OBOracleHost   string
	OBOraclePort   string
	OBOracleDB     string
}

const dotenvRel = "testdata/db/.local-dev.env"

// moduleRoot 从包目录向上找 go.mod 根，便于定位仓库级 dotenv 与报告目录。
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// parseDotenv 解析 KEY=VALUE / export KEY='VALUE' 行（' 或 " 引号剥除，忽略 # 注释/空行）。
func parseDotenv(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
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
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// lookup 依序取 OS 环境变量 → dotenv。
func lookup(dotenv map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	for _, k := range keys {
		if v := strings.TrimSpace(dotenv[k]); v != "" {
			return v
		}
	}
	return ""
}

// loadEnv 装配环境；某连接缺失时对应字段为空，用例自行 skip。
func loadEnv(t testing.TB) envCfg {
	t.Helper()
	dot := parseDotenv(filepath.Join(moduleRoot(), dotenvRel))
	cfg := envCfg{
		OBOracleDSN: lookup(dot, "OWL_E2E_OB_ORACLE_DSN"),
		OBMysqlDSN:  lookup(dot, "OWL_E2E_OB_MYSQL_DSN"),
		MysqlDSN:    lookup(dot, "OWL_E2E_MYSQL_DSN"),
		PgDSN:       lookup(dot, "OWL_E2E_PG_DSN"),
		DevPW:       lookup(dot, "OWL_E2E_DEV_PW"),
	}
	if cfg.DevPW == "" {
		cfg.DevPW = "" // 仅允许来自环境/dotenv；缺 DSN 的用例会先 skip
	}
	cfg.OBOracleUser = lookup(dot, "OWL_E2E_OB_ORACLE_USER")
	cfg.OBOracleTenant = lookup(dot, "OWL_E2E_OB_ORACLE_TENANT")
	cfg.OBOracleHost = lookup(dot, "OWL_E2E_OB_ORACLE_HOST")
	cfg.OBOraclePort = lookup(dot, "OWL_E2E_OB_ORACLE_PORT")
	cfg.OBOracleDB = lookup(dot, "OWL_E2E_OB_ORACLE_DB")
	if cfg.OBOracleUser == "" {
		cfg.OBOracleUser = "sys@oratest"
	}
	if cfg.OBOracleTenant == "" {
		cfg.OBOracleTenant = "oratest"
	}
	if cfg.OBOracleHost == "" {
		cfg.OBOracleHost = "127.0.0.1"
	}
	if cfg.OBOraclePort == "" {
		cfg.OBOraclePort = "2881"
	}
	if cfg.OBOracleDB == "" {
		cfg.OBOracleDB = cfg.OBOracleTenant
	}
	return cfg
}

// OB Oracle 租户经 mysql-wire 直连不需 db 段（obclient 亦不指定库名）。
func userDSN(user string, e envCfg) string {
	pw := urlEncodePW(e.DevPW)
	return "oceanbase-oracle://" + user + ":" + pw + "@" + e.OBOracleHost + ":" + e.OBOraclePort + "/"
}

func urlEncodePW(pw string) string {
	r := strings.NewReplacer("%", "%25", "@", "%40", "#", "%23", " ", "%20", ":", "%3A", "/", "%2F")
	return r.Replace(pw)
}

func skipNoEnv(t *testing.T, dsn, name string) {
	t.Helper()
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("set %s (or testdata/db/.local-dev.env) to run %s", name, t.Name())
	}
}
