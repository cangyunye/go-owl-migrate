package cdc

import (
	"encoding/json"
	"fmt"
	"testing"
)

// buildTargetDesc returns a small target table descriptor for the builder.
func buildTargetDesc(columns []string, keyCols []string, types map[string]string) *TargetTable {
	return &TargetTable{
		Table:       "public.emp",
		Columns:     columns,
		KeyCols:     keyCols,
		TypeMap:     types,
		Quoter:      func(name string) string { return `"` + name + `"` },
		Placeholder: func(i int) string { return fmt.Sprintf("$%d", i) },
	}
}

func jsonRow(kv map[string]any) []byte {
	b, _ := json.Marshal(kv)
	return b
}

func TestReplaySQL_Insert(t *testing.T) {
	tt := buildTargetDesc(
		[]string{"empno", "ename", "sal"},
		[]string{"empno"},
		map[string]string{"empno": "numeric", "ename": "varchar", "sal": "numeric"},
	)
	ch := Change{OpType: "I", NewData: jsonRow(map[string]any{"empno": float64(1), "ename": "ADAMS", "sal": float64(100)})}
	stmt, args, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	wantContain := []string{
		`INSERT INTO public.emp ("empno", "ename", "sal") VALUES ($1, $2, $3)`,
	}
	for _, w := range wantContain {
		if stmt != w {
			t.Errorf("got:\n%s\nwant:\n%s", stmt, w)
		}
	}
	if len(args) != 3 {
		t.Errorf("args = %v, want 3", args)
	}
}

func TestReplaySQL_UpdateByKey(t *testing.T) {
	tt := buildTargetDesc(
		[]string{"empno", "ename", "sal"},
		[]string{"empno"},
		map[string]string{"empno": "numeric", "ename": "varchar", "sal": "numeric"},
	)
	ch := Change{
		OpType:  "U",
		OldData: jsonRow(map[string]any{"empno": float64(1)}),
		NewData: jsonRow(map[string]any{"empno": float64(1), "ename": "ADAMS", "sal": float64(200)}),
	}
	stmt, args, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	want := `UPDATE public.emp SET "ename" = $1, "sal" = $2 WHERE "empno" = $3`
	if stmt != want {
		t.Errorf("got:\n%s\nwant:\n%s", stmt, want)
	}
	if len(args) != 3 {
		t.Errorf("args = %v, want 3", args)
	}
}

func TestReplaySQL_DeleteByKey(t *testing.T) {
	tt := buildTargetDesc(
		[]string{"empno", "ename", "sal"},
		[]string{"empno"},
		map[string]string{"empno": "numeric"},
	)
	ch := Change{OpType: "D", OldData: jsonRow(map[string]any{"empno": float64(1)})}
	stmt, args, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	want := `DELETE FROM public.emp WHERE "empno" = $1`
	if stmt != want {
		t.Errorf("got:\n%s\nwant:\n%s", stmt, want)
	}
	if len(args) != 1 {
		t.Errorf("args = %v, want 1", args)
	}
}

func TestReplaySQL_ColumnMapping(t *testing.T) {
	tt := &TargetTable{
		Table:       "public.emp",
		Columns:     []string{"EMPNO", "ENAME"},
		KeyCols:     []string{"EMPNO"},
		ColumnMap:   map[string]string{"EMPNO": "empno", "ENAME": "ename"},
		Quoter:      func(n string) string { return `"` + n + `"` },
		Placeholder: func(i int) string { return fmt.Sprintf("$%d", i) },
	}
	ch := Change{OpType: "I", NewData: jsonRow(map[string]any{"EMPNO": float64(9), "ENAME": "X"})}
	stmt, _, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	if !contains(stmt, `INSERT INTO public.emp ("empno", "ename") VALUES ($1, $2)`) {
		t.Errorf("column mapping not applied: %s", stmt)
	}
}

func TestReplaySQL_Truncate(t *testing.T) {
	tt := buildTargetDesc([]string{"a"}, []string{"a"}, map[string]string{"a": "numeric"})
	stmt, args, err := BuildReplaySQL(tt, Change{OpType: "T"})
	if err != nil {
		t.Fatalf("BuildReplaySQL truncate: %v", err)
	}
	if stmt != "TRUNCATE TABLE public.emp" {
		t.Errorf("truncate stmt = %q", stmt)
	}
	if len(args) != 0 {
		t.Errorf("truncate args = %v, want empty", args)
	}
}

func TestReplaySQL_NoKeyUsesAllOldColumns(t *testing.T) {
	tt := buildTargetDesc(
		[]string{"empno", "ename"},
		nil, // no key
		map[string]string{"empno": "numeric", "ename": "varchar"},
	)
	ch := Change{OpType: "D", OldData: jsonRow(map[string]any{"empno": float64(1), "ename": "ADAMS"})}
	stmt, _, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	// Both columns used for matching (no key).
	if !contains(stmt, `WHERE "empno" = $1 AND "ename" IS NOT DISTINCT FROM $2`) && !contains(stmt, `"ename" = $2`) {
		t.Errorf("no-key delete should match on all old columns:\n%s", stmt)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestReplaySQL_ColumnPriorityRespected(t *testing.T) {
	tt := buildTargetDesc(
		[]string{"empno", "ename", "sal"},
		[]string{"empno"},
		map[string]string{"empno": "numeric", "ename": "varchar", "sal": "varchar"},
	)
	// JSON object keys arrive unordered; builder must emit descriptor order.
	ch := Change{OpType: "I", NewData: jsonRow(map[string]any{"sal": "y", "empno": float64(2), "ename": "x"})}
	stmt, _, err := BuildReplaySQL(tt, ch)
	if err != nil {
		t.Fatalf("BuildReplaySQL: %v", err)
	}
	if !contains(stmt, `("empno", "ename", "sal")`) {
		t.Errorf("columns should follow descriptor priority order: %s", stmt)
	}
}
