package cdc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SplitSQLStatements splits a DDL/DML block into individual statements on ";"
// terminators, correctly handling PG/plpgsql dollar-quoted bodies ($$...$$) and
// Oracle-style `;` + `/` statement separators. It is used to apply generated CDC
// DDL one statement at a time via a driver that cannot run multi-statement text.
func SplitSQLStatements(block string) []string {
	var out []string
	var cur strings.Builder
	var inDollar bool
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		if strings.Contains(line, "$$") {
			inDollar = !inDollar
		}
		cur.WriteString(line + "\n")
		t := strings.TrimSpace(line)
		if !inDollar && strings.HasSuffix(t, ";") {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

// ChangelogReader reads normalized changes from a source changelog table by a
// monotonically increasing sequence id. It is dialect-agnostic: the caller
// supplies the quoted qualified table name and placeholder style.
type Poller struct {
	DB          *sql.DB
	Changelog   string // quoted qualified changelog table, e.g. "public"."owl_chg_emp"
	BatchSize   int
	Placeholder func(i int) string // 1-based placeholder token
}

func (p *Poller) placeholder(i int) string {
	if p.Placeholder != nil {
		return p.Placeholder(i)
	}
	return fmt.Sprintf("$%d", i)
}

// PollAfter returns up to BatchSize changes with chg_id > after, ordered by
// chg_id ascending, plus the highest chg_id in the returned batch (0 if empty).
func (p *Poller) PollAfter(ctx context.Context, after int64) ([]Change, int64, error) {
	if p.BatchSize <= 0 {
		p.BatchSize = 500
	}
	q := fmt.Sprintf(
		"SELECT chg_id, shard_id, op_type, old_data, new_data FROM %s WHERE chg_id > %s ORDER BY chg_id ASC LIMIT %s",
		p.Changelog, p.placeholder(1), p.placeholder(2),
	)
	rows, err := p.DB.QueryContext(ctx, q, after, p.BatchSize)
	if err != nil {
		return nil, 0, fmt.Errorf("poll changelog: %w", err)
	}
	defer rows.Close()

	var changes []Change
	var maxID int64
	for rows.Next() {
		var c Change
		var oldData, newData []byte
		if err := rows.Scan(&c.ChgID, &c.ShardID, &c.OpType, &oldData, &newData); err != nil {
			return nil, 0, fmt.Errorf("scan change: %w", err)
		}
		// SQL NULL arrives as nil byte slice.
		c.OldData = cloneBytes(oldData)
		c.NewData = cloneBytes(newData)
		changes = append(changes, c)
		if c.ChgID > maxID {
			maxID = c.ChgID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate changes: %w", err)
	}
	return changes, maxID, nil
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
