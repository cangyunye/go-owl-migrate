-- SCOTT 种子脚本（用于 gvenzl/oracle-free，PDB=XEPDB1）
-- 运行方式（以 SYS 身份连到 XEPDB1）：
--   docker exec -i oracle sqlplus -s sys/Oracle123!@//localhost:1521/XEPDB1 as sysdba < testdata/db/oracle/scott_seed.sql
--
-- 内容：创建 SCOTT 用户 + DEPT/EMP 表与数据 + 视图/序列/同义词/触发器/函数/包

-- ── 1. 以 SYS 身份创建用户并授权 ──
CREATE USER scott IDENTIFIED BY tiger;
GRANT DBA TO scott;

-- ── 2. 切换到 SCOTT 建对象 ──
CONNECT scott/tiger@//localhost:1521/XEPDB1

-- 表
CREATE TABLE dept (
  deptno NUMBER(2) PRIMARY KEY,
  dname  VARCHAR2(14),
  loc    VARCHAR2(13)
);

CREATE TABLE emp (
  empno    NUMBER(4) PRIMARY KEY,
  ename    VARCHAR2(10),
  job      VARCHAR2(9),
  mgr      NUMBER(4),
  hiredate DATE,
  sal      NUMBER(7,2),
  comm     NUMBER(7,2),
  deptno   NUMBER(2) REFERENCES dept(deptno)
);

-- 数据：DEPT
INSERT INTO dept VALUES (10, 'ACCOUNTING', 'NEW YORK');
INSERT INTO dept VALUES (20, 'RESEARCH',   'DALLAS');
INSERT INTO dept VALUES (30, 'SALES',      'CHICAGO');
INSERT INTO dept VALUES (40, 'OPERATIONS', 'BOSTON');

-- 数据：EMP（14 行）
INSERT INTO emp VALUES (7369, 'SMITH',  'CLERK',     7902, TO_DATE('1980-12-17','YYYY-MM-DD'), 800,  NULL, 20);
INSERT INTO emp VALUES (7499, 'ALLEN',  'SALESMAN',  7698, TO_DATE('1981-02-20','YYYY-MM-DD'), 1600, 300,  30);
INSERT INTO emp VALUES (7521, 'WARD',   'SALESMAN',  7698, TO_DATE('1981-02-22','YYYY-MM-DD'), 1250, 500,  30);
INSERT INTO emp VALUES (7566, 'JONES',  'MANAGER',   7839, TO_DATE('1981-04-02','YYYY-MM-DD'), 2975, NULL, 20);
INSERT INTO emp VALUES (7654, 'MARTIN', 'SALESMAN',  7698, TO_DATE('1981-09-28','YYYY-MM-DD'), 1250, 1400, 30);
INSERT INTO emp VALUES (7698, 'BLAKE',  'MANAGER',   7839, TO_DATE('1981-05-01','YYYY-MM-DD'), 2850, NULL, 30);
INSERT INTO emp VALUES (7782, 'CLARK',  'MANAGER',   7839, TO_DATE('1981-06-09','YYYY-MM-DD'), 2450, NULL, 10);
INSERT INTO emp VALUES (7788, 'SCOTT',  'ANALYST',   7566, TO_DATE('1987-04-19','YYYY-MM-DD'), 3000, NULL, 20);
INSERT INTO emp VALUES (7839, 'KING',   'PRESIDENT', NULL, TO_DATE('1981-11-17','YYYY-MM-DD'), 5000, NULL, 10);
INSERT INTO emp VALUES (7844, 'TURNER', 'SALESMAN',  7698, TO_DATE('1981-09-08','YYYY-MM-DD'), 1500, 0,    30);
INSERT INTO emp VALUES (7876, 'ADAMS',  'CLERK',     7788, TO_DATE('1987-05-23','YYYY-MM-DD'), 1100, NULL, 20);
INSERT INTO emp VALUES (7900, 'JAMES',  'CLERK',     7698, TO_DATE('1981-12-03','YYYY-MM-DD'), 950,  NULL, 30);
INSERT INTO emp VALUES (7902, 'FORD',   'ANALYST',   7566, TO_DATE('1981-12-03','YYYY-MM-DD'), 3000, NULL, 20);
INSERT INTO emp VALUES (7934, 'MILLER', 'CLERK',     7782, TO_DATE('1982-01-23','YYYY-MM-DD'), 1300, NULL, 10);

-- 索引
CREATE INDEX idx_emp_ename   ON emp (ename);
CREATE INDEX idx_emp_deptno  ON emp (deptno);
CREATE INDEX idx_emp_namejob ON emp (ename, job);

-- 视图
CREATE OR REPLACE VIEW emp_view AS
  SELECT e.empno, e.ename, e.job, e.sal, d.dname
  FROM emp e JOIN dept d ON e.deptno = d.deptno
  WHERE e.sal > 1000;

-- 序列
CREATE SEQUENCE seq_emp_id
  START WITH 1000 INCREMENT BY 1 MINVALUE 1 MAXVALUE 999999999 NOCYCLE CACHE 20;

-- 同义词
CREATE OR REPLACE SYNONYM emp_syn FOR scott.emp;

-- 触发器
CREATE OR REPLACE TRIGGER trg_emp_sal
  BEFORE INSERT ON emp
  FOR EACH ROW
BEGIN
  IF :NEW.sal < 0 THEN
    :NEW.sal := 0;
  END IF;
END trg_emp_sal;
/

-- 函数
CREATE OR REPLACE FUNCTION get_emp_count RETURN NUMBER AS
  v_count NUMBER;
BEGIN
  SELECT COUNT(*) INTO v_count FROM emp;
  RETURN v_count;
END get_emp_count;
/

-- 包规范
CREATE OR REPLACE PACKAGE pkg_emp AS
  PROCEDURE get_emp(p_id IN NUMBER);
  FUNCTION get_count RETURN NUMBER;
END pkg_emp;
/

-- 包体
CREATE OR REPLACE PACKAGE BODY pkg_emp AS
  PROCEDURE get_emp(p_id IN NUMBER) IS
  BEGIN
    NULL;
  END get_emp;
  FUNCTION get_count RETURN NUMBER IS
    v_count NUMBER;
  BEGIN
    SELECT COUNT(*) INTO v_count FROM emp;
    RETURN v_count;
  END get_count;
END pkg_emp;
/

COMMIT;
EXIT;
