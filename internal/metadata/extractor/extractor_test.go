package extractor

import "testing"

func TestNormalizeDBType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"goldendb-mysql", "mysql"},
		{"goldendb-oracle", "oracle"},
		{"oceanbase-mysql", "mysql"},
		{"oceanbase-oracle", "oracle"},
		{"goldendb", "mysql"},
		{"oceanbase", "mysql"},
		{"panweidb", "postgres"},
		{"opengaussdb", "postgres"},
		{"postgres", "postgres"},
		{"mysql", "mysql"},
		{"oracle", "oracle"},
	}
	for _, tt := range tests {
		if got := normalizeDBType(tt.in); got != tt.want {
			t.Errorf("normalizeDBType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGet(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql", "oracle"} {
		if _, err := Get(dbType); err != nil {
			t.Errorf("Get(%q) unexpected error: %v", dbType, err)
		}
	}
	if _, err := Get("nonexistent"); err == nil {
		t.Error("Get(nonexistent) expected error")
	}
}

func TestGetQuerySQL(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql", "oracle"} {
		for _, obj := range []string{"tables", "columns", "pk", "indexes", "views"} {
			if sql := GetQuerySQL(dbType, obj); sql == "" {
				t.Errorf("GetQuerySQL(%q, %q) = empty", dbType, obj)
			}
		}
	}
	if sql := GetQuerySQL("postgres", "bogus"); sql != "" {
		t.Errorf("GetQuerySQL(postgres, bogus) = %q, want empty", sql)
	}
}
