-- SELECT for SCOTT.EMP
-- Batch size: 5000 | Method: cursor
-- Replace $PAGE_SIZE with 5000
-- Replace $OFFSET with (batch_number * page_size)
SELECT "empno", "ename", "job", "mgr", "hiredate", "sal", "comm", "deptno"
FROM "scott"."emp"
WHERE ("empno" > $LAST_EMPNO);
