-- owl-migrate 验收种子 · PostgreSQL / openGauss 通用
-- 用法：psql -d <db> -f seed_pg.sql（默认 public schema）

CREATE TABLE IF NOT EXISTS owl_acc_dept (
  deptno INT PRIMARY KEY,
  dname  VARCHAR(100)
);

CREATE TABLE IF NOT EXISTS owl_acc_emp (
  empno    INT PRIMARY KEY,
  ename    VARCHAR(100),
  job      VARCHAR(100),
  sal      NUMERIC(12,2),
  hiredate TIMESTAMP
);

DELETE FROM owl_acc_emp;
DELETE FROM owl_acc_dept;

INSERT INTO owl_acc_dept VALUES (10, '研发部'), (20, '市场部'), (30, '财务部');
INSERT INTO owl_acc_emp VALUES
  (1001, '张伟', '工程师', 8500.50, '2024-01-02 09:00:00'),
  (1002, '李娜', '产品经理', 9200.00, '2024-03-15 14:30:00'),
  (1003, NULL, '实习生', NULL, NULL),
  (1004, 'Alex', '顾问', 12345.67, '2023-12-31 23:59:59');
