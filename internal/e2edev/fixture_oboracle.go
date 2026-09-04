//go:build e2e

package e2edev

import (
	"database/sql"
	"fmt"
	"testing"
)

// ── OB Oracle 租户引导：测试用户 + MIGSRC 种子 schema ──
// 口令来自 env（OWL_E2E_DEV_PW），不写入提交文件。

// obOracleUsers 是在 OB Oracle 租户内引导的用户（密码统一 OWL_E2E_DEV_PW）。
var obOracleUsers = []string{"MIGSRC", "MIGTGT", "MIG_MYSQL", "MIG_PG_HR", "MIG_PG_FIN", "MIG_OBM"}

// obUserDSN 构造 OB Oracle 租户内某用户（user@tenant）的连接 DSN。
func obUserDSN(e envCfg, user string) string {
	return userDSN(user+"@"+e.OBOracleTenant, e)
}

// ensureOBUser 幂等创建/重建测试用户：drop-if-exists（容错）→ create → 授权。
// create/grant 失败即 fatal（引导必须可靠）；仅 drop 不存在容错。
func ensureOBUser(t *testing.T, sys *sql.DB, name, pw string) {
	t.Helper()
	drop := fmt.Sprintf(`BEGIN EXECUTE IMMEDIATE 'DROP USER "%s" CASCADE'; EXCEPTION WHEN OTHERS THEN NULL; END;`, name)
	if _, err := sys.Exec(drop); err != nil {
		t.Fatalf("drop user %s: %v", name, err)
	}
	create := fmt.Sprintf(`CREATE USER "%s" IDENTIFIED BY "%s"`, name, pw)
	if _, err := sys.Exec(create); err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	for _, g := range []string{
		fmt.Sprintf(`GRANT CONNECT, RESOURCE TO "%s"`, name),
		fmt.Sprintf(`GRANT CREATE VIEW TO "%s"`, name),
		fmt.Sprintf(`GRANT CREATE SYNONYM TO "%s"`, name),
	} {
		if _, err := sys.Exec(g); err != nil {
			t.Fatalf("grant %s: %v (%s)", name, err, g)
		}
	}
}

// BootstrapOBOracleUsers 确保测试用户存在（MIGSRC 的种子对象见
// ensureMIGSRCFixture，由各用例按需调用）。
func BootstrapOBOracleUsers(t *testing.T, e envCfg) *sql.DB {
	t.Helper()
	sys := connect(t, "oceanbase-oracle", e.OBOracleDSN)
	for _, u := range obOracleUsers {
		ensureOBUser(t, sys, u, e.DevPW)
	}
	return sys
}

// obExecTolerant 记录单条 DDL 失败但不 fatal（用于"能否创建取决于 OB 版本"
// 的对象，如包/同义词；结果由后续探针用例裁决）。
func obExecTolerant(t *testing.T, db *sql.DB, label, sql string) {
	t.Helper()
	if _, err := db.Exec(sql); err != nil {
		t.Logf("tolerant %s: %v", label, err)
	}
}

// EnsureMIGSRCFixture 以 MIGSRC 身份建 SCOTT 风格种子（幂等：用户先重建）。
func EnsureMIGSRCFixture(t *testing.T, e envCfg) *sql.DB {
	t.Helper()
	BootstrapOBOracleUsers(t, e)
	// MIGSRC 用户已重建 → 直接在其连接里建对象
	db := connect(t, "oceanbase-oracle", obUserDSN(e, "MIGSRC"))
	execAll(t, db,
		`CREATE TABLE DEPT (DEPTNO NUMBER(4) PRIMARY KEY, DNAME VARCHAR2(30), LOC VARCHAR2(30))`,
		`CREATE TABLE EMP (EMPNO NUMBER(4) PRIMARY KEY, ENAME VARCHAR2(20) NOT NULL, JOB VARCHAR2(20), SAL NUMBER(9,2), HIREDATE DATE, DEPTNO NUMBER(4))`,
		`ALTER TABLE EMP ADD CONSTRAINT FK_EMP_DEPT FOREIGN KEY (DEPTNO) REFERENCES DEPT (DEPTNO)`,
		`CREATE INDEX IDX_EMP_ENAME ON EMP (ENAME)`,
		`COMMENT ON TABLE EMP IS 'employee master'`,
		`INSERT INTO DEPT (DEPTNO, DNAME, LOC) VALUES (10, 'ACCOUNTING', 'NEW YORK')`,
		`INSERT INTO DEPT (DEPTNO, DNAME, LOC) VALUES (20, 'RESEARCH', 'DALLAS')`,
		`INSERT INTO EMP (EMPNO, ENAME, JOB, SAL, DEPTNO) VALUES (7369, 'SMITH', 'CLERK', 800, 20)`,
		`INSERT INTO EMP (EMPNO, ENAME, JOB, SAL, DEPTNO) VALUES (7782, 'CLARK', 'MANAGER', 2450, 10)`,
		`INSERT INTO EMP (EMPNO, ENAME, JOB, SAL, HIREDATE, DEPTNO) VALUES (7839, 'KING', 'PRESIDENT', 5000, DATE '1981-11-17', 10)`,
		`COMMIT`,
		`CREATE SEQUENCE SEQ_EMP START WITH 100 INCREMENT BY 1 NOCACHE`,
		`CREATE VIEW V_EMP AS SELECT EMPNO, ENAME, SAL, DEPTNO FROM EMP`,
	)
	// 分区表（Phase B3 复核分区重建 clause）
	obExecTolerant(t, db, "partitioned table", `
		CREATE TABLE PART_SALES (SALES_DATE DATE, REGION VARCHAR2(10), AMT NUMBER(12,2))
		PARTITION BY RANGE (SALES_DATE) (
			PARTITION p2024 VALUES LESS THAN (TO_DATE('2025-01-01', 'YYYY-MM-DD')),
			PARTITION p2025 VALUES LESS THAN (TO_DATE('2026-01-01', 'YYYY-MM-DD')))`)
	// 版本敏感对象（能否建取决于 OB 4.4 支持度；失败仅记录，由探针裁决）
	obExecTolerant(t, db, "synonym", `CREATE SYNONYM SYN_EMP FOR EMP`)
	obExecTolerant(t, db, "trigger", `
		CREATE OR REPLACE TRIGGER TRG_EMP
		BEFORE INSERT ON EMP FOR EACH ROW WHEN (NEW.EMPNO IS NULL)
		BEGIN SELECT SEQ_EMP.NEXTVAL INTO :NEW.EMPNO FROM DUAL; END;`)
	obExecTolerant(t, db, "function", `
		CREATE OR REPLACE FUNCTION FN_BONUS (p_sal NUMBER) RETURN NUMBER IS
		BEGIN RETURN p_sal * 0.1; END;`)
	obExecTolerant(t, db, "package", `
		CREATE OR REPLACE PACKAGE PKG_EMP AS
			FUNCTION RAISE_SAL (p NUMBER) RETURN NUMBER;
		END;`)
	obExecTolerant(t, db, "package body", `
		CREATE OR REPLACE PACKAGE BODY PKG_EMP AS
			FUNCTION RAISE_SAL (p NUMBER) RETURN NUMBER IS
			BEGIN RETURN p * 1.1; END;
		END;`)
	return db
}
