package config

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTableFilter_Match(t *testing.T) {
	tests := []struct {
		name    string
		filter  TableFilterConfig
		schema  string
		table   string
		want    bool
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
			name: "glob include schema pattern",
			filter: TableFilterConfig{Include: []string{"SCOTT.*"}},
			schema: "SCOTT", table: "EMP",
			want: true,
		},
		{
			name: "glob include schema pattern no match",
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
