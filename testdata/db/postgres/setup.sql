-- PostgreSQL test objects for E2E DDL generation testing

-- Indexes on EMP
CREATE INDEX IF NOT EXISTS idx_emp_ename ON "EMP" ("ENAME");
CREATE INDEX IF NOT EXISTS idx_emp_deptno ON "EMP" ("DEPTNO");
CREATE INDEX IF NOT EXISTS idx_emp_name_job ON "EMP" ("ENAME", "JOB");

-- View
CREATE OR REPLACE VIEW emp_view AS
  SELECT e."EMPNO", e."ENAME", e."JOB", e."SAL", d."DNAME"
  FROM "EMP" e JOIN "DEPT" d ON e."DEPTNO" = d."DEPTNO"
  WHERE e."SAL" > 1000;

-- Sequence
CREATE SEQUENCE IF NOT EXISTS seq_emp_id
  START WITH 1000 INCREMENT BY 1 MINVALUE 1 MAXVALUE 999999999 NO CYCLE CACHE 20;

-- Function
CREATE OR REPLACE FUNCTION get_emp_count()
RETURNS integer AS $$
DECLARE
  v_count integer;
BEGIN
  SELECT COUNT(*) INTO v_count FROM "EMP";
  RETURN v_count;
END;
$$ LANGUAGE plpgsql;

-- Trigger function and trigger
CREATE OR REPLACE FUNCTION trg_emp_sal_func()
RETURNS trigger AS $$
BEGIN
  IF NEW."SAL" < 0 THEN
    NEW."SAL" := 0;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_emp_sal ON "EMP";
CREATE TRIGGER trg_emp_sal
  BEFORE INSERT ON "EMP"
  FOR EACH ROW
  EXECUTE FUNCTION trg_emp_sal_func();
