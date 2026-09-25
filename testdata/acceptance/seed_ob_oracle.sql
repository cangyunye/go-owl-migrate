-- owl-migrate 验收种子 · OceanBase-Oracle 租户（在 MIGSRC 用户下执行）
-- 用法：obclient -h127.0.0.1 -P2881 -uMIGSRC@oratest -p*** -D MIGSRC -f seed_ob_oracle.sql
-- 注意：OB-Oracle DML 不自动提交，文件末尾已 COMMIT。

CREATE TABLE owl_acc_dept (
  deptno NUMBER(10) PRIMARY KEY,
  dname  VARCHAR2(200)
);

CREATE TABLE owl_acc_emp (
  empno    NUMBER(10) PRIMARY KEY,
  ename    VARCHAR2(200),
  job      VARCHAR2(200),
  sal      NUMBER(12,2),
  hiredate DATE
);

INSERT INTO owl_acc_dept VALUES (10, '研发部');
INSERT INTO owl_acc_dept VALUES (20, '市场部');
INSERT INTO owl_acc_dept VALUES (30, '财务部');
INSERT INTO owl_acc_emp VALUES (1001, '张伟', '工程师', 8500.50, TO_DATE('2024-01-02 09:00:00','YYYY-MM-DD HH24:MI:SS'));
INSERT INTO owl_acc_emp VALUES (1002, '李娜', '产品经理', 9200.00, TO_DATE('2024-03-15 14:30:00','YYYY-MM-DD HH24:MI:SS'));
INSERT INTO owl_acc_emp VALUES (1003, NULL, '实习生', NULL, NULL);
INSERT INTO owl_acc_emp VALUES (1004, 'Alex', '顾问', 12345.67, TO_DATE('2023-12-31 23:59:59','YYYY-MM-DD HH24:MI:SS'));
COMMIT;
