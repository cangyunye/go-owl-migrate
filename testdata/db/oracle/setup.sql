-- Oracle SCOTT schema test objects for E2E DDL generation testing
-- SCOTT already has EMP and DEPT tables; add supplementary objects.

-- View
CREATE OR REPLACE VIEW emp_view AS
  SELECT e.empno, e.ename, e.job, e.sal, d.dname
  FROM emp e JOIN dept d ON e.deptno = d.deptno
  WHERE e.sal > 1000;

-- Sequence
CREATE SEQUENCE seq_emp_id
  START WITH 1000 INCREMENT BY 1 MINVALUE 1 MAXVALUE 999999999 NOCYCLE CACHE 20;

-- Synonym
CREATE OR REPLACE SYNONYM emp_syn FOR scott.emp;

-- Trigger
CREATE OR REPLACE TRIGGER trg_emp_sal
  BEFORE INSERT ON emp
  FOR EACH ROW
BEGIN
  IF :NEW.sal < 0 THEN
    :NEW.sal := 0;
  END IF;
END trg_emp_sal;
/

-- Function
CREATE OR REPLACE FUNCTION get_emp_count RETURN NUMBER AS
  v_count NUMBER;
BEGIN
  SELECT COUNT(*) INTO v_count FROM emp;
  RETURN v_count;
END get_emp_count;
/

-- Package specification
CREATE OR REPLACE PACKAGE pkg_emp AS
  PROCEDURE get_emp(p_id IN NUMBER);
  FUNCTION get_count RETURN NUMBER;
END pkg_emp;
/

-- Package body
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

EXIT;
