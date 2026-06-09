-- SELECT for SCOTT.DEPT
-- Batch size: 5000 | Method: cursor
-- Replace $PAGE_SIZE with 5000
-- Replace $OFFSET with (batch_number * page_size)
SELECT "deptno", "dname", "loc"
FROM "scott"."dept"
WHERE ("deptno" > $LAST_DEPTNO);
