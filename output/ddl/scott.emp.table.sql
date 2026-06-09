CREATE TABLE IF NOT EXISTS "public"."emp" (
  "empno" NUMBER NOT NULL,
  "ename" VARCHAR2,
  "job" VARCHAR2,
  "mgr" NUMBER,
  "hiredate" DATE,
  "sal" NUMBER,
  "comm" NUMBER,
  "deptno" NUMBER NOT NULL
);
COMMENT ON TABLE "public"."EMP" IS 'Employee table'
