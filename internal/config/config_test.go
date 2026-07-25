package config

import (
	"os"
	"testing"
)

func TestTableFilter_Match(t *testing.T) {
	tests := []struct {
		name   string
		filter TableFilterConfig
		schema string
		table  string
		want   bool
	}{
		{
			name:   "wildcard include matches everything",
			filter: TableFilterConfig{Include: []string{"*"}},
			schema: "SCOTT", table: "EMP",
			want: true,
		},
		{
			name:   "exact include matches",
			filter: TableFilterConfig{Include: []string{"SCOTT.EMP"}},
			schema: "SCOTT", table: "EMP",
			want: true,
		},
		{
			name:   "exact include no match",
			filter: TableFilterConfig{Include: []string{"HR.DEPT"}},
			schema: "SCOTT", table: "EMP",
			want: false,
		},
		{
			name: "glob exclude matches",
			filter: TableFilterConfig{
				Include: []string{"*"},
				Exclude: TableExcludeConfig{Glob: []string{"*_LOG"}},
			},
			schema: "SCOTT", table: "ERR_LOG",
			want: false,
		},
		{
			name: "glob exclude no match",
			filter: TableFilterConfig{
				Include: []string{"*"},
				Exclude: TableExcludeConfig{Glob: []string{"*_LOG"}},
			},
			schema: "SCOTT", table: "EMP",
			want: true,
		},
		{
			name: "regex exclude matches Oracle recycle bin",
			filter: TableFilterConfig{
				Include: []string{"*"},
				Exclude: TableExcludeConfig{Regex: []string{`^BIN\$`}},
			},
			schema: "SCOTT", table: "BIN$abc123",
			want: false,
		},
		{
			name: "schema exclude",
			filter: TableFilterConfig{
				Include: []string{"*"},
				Exclude: TableExcludeConfig{Schemas: []string{"SYS", "SYSTEM"}},
			},
			schema: "SYS", table: "SOME_TABLE",
			want: false,
		},
		{
			name: "exact table exclude",
			filter: TableFilterConfig{
				Include: []string{"*"},
				Exclude: TableExcludeConfig{Tables: []string{"SCOTT.TEMP_DATA"}},
			},
			schema: "SCOTT", table: "TEMP_DATA",
			want: false,
		},
		{
			name:   "glob include schema pattern",
			filter: TableFilterConfig{Include: []string{"SCOTT.*"}},
			schema: "SCOTT", table: "EMP",
			want: true,
		},
		{
			name:   "glob include schema pattern no match",
			filter: TableFilterConfig{Include: []string{"HR.*"}},
			schema: "SCOTT", table: "EMP",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchTable(tt.filter, tt.schema, tt.table)
			if got != tt.want {
				t.Errorf("MatchTable(%v, %q, %q) = %v, want %v",
					tt.filter, tt.schema, tt.table, got, tt.want)
			}
		})
	}
}

func loadYAML(data []byte) (*Config, error) {
	f, err := os.CreateTemp("", "owl-test-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	return Load(f.Name())
}

func TestPoolConfig_Parse(t *testing.T) {
	yamlData := []byte(`
metadata:
  type: csv
  csv:
    path: ./testdata/csv/
ddl:
  target_dialect: postgres
source:
  type: postgres
  dsn: "host=127.0.0.1 port=5432 dbname=test user=u password=p sslmode=disable"
  connect_timeout: "10s"
  query_timeout: "5m"
  pool:
    max_open_conns: 20
    max_idle_conns: 8
    conn_max_lifetime: "1h"
    conn_max_idle_time: "10m"
`)
	cfg, err := loadYAML(yamlData)
	if err != nil {
		t.Fatalf("load yaml: %v", err)
	}

	p := cfg.Source.Pool
	if p.MaxOpenConns != 20 {
		t.Errorf("MaxOpenConns = %d, want 20", p.MaxOpenConns)
	}
	if p.MaxIdleConns != 8 {
		t.Errorf("MaxIdleConns = %d, want 8", p.MaxIdleConns)
	}
	if p.ConnMaxLifetime != "1h" {
		t.Errorf("ConnMaxLifetime = %q, want %q", p.ConnMaxLifetime, "1h")
	}
	if p.ConnMaxIdleTime != "10m" {
		t.Errorf("ConnMaxIdleTime = %q, want %q", p.ConnMaxIdleTime, "10m")
	}
	if cfg.Source.ConnectTimeout != "10s" {
		t.Errorf("ConnectTimeout = %q, want %q", cfg.Source.ConnectTimeout, "10s")
	}
	if cfg.Source.QueryTimeout != "5m" {
		t.Errorf("QueryTimeout = %q, want %q", cfg.Source.QueryTimeout, "5m")
	}
}

func TestPoolConfig_Defaults(t *testing.T) {
	yamlData := []byte(`
metadata:
  type: csv
  csv:
    path: ./testdata/csv/
ddl:
  target_dialect: postgres
source:
  type: postgres
  dsn: "host=127.0.0.1 dbname=test"
`)
	cfg, err := loadYAML(yamlData)
	if err != nil {
		t.Fatalf("load yaml: %v", err)
	}

	p := cfg.Source.Pool
	if p.MaxOpenConns != 0 {
		t.Errorf("default MaxOpenConns = %d, want 0", p.MaxOpenConns)
	}
	if p.MaxIdleConns != 0 {
		t.Errorf("default MaxIdleConns = %d, want 0", p.MaxIdleConns)
	}
	if p.ConnMaxLifetime != "" {
		t.Errorf("default ConnMaxLifetime = %q, want empty", p.ConnMaxLifetime)
	}
}

func TestPoolConfig_IsZero(t *testing.T) {
	tests := []struct {
		name string
		pool PoolConfig
		want bool
	}{
		{"zero value", PoolConfig{}, true},
		{"max_open set", PoolConfig{MaxOpenConns: 10}, false},
		{"max_idle set", PoolConfig{MaxIdleConns: 5}, false},
		{"lifetime set", PoolConfig{ConnMaxLifetime: "30m"}, false},
		{"idle_time set", PoolConfig{ConnMaxIdleTime: "5m"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pool.isZero(); got != tt.want {
				t.Errorf("PoolConfig.isZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDBConfig_IsZero_WithPool(t *testing.T) {
	tests := []struct {
		name string
		cfg  DBConfig
		want bool
	}{
		{"all zero", DBConfig{}, true},
		{"type only", DBConfig{Type: "postgres"}, false},
		{"pool only", DBConfig{Pool: PoolConfig{MaxOpenConns: 5}}, false},
		{"connect_timeout only", DBConfig{ConnectTimeout: "10s"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.isZero(); got != tt.want {
				t.Errorf("DBConfig.isZero() = %v, want %v", got, tt.want)
			}
		})
	}
}
