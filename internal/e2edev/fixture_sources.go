//go:build e2e

package e2edev

import (
	"database/sql"
	"fmt"
	"testing"
)

// ── MySQL / OB-MySQL / PostgreSQL 源种子引导（幂等）──

const srcMySQLDB = "migsrc_mysql"
const srcOBMDB = "migsrc_obm"

// seedMySQLShape 在指定库内建同一套"公司"种子（MySQL/OB-MySQL 通用）：
// 2 表 + PK/FK/索引/视图/函数/触发器/注释 + 数据；类型覆盖 int/varchar/decimal/datetime。
func seedMySQLShape(t *testing.T, db *sql.DB) {
	t.Helper()
	execAll(t, db,
		`CREATE TABLE dept (deptno INT PRIMARY KEY, dname VARCHAR(30) NOT NULL, loc VARCHAR(30)) ENGINE=InnoDB`,
		`CREATE TABLE emp (
			empno INT NOT NULL,
			ename VARCHAR(20) NOT NULL,
			job VARCHAR(20),
			sal DECIMAL(9,2),
			hiredate DATETIME,
			deptno INT,
			PRIMARY KEY (empno),
			CONSTRAINT fk_emp_dept FOREIGN KEY (deptno) REFERENCES dept (deptno)
		) ENGINE=InnoDB`,
		`CREATE INDEX idx_emp_ename ON emp (ename)`,
		`INSERT INTO dept (deptno, dname, loc) VALUES (10, 'ACCOUNTING', 'NEW YORK'), (20, 'RESEARCH', 'DALLAS')`,
		`INSERT INTO emp (empno, ename, job, sal, hiredate, deptno) VALUES
			(7369, 'SMITH', 'CLERK', 800.00, '1980-12-17 00:00:00', 20),
			(7782, 'CLARK', 'MANAGER', 2450.00, '1981-06-09 00:00:00', 10),
			(7839, 'KING', 'PRESIDENT', 5000.00, '1981-11-17 00:00:00', 10)`,
		`CREATE VIEW v_emp AS SELECT empno, ename, sal, deptno FROM emp`,
		`CREATE FUNCTION fn_bonus (p_sal DECIMAL(9,2)) RETURNS DECIMAL(9,2) DETERMINISTIC RETURN p_sal * 0.1`,
		`CREATE TRIGGER trg_emp BEFORE INSERT ON emp FOR EACH ROW SET NEW.sal = COALESCE(NEW.sal, 0)`,
	)
}

// BootstrapMySQLSource 建/重建独立 MySQL 源库与种子（连接 DSN 需无库名或以 / 结尾）。
func BootstrapMySQLSource(t *testing.T, e envCfg) *sql.DB {
	t.Helper()
	db := connect(t, "mysql", e.MysqlDSN)
	execAll(t, db,
		fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", srcMySQLDB),
		fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", srcMySQLDB),
	)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2 := connect(t, "mysql", e.MysqlDSN+srcMySQLDB)
	t.Cleanup(func() { db2.Close() })
	seedMySQLShape(t, db2)
	return db2
}

// BootstrapOBMySQLSource 建/重建 OB-MySQL 租户内源库与种子。
func BootstrapOBMySQLSource(t *testing.T, e envCfg) *sql.DB {
	t.Helper()
	db := connect(t, "oceanbase-mysql", e.OBMysqlDSN)
	execAll(t, db,
		fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", srcOBMDB),
		fmt.Sprintf("CREATE DATABASE `%s` DEFAULT CHARACTER SET utf8mb4", srcOBMDB),
	)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2 := connect(t, "oceanbase-mysql", e.OBMysqlDSN+srcOBMDB)
	t.Cleanup(func() { db2.Close() })
	seedMySQLShape(t, db2)
	return db2
}

// ── PostgreSQL：多用户 schema 专项种子 ──
// 连接用户 superme（须为超管/可建角色）；schema src_hr/src_fin 分别由
// 角色 hr_owner/fin_owner 拥有 —— 模拟"连接用户 ≠ schema 属主"。

const (
	pgRoleHr = "hr_owner"
	pgRoleFn = "fin_owner"
	pgSchHr  = "src_hr"
	pgSchFn  = "src_fin"
)

// BootstrapPGMultiOwner 建角色/属主 schema/表；权限不足（非超管）时 skip。
func BootstrapPGMultiOwner(t *testing.T, e envCfg) *sql.DB {
	t.Helper()
	db := connect(t, "postgres", e.PgDSN)

	var isSuper bool
	if err := db.QueryRow("SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&isSuper); err != nil || !isSuper {
		t.Skipf("pg user %s is not superuser; cannot bootstrap roles", currentUser(t, db))
	}
	execAll(t, db,
		fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgSchHr),
		fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgSchFn),
		fmt.Sprintf(`DROP ROLE IF EXISTS %s`, pgRoleHr),
		fmt.Sprintf(`DROP ROLE IF EXISTS %s`, pgRoleFn),
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, pgRoleHr, e.DevPW),
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, pgRoleFn, e.DevPW),
		fmt.Sprintf(`CREATE SCHEMA %s AUTHORIZATION %s`, pgSchHr, pgRoleHr),
		fmt.Sprintf(`CREATE SCHEMA %s AUTHORIZATION %s`, pgSchFn, pgRoleFn),
	)
	// schema src_hr 内对象：一张由连接用户建（属主 superme），一张以属主角色身份建
	execAll(t, db,
		fmt.Sprintf(`CREATE TABLE %s.dept (deptno INT PRIMARY KEY, dname TEXT NOT NULL, loc TEXT)`, pgSchHr),
		fmt.Sprintf(`CREATE TABLE %s.emp (
			empno INT PRIMARY KEY, ename TEXT NOT NULL, sal NUMERIC(9,2), hiredate TIMESTAMPTZ, deptno INT,
			CONSTRAINT fk_emp_dept FOREIGN KEY (deptno) REFERENCES %s.dept (deptno))`, pgSchHr, pgSchHr),
		fmt.Sprintf(`CREATE INDEX idx_emp_ename ON %s.emp (ename)`, pgSchHr),
		fmt.Sprintf(`INSERT INTO %s.dept VALUES (10, 'ACCOUNTING', 'NEW YORK'), (20, 'RESEARCH', 'DALLAS')`, pgSchHr),
		fmt.Sprintf(`INSERT INTO %s.emp (empno, ename, sal, hiredate, deptno) VALUES
			(7369, 'SMITH', 800.00, '1980-12-17 09:00:00+00', 20),
			(7839, 'KING', 5000.00, '1981-11-17 09:00:00+00', 10)`, pgSchHr),
		fmt.Sprintf(`CREATE VIEW %s.v_emp AS SELECT empno, ename, sal FROM %s.emp`, pgSchHr, pgSchHr),
		fmt.Sprintf(`CREATE SEQUENCE %s.seq_emp START 100`, pgSchHr),
		fmt.Sprintf(`CREATE TABLE %s.ledger (id BIGSERIAL PRIMARY KEY, amount NUMERIC(14,2), note TEXT)`, pgSchFn),
		fmt.Sprintf(`INSERT INTO %s.ledger (amount, note) VALUES (12.34, 'a'), (0.00, 'b')`, pgSchFn),
	)
	return db
}

func currentUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	var u string
	if err := db.QueryRow("SELECT current_user").Scan(&u); err != nil {
		t.Fatalf("current_user: %v", err)
	}
	return u
}
