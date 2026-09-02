//go:build e2e

// Package e2eob is an environment-driven acceptance test for the
// OceanBase-Oracle migration feature set. It connects to a live OceanBase
// Oracle-compatible tenant, exercises connection resolution, metadata
// extraction, type mapping, DDL generation and data reads, and writes a
// machine-readable report to output/e2e/oboracle_report.json.
//
// Configuration (environment variables):
//
//	OWL_E2E_OB_DSN     OceanBase Oracle tenant DSN. Either the MySQL-wire form
//	                   oboracle://user@tenant#cluster:pass@host:2883/db (or the
//	                   oceanbase-oracle:// spelling), or the TNS form
//	                   oracle://user:pass@host:port/service.
//	OWL_E2E_OB_SCHEMA   Owner/schema to extract (e.g. SCOTT).
//
// Run:
//
//	go test -tags e2e -v ./internal/e2eob/
package e2eob

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/helingjun/obconnector-go" // registers "oboracle"/"oceanbase" drivers
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oceanbase"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

const (
	dsnEnv    = "OWL_E2E_OB_DSN"
	schemaEnv = "OWL_E2E_OB_SCHEMA"
	// reportEnv overrides the report path (full or CWD-relative).
	reportEnv = "OWL_E2E_REPORT"
)

func reportPath() string {
	if p := strings.TrimSpace(os.Getenv(reportEnv)); p != "" {
		return p
	}
	return filepath.Join(moduleRoot(), "output", "e2e", "oboracle_report.json")
}

// moduleRoot walks up from the package directory to the go.mod root, so the
// report lands in <repo>/output/e2e/ regardless of where `go test` runs.
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

type checkResult struct {
	Check   string `json:"check"`
	Status  string `json:"status"` // pass | fail | skip
	Detail  string `json:"detail,omitempty"`
	Elapsed string `json:"elapsed"`
}

type report struct {
	DBType    string        `json:"db_type"`
	DSN       string        `json:"dsn"`
	Schema    string        `json:"schema"`
	Generated string        `json:"generated_at"`
	Summary   reportSummary `json:"summary"`
	Results   []checkResult `json:"results"`
}

type reportSummary struct {
	Pass int `json:"pass"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// TestOceanBaseOracleReport runs the full feature matrix and always writes the
// JSON report. DB-dependent checks are skipped when the env vars are absent,
// so the report still records the dialect/DDL checks in that case.
func TestOceanBaseOracleReport(t *testing.T) {
	rep := &report{
		DBType:    "oceanbase-oracle",
		Schema:    os.Getenv(schemaEnv),
		Generated: time.Now().Format(time.RFC3339),
	}
	rep.DSN = config.MaskDSN(os.Getenv(dsnEnv))

	// Open the connection once (validates driver/DSN resolution) and share it.
	var (
		db     *sql.DB
		schema = strings.TrimSpace(os.Getenv(schemaEnv))
	)
	cfg := config.DBConfig{Type: "oceanbase-oracle", DSN: os.Getenv(dsnEnv), ConnectTimeout: "30s"}
	wire := dbconn.OceanBaseOracleUsesMySQLWire(cfg)

	if cfg.DSN != "" && schema != "" {
		var err error
		db, err = dbconn.Open(cfg)
		if err != nil {
			rep.add(checkResult{Check: "connect", Status: "fail", Detail: err.Error()})
			db = nil // extraction checks below will each skip on nil db
		} else {
			t.Cleanup(func() { db.Close() })
			rep.add(checkResult{Check: "connect", Status: "pass",
				Detail: fmt.Sprintf("wire=%s", wireName(wire))})
		}
	} else {
		rep.add(checkResult{Check: "connect", Status: "skip",
			Detail: fmt.Sprintf("set %s and %s to connect", dsnEnv, schemaEnv)})
	}

	// ── 1. Compatibility-mode probe (MySQL wire only) ──
	t.Run("compat_mode", func(t *testing.T) {
		if db == nil {
			rep.skip(t, "compat_mode", "no connection")
			return
		}
		if !wire {
			rep.skip(t, "compat_mode", "probe only available over MySQL wire")
			return
		}
		rep.check(t, "compat_mode", func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			mode := dbconn.ProbeOceanBaseCompatMode(ctx, db)
			if mode == "" {
				return "", fmt.Errorf("could not detect ob_compatibility_mode")
			}
			if mode != "oracle" {
				return "", fmt.Errorf("compat mode = %q, want oracle", mode)
			}
			return "tenant compat mode = oracle", nil
		})
	})

	// ── 2. Metadata extraction ──
	var sm *md.SchemaModel
	t.Run("extract", func(t *testing.T) {
		if db == nil {
			rep.skip(t, "extract", "no connection")
			return
		}
		rep.check(t, "extract", func() (string, error) {
			var err error
			sm, err = extractor.Extract(db, "oceanbase-oracle-wire", schema)
			if err != nil {
				return "", fmt.Errorf("extract %s: %w", schema, err)
			}
			tables := sm.GetTables()
			if len(tables) == 0 {
				return "", fmt.Errorf("schema %s has no tables", schema)
			}
			for _, tbl := range tables {
				if len(tbl.GetColumns()) == 0 {
					return "", fmt.Errorf("table %q extracted with no columns", tbl.TableName)
				}
			}
			return fmt.Sprintf("%d tables extracted", len(tables)), nil
		})
	})

	t.Run("columns", func(t *testing.T) {
		if sm == nil {
			rep.skip(t, "columns", "extract did not run")
			return
		}
		rep.check(t, "columns", func() (string, error) {
			tbl := sm.GetTables()[0]
			cols := tbl.GetColumns()
			for _, c := range cols {
				if c.ColumnName == "" || c.DataType == "" {
					return "", fmt.Errorf("table %q column %q has empty name/type", tbl.TableName, c.ColumnName)
				}
			}
			return fmt.Sprintf("%s: %d columns (first=%s %s)", tbl.TableName, len(cols),
				cols[0].ColumnName, cols[0].DataType), nil
		})
	})

	t.Run("keys_indexes", func(t *testing.T) {
		if sm == nil {
			rep.skip(t, "keys_indexes", "extract did not run")
			return
		}
		rep.check(t, "keys_indexes", func() (string, error) {
			var pk, fk, ix int
			for _, tbl := range sm.GetTables() {
				pk += len(tbl.GetPrimaryKeys())
				fk += len(sm.GetForeignKeys(schema, tbl.TableName))
				ix += len(sm.GetIndexes(schema, tbl.TableName))
			}
			if pk == 0 {
				return "", fmt.Errorf("no primary keys found across %d tables", len(sm.GetTables()))
			}
			return fmt.Sprintf("pk=%d fk=%d indexes=%d", pk, fk, ix), nil
		})
	})

	t.Run("objects", func(t *testing.T) {
		if sm == nil {
			rep.skip(t, "objects", "extract did not run")
			return
		}
		rep.check(t, "objects", func() (string, error) {
			trig := 0
			for _, tbl := range sm.GetTables() {
				trig += len(sm.GetTriggers(schema, tbl.TableName))
			}
			return fmt.Sprintf("views=%d seq=%d synonym=%d trigger=%d func=%d pkg=%d",
				len(sm.GetViews()), len(sm.GetSequences(schema)), len(sm.GetSynonyms(schema)),
				trig, len(sm.GetFunctions(schema)), len(sm.GetPackages(schema))), nil
		})
	})

	// ── 3. Data read over the "?"-placeholder Oracle wire ──
	t.Run("data_read", func(t *testing.T) {
		if db == nil || sm == nil {
			rep.skip(t, "data_read", "no connection/extract")
			return
		}
		rep.check(t, "data_read", func() (string, error) {
			tbl := sm.GetTables()[0].TableName
			q := fmt.Sprintf("SELECT * FROM %s.%s WHERE ROWNUM <= ?", schema, tbl)
			rows, err := db.Query(q, 1)
			if err != nil {
				return "", fmt.Errorf("bound SELECT (%q): %v", q, err)
			}
			defer rows.Close()
			if !rows.Next() {
				return "", fmt.Errorf("bound SELECT on %s returned no rows", tbl)
			}
			return fmt.Sprintf("bound SELECT on %s ok", tbl), nil
		})
	})

	// ── 4. Dialect / type mapping / DDL (no DB required) ──
	ob := oceanbase.NewOracle()

	t.Run("type_mapping", func(t *testing.T) {
		rep.check(t, "type_mapping", func() (string, error) {
			cases := []struct {
				raw           string
				len, prec, sc int
				wantBase      dialect.LogicalBase
				wantTarget    string
			}{
				{"BFILE", 0, 0, 0, dialect.LBBLOB, "BLOB"}, // OB-Oracle: BFILE -> BLOB (overrides Oracle's RAW mapping)
				{"NUMBER", 0, 7, 2, dialect.LBNumeric, ""},
				{"NUMBER", 0, 4, 0, dialect.LBSmallInt, ""},
				{"VARCHAR2", 14, 0, 0, dialect.LBVarchar, ""},
				{"DATE", 0, 0, 0, dialect.LBDatetime, ""}, // Oracle DATE carries time
				{"CLOB", 0, 0, 0, dialect.LBCLOB, ""},
			}
			var bad []string
			for _, c := range cases {
				lt := ob.ToLogicalType(c.raw, c.len, c.prec, c.sc)
				if lt.Base != c.wantBase {
					bad = append(bad, fmt.Sprintf("%s -> %s (want %s)", c.raw, lt.Base, c.wantBase))
				}
				if c.wantTarget != "" && ob.FromLogicalType(lt) != c.wantTarget {
					bad = append(bad, fmt.Sprintf("%s target %q (want %q)", c.raw, ob.FromLogicalType(lt), c.wantTarget))
				}
			}
			if len(bad) > 0 {
				return "", fmt.Errorf("%s", strings.Join(bad, "; "))
			}
			return "BFILE->BLOB, NUMBER, VARCHAR2, DATE, CLOB mapped", nil
		})
	})

	t.Run("ddl_table", func(t *testing.T) {
		rep.check(t, "ddl_table", func() (string, error) {
			tbl, _ := md.NewTableDef("SCOTT", "EMP")
			c1, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
			c1.DataPrecision = 4
			tbl.AddColumn(c1)
			c2, _ := md.NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
			c2.DataLength = 10
			tbl.AddColumn(c2)
			c3, _ := md.NewColumnDef("SCOTT", "EMP", "HIREDATE", 3, "DATE")
			tbl.AddColumn(c3)
			sql, err := ob.BuildCreateTable(tbl, dialect.BuildOptions{SkipPartitions: true})
			if err != nil {
				return "", err
			}
			if !strings.Contains(sql, "CREATE TABLE") || !strings.Contains(sql, "EMPNO") {
				return "", fmt.Errorf("unexpected DDL:\n%s", sql)
			}
			return "CREATE TABLE generated", nil
		})
	})

	t.Run("ddl_bitmap_index", func(t *testing.T) {
		rep.check(t, "ddl_bitmap_index", func() (string, error) {
			idx := &md.IndexDef{TableSchema: "SCOTT", TableName: "EMP",
				IndexName: "IDX_BIT", ColumnName: "DEPTNO", IndexType: "BITMAP"}
			sql, err := ob.BuildCreateIndex([]*md.IndexDef{idx}, dialect.BuildOptions{})
			if err != nil {
				return "", err
			}
			if !strings.Contains(sql, "-- MANUAL") {
				return "", fmt.Errorf("Bitmap index should downgrade to MANUAL comment, got:\n%s", sql)
			}
			return "Bitmap index -> MANUAL comment", nil
		})
	})

	t.Run("ddl_sequence", func(t *testing.T) {
		rep.check(t, "ddl_sequence", func() (string, error) {
			seq := &md.SequenceDef{SequenceSchema: "SCOTT", SequenceName: "SEQ_EMP_ID",
				StartValue: 1000, IncrementBy: 1, MaxValue: 999999999, CacheSize: 20}
			sql, err := ob.BuildCreateSequence(seq, dialect.BuildOptions{})
			if err != nil {
				return "", err
			}
			if !strings.Contains(sql, "CREATE SEQUENCE") || !strings.Contains(sql, "SEQ_EMP_ID") {
				return "", fmt.Errorf("unexpected sequence DDL:\n%s", sql)
			}
			return "CREATE SEQUENCE generated", nil
		})
	})

	t.Run("feature_truncate_txn", func(t *testing.T) {
		rep.check(t, "feature_truncate_txn", func() (string, error) {
			if !ob.TruncateIsTransactional() {
				return "", fmt.Errorf("OceanBase Oracle TRUNCATE must be transactional")
			}
			return "TRUNCATE is transactional (OB behavior)", nil
		})
	})

	rep.write(t)
}

// ── helpers ──

func wireName(wire bool) string {
	if wire {
		return "mysql-wire (oboracle)"
	}
	return "tns (go-ora)"
}

func (r *report) add(c checkResult) { r.Results = append(r.Results, c) }

func (r *report) check(t *testing.T, name string, fn func() (string, error)) {
	t.Helper()
	start := time.Now()
	detail, err := fn()
	c := checkResult{Check: name, Elapsed: time.Since(start).Round(time.Millisecond).String()}
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		t.Errorf("%s: %v", name, err)
	} else {
		c.Status = "pass"
		c.Detail = detail
	}
	r.add(c)
}

func (r *report) skip(t *testing.T, name, reason string) {
	t.Helper()
	r.add(checkResult{Check: name, Status: "skip", Detail: reason})
}

func (r *report) write(t *testing.T) {
	for _, c := range r.Results {
		switch c.Status {
		case "pass":
			r.Summary.Pass++
		case "fail":
			r.Summary.Fail++
		default:
			r.Summary.Skip++
		}
	}
	rp := reportPath()
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		t.Errorf("mkdir %s: %v", filepath.Dir(rp), err)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Errorf("marshal report: %v", err)
		return
	}
	if err := os.WriteFile(rp, b, 0o644); err != nil {
		t.Errorf("write report: %v", err)
		return
	}
	abs, _ := filepath.Abs(rp)
	t.Logf("report: %s (pass=%d fail=%d skip=%d)", abs, r.Summary.Pass, r.Summary.Fail, r.Summary.Skip)
	if r.Summary.Fail > 0 {
		t.Fatalf("%d check(s) failed", r.Summary.Fail)
	}
}
