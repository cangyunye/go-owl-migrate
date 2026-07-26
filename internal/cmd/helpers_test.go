package cmd

import (
	"path/filepath"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func mustCol(t *testing.T, schema, table, name string, pos int, dataType string) *md.ColumnDef {
	t.Helper()
	col, err := md.NewColumnDef(schema, table, name, pos, dataType)
	if err != nil {
		t.Fatalf("new column: %v", err)
	}
	return col
}

func TestBuildPKMap(t *testing.T) {
	sm := md.NewSchemaModel()
	emp, _ := md.NewTableDef("SCOTT", "EMP")
	emp.AddColumn(mustCol(t, "SCOTT", "EMP", "EMPNO", 1, "NUMBER"))
	emp.AddPrimaryKey("PK_EMP", "EMPNO")
	sm.AddTable(emp)

	bonus, _ := md.NewTableDef("SCOTT", "BONUS")
	bonus.AddColumn(mustCol(t, "SCOTT", "BONUS", "ENAME", 1, "VARCHAR2"))
	sm.AddTable(bonus)

	pkMap := buildPKMap(sm)
	if got := pkMap["SCOTT.EMP"]; len(got) != 1 || got[0] != "EMPNO" {
		t.Errorf("EMP pk = %v, want [EMPNO]", got)
	}
	if _, ok := pkMap["SCOTT.BONUS"]; ok {
		t.Error("BONUS has no PK, should not be in map")
	}
}

func TestNewLogger(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.General.LogLevel = "info"
		cfg.General.LogFormat = "text"
		if newLogger(cfg) == nil {
			t.Error("newLogger returned nil")
		}
	})
	t.Run("json", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.General.LogLevel = "debug"
		cfg.General.LogFormat = "json"
		if newLogger(cfg) == nil {
			t.Error("newLogger returned nil for json")
		}
	})
	t.Run("file output", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.General.LogLevel = "info"
		cfg.General.LogFormat = "text"
		cfg.General.LogFile = filepath.Join(t.TempDir(), "test.log")
		lg := newLogger(cfg)
		if lg == nil {
			t.Fatal("newLogger returned nil for file")
		}
		lg.Info("test message")
		_ = lg.Sync()
	})
	t.Run("invalid format falls back", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.General.LogFormat = "invalid-format"
		if newLogger(cfg) == nil {
			t.Error("newLogger should fall back, not nil")
		}
	})
}
