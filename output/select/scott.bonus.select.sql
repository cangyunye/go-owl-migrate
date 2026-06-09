-- SELECT for SCOTT.BONUS
-- Batch size: 5000 | Method: cursor
-- Replace $PAGE_SIZE with 5000
-- Replace $OFFSET with (batch_number * page_size)
SELECT "ename", "job", "sal", "comm"
FROM "scott"."bonus"
-- LIMIT $PAGE_SIZE OFFSET $OFFSET;
