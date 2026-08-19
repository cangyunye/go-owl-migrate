package cdc

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Change is a normalized changelog row captured from a source database.
type Change struct {
	ChgID   int64
	ShardID int64
	OpType  string // I / U / D / T
	OldData []byte // JSON object (null for I/T)
	NewData []byte // JSON object (null for D/T)
	ChgTime time.Time
}

// TargetTable describes the target table shape needed to build replay DML.
type TargetTable struct {
	Table string // qualified target table (e.g. public.emp)

	// Columns and KeyCols are the SOURCE changelog JSON column names.
	// ColumnMap maps a source JSON column name to the target column name; when
	// absent the source name is used as-is. This decouples source/target naming
	// (e.g. Oracle EMPNO vs PG empno).
	Columns     []string          // source columns to write (in priority order)
	KeyCols     []string          // source key columns for WHERE (may be empty)
	ColumnMap   map[string]string // source JSON key -> target column name
	TypeMap     map[string]string // target column -> logical/base type name
	Quoter      func(string) string
	Placeholder func(int) string // maps 1-based ordinal to placeholder token
}

// targetName resolves a source JSON column name to its target column name.
func (t *TargetTable) targetName(src string) string {
	if n, ok := t.ColumnMap[src]; ok {
		return n
	}
	return src
}

func (t *TargetTable) placeholder(i int) string {
	if t.Placeholder != nil {
		return t.Placeholder(i)
	}
	return fmt.Sprintf("$%d", i)
}

func (t *TargetTable) quote(name string) string {
	if t.Quoter != nil {
		return t.Quoter(name)
	}
	return name
}

// parseJSONRow decodes a JSON object into a map.
func parseJSONRow(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode row json: %w", err)
	}
	return m, nil
}

// columnOrder returns the stable union of columns present in the given rows,
// preserving the TargetTable.Columns priority then sorted remainder.
func (t *TargetTable) columnOrder(newVals, oldVals map[string]any) []string {
	seen := make(map[string]bool)
	var order []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
	}
	for _, c := range t.Columns {
		if _, ok := newVals[c]; ok {
			add(c)
		} else if _, ok := oldVals[c]; ok {
			add(c)
		}
	}
	// sorted remainder
	var rest []string
	for k := range newVals {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	for k := range oldVals {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, r := range rest {
		add(r)
	}
	return order
}

// BuildReplaySQL converts a Change into a target INSERT/UPDATE/DELETE statement
// plus its positional arguments. UPDATE/DELETE locate rows by KeyCols; when no
// keys are configured, all old-row columns are used for matching.
func BuildReplaySQL(t *TargetTable, ch Change) (string, []any, error) {
	switch strings.ToUpper(ch.OpType) {
	case "I":
		return buildInsert(t, ch)
	case "U":
		return buildUpdate(t, ch)
	case "D":
		return buildDelete(t, ch)
	case "T":
		return fmt.Sprintf("TRUNCATE TABLE %s", t.Table), nil, nil
	default:
		return "", nil, fmt.Errorf("unknown op_type %q", ch.OpType)
	}
}

// ValueFormatter renders a value as a SQL literal for standalone batch files
// that cannot use bind placeholders.
type ValueFormatter func(col string, v any) string

// DefaultValueFormatter renders common Go values as SQL literals.
func DefaultValueFormatter(_ string, v any) string {
	if v == nil {
		return "NULL"
	}
	switch n := v.(type) {
	case bool:
		if n {
			return "TRUE"
		}
		return "FALSE"
	case string:
		return "'" + strings.ReplaceAll(n, "'", "''") + "'"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// BuildReplayLiterals converts a Change into SQL using literal values (for
// file-batch mode) rather than bind placeholders.
func BuildReplayLiterals(t *TargetTable, ch Change, fmtv ValueFormatter) (string, error) {
	if fmtv == nil {
		fmtv = DefaultValueFormatter
	}
	switch strings.ToUpper(ch.OpType) {
	case "I":
		vals, err := parseJSONRow(ch.NewData)
		if err != nil {
			return "", err
		}
		if len(vals) == 0 {
			return "", fmt.Errorf("insert change has empty new_data")
		}
		order := t.columnOrder(vals, nil)
		cols := make([]string, 0, len(order))
		lits := make([]string, 0, len(order))
		for _, c := range order {
			cols = append(cols, t.quote(t.targetName(c)))
			lits = append(lits, fmtv(t.targetName(c), normalizeValue(vals[c])))
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
			t.Table, strings.Join(cols, ", "), strings.Join(lits, ", ")), nil
	case "U":
		oldVals, err := parseJSONRow(ch.OldData)
		if err != nil {
			return "", err
		}
		newVals, err := parseJSONRow(ch.NewData)
		if err != nil {
			return "", err
		}
		order := t.columnOrder(newVals, oldVals)
		keySet := map[string]bool{}
		for _, k := range t.KeyCols {
			keySet[k] = true
		}
		var sets []string
		for _, c := range order {
			if keySet[c] {
				continue
			}
			if _, ok := newVals[c]; !ok {
				continue
			}
			sets = append(sets, fmt.Sprintf("%s = %s", t.quote(t.targetName(c)), fmtv(t.targetName(c), normalizeValue(newVals[c]))))
		}
		where, err := t.whereLiterals(oldVals, fmtv)
		if err != nil {
			return "", err
		}
		if len(sets) == 0 {
			return "", fmt.Errorf("update has no settable columns")
		}
		return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", t.Table, strings.Join(sets, ", "), where), nil
	case "D":
		oldVals, err := parseJSONRow(ch.OldData)
		if err != nil {
			return "", err
		}
		where, err := t.whereLiterals(oldVals, fmtv)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("DELETE FROM %s WHERE %s;", t.Table, where), nil
	case "T":
		return fmt.Sprintf("TRUNCATE TABLE %s;\n", t.Table), nil
	default:
		return "", fmt.Errorf("unknown op_type %q", ch.OpType)
	}
}

// whereLiterals is the literal-SQL variant of whereClause.
func (t *TargetTable) whereLiterals(vals map[string]any, fmtv ValueFormatter) (string, error) {
	if len(vals) == 0 {
		return "", fmt.Errorf("cannot build WHERE: no row data available")
	}
	keySet := map[string]bool{}
	for _, k := range t.KeyCols {
		keySet[k] = true
	}
	var keys, rest []string
	for k := range vals {
		if keySet[k] {
			keys = append(keys, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Strings(keys)
	sort.Strings(rest)
	order := append(keys, rest...)
	var preds []string
	for _, c := range order {
		v, ok := vals[c]
		if !ok {
			continue
		}
		preds = append(preds, fmt.Sprintf("%s = %s", t.quote(t.targetName(c)), fmtv(t.targetName(c), normalizeValue(v))))
	}
	if len(preds) == 0 {
		return "", fmt.Errorf("cannot build WHERE: no usable columns")
	}
	return strings.Join(preds, " AND "), nil
}

func buildInsert(t *TargetTable, ch Change) (string, []any, error) {
	vals, err := parseJSONRow(ch.NewData)
	if err != nil {
		return "", nil, err
	}
	if len(vals) == 0 {
		return "", nil, fmt.Errorf("insert change has empty new_data")
	}
	order := t.columnOrder(vals, nil)
	cols := make([]string, 0, len(order))
	args := make([]any, 0, len(order))
	ph := make([]string, 0, len(order))
	for i, c := range order {
		cols = append(cols, t.quote(t.targetName(c)))
		v, ok := vals[c]
		if !ok {
			return "", nil, fmt.Errorf("column %q missing in insert data", c)
		}
		args = append(args, normalizeValue(v))
		ph = append(ph, t.placeholder(i+1))
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		t.Table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	return stmt, args, nil
}

func buildUpdate(t *TargetTable, ch Change) (string, []any, error) {
	oldVals, err := parseJSONRow(ch.OldData)
	if err != nil {
		return "", nil, err
	}
	newVals, err := parseJSONRow(ch.NewData)
	if err != nil {
		return "", nil, err
	}
	order := t.columnOrder(newVals, oldVals)

	var sets []string
	var args []any
	// WHERE keys come from old row (pre-image) keys by column name.
	keySet := make(map[string]bool)
	for _, k := range t.KeyCols {
		keySet[k] = true
	}

	idx := 1
	for _, c := range order {
		if keySet[c] {
			continue // keys come last, from old data
		}
		if _, ok := newVals[c]; !ok {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = %s", t.quote(t.targetName(c)), t.placeholder(idx)))
		args = append(args, normalizeValue(newVals[c]))
		idx++
	}

	whereSQL, whereArgs, err := t.whereClause(oldVals, idx)
	if err != nil {
		return "", nil, err
	}
	if len(sets) == 0 {
		return "", nil, fmt.Errorf("update has no settable columns")
	}
	args = append(args, whereArgs...)
	stmt := fmt.Sprintf("UPDATE %s SET %s WHERE %s", t.Table, strings.Join(sets, ", "), whereSQL)
	return stmt, args, nil
}

func buildDelete(t *TargetTable, ch Change) (string, []any, error) {
	oldVals, err := parseJSONRow(ch.OldData)
	if err != nil {
		return "", nil, err
	}
	whereSQL, whereArgs, err := t.whereClause(oldVals, 1)
	if err != nil {
		return "", nil, err
	}
	stmt := fmt.Sprintf("DELETE FROM %s WHERE %s", t.Table, whereSQL)
	return stmt, whereArgs, nil
}

// whereClause builds the WHERE predicates from the given row's values.
// Predicates are ordered: key columns first, then remaining old columns.
func (t *TargetTable) whereClause(vals map[string]any, startIdx int) (string, []any, error) {
	if len(vals) == 0 {
		return "", nil, fmt.Errorf("cannot build WHERE: no row data available")
	}
	// order: keys first, then rest sorted
	keySet := make(map[string]bool)
	for _, k := range t.KeyCols {
		keySet[k] = true
	}
	var keys, rest []string
	for k := range vals {
		if keySet[k] {
			keys = append(keys, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Strings(keys)
	sort.Strings(rest)
	order := append(keys, rest...)

	preds := make([]string, 0, len(order))
	args := make([]any, 0, len(order))
	for i, c := range order {
		v, ok := vals[c]
		if !ok {
			continue
		}
		preds = append(preds, fmt.Sprintf("%s = %s", t.quote(t.targetName(c)), t.placeholder(startIdx+i)))
		args = append(args, normalizeValue(v))
	}
	if len(preds) == 0 {
		return "", nil, fmt.Errorf("cannot build WHERE: no usable columns")
	}
	return strings.Join(preds, " AND "), args, nil
}

// normalizeValue maps JSON-decoded numbers to Go ints/floats suitable for DB drivers.
func normalizeValue(v any) any {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return int64(n)
		}
		return n
	default:
		return v
	}
}
