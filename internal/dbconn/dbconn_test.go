package dbconn

import (
	"net/url"
	"strings"
	"testing"
)

func TestFamily(t *testing.T) {
	tests := map[string]string{
		"mysql":              "mysql",
		"goldendb":           "mysql",
		"goldendb-mysql":     "mysql",
		"oceanbase-mysql":    "mysql",
		"oracle":             "oracle",
		"oceanbase-oracle":   "oracle",
		"postgres":           "postgres",
		"opengaussdb":        "postgres",
		"opengaussdb-oracle": "postgres",
		"opengaussdb-mysql":  "postgres",
		"panweidb":           "postgres",
		"panweidb-oracle":    "postgres",
		"sqlite3":            "sqlite3",
		"duckdb":             "duckdb",
	}
	for in, want := range tests {
		if got := Family(in); got != want {
			t.Errorf("Family(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDriverName(t *testing.T) {
	tests := map[string]string{
		"mysql":              "mysql",
		"goldendb":           "mysql",
		"oceanbase-oracle":   "oracle",
		"postgres":           "postgres",
		"opengaussdb":        "opengauss",
		"opengaussdb-oracle": "opengauss",
		"opengaussdb-mysql":  "opengauss",
		"panweidb":           "opengauss",
		"panweidb-mysql":     "opengauss",
		"panweidb-oracle":    "opengauss",
		"sqlite3":            "sqlite3",
		"duckdb":             "duckdb",
	}
	for in, want := range tests {
		got, err := driverName(in)
		if err != nil {
			t.Errorf("driverName(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("driverName(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := driverName("nosuchdb"); err == nil {
		t.Error("driverName(nosuchdb) should error")
	}
}

func TestInjectPGSearchPath(t *testing.T) {
	t.Run("url dsn adds search_path", func(t *testing.T) {
		got := InjectPGSearchPath("postgres://postgres:pw@localhost:5432/db?sslmode=disable", "public")
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if u.Query().Get("search_path") != "public" {
			t.Errorf("search_path = %q, want public", u.Query().Get("search_path"))
		}
		if u.Query().Get("sslmode") != "disable" {
			t.Errorf("sslmode lost: %s", got)
		}
	})

	t.Run("existing search_path wins", func(t *testing.T) {
		dsn := "postgres://u:p@h:5432/db?search_path=custom"
		if got := InjectPGSearchPath(dsn, "public"); got != dsn {
			t.Errorf("should preserve existing search_path, got %q", got)
		}
	})

	t.Run("keyword dsn appends search_path", func(t *testing.T) {
		got := InjectPGSearchPath("host=localhost port=5432 dbname=db", "myschema")
		if !strings.Contains(got, "search_path=myschema") {
			t.Errorf("keyword dsn missing search_path: %q", got)
		}
	})

	t.Run("empty schema unchanged", func(t *testing.T) {
		dsn := "postgres://u:p@h:5432/db"
		if got := InjectPGSearchPath(dsn, ""); got != dsn {
			t.Errorf("empty schema should pass through, got %q", got)
		}
	})
}

func TestInjectOracleParams(t *testing.T) {
	t.Run("adds defaults to bare dsn", func(t *testing.T) {
		got := InjectOracleParams("oracle://user:pw@host:1521/ORCL")
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		q := u.Query()
		if q.Get("PREFETCH_ROWS") != "25" {
			t.Errorf("PREFETCH_ROWS = %q, want 25", q.Get("PREFETCH_ROWS"))
		}
		if q.Get("LOB FETCH") != "POST" {
			t.Errorf("LOB FETCH = %q, want POST", q.Get("LOB FETCH"))
		}
	})

	t.Run("preserves existing values", func(t *testing.T) {
		got := InjectOracleParams("oracle://user:pw@host:1521/ORCL?PREFETCH_ROWS=100&LOB%20FETCH=INLINE")
		u, _ := url.Parse(got)
		q := u.Query()
		if q.Get("PREFETCH_ROWS") != "100" {
			t.Errorf("PREFETCH_ROWS = %q, want preserved 100", q.Get("PREFETCH_ROWS"))
		}
		if q.Get("LOB FETCH") != "INLINE" {
			t.Errorf("LOB FETCH = %q, want preserved INLINE", q.Get("LOB FETCH"))
		}
	})

	t.Run("preserves case-insensitive user param", func(t *testing.T) {
		got := InjectOracleParams("oracle://user:pw@host:1521/ORCL?prefetch_rows=50")
		u, _ := url.Parse(got)
		q := u.Query()
		count := 0
		for k := range q {
			if k == "PREFETCH_ROWS" || k == "prefetch_rows" {
				count++
			}
		}
		if count == 0 {
			t.Errorf("user prefetch_rows lost: %s", got)
		}
		if q.Get("PREFETCH_ROWS") == "25" {
			t.Errorf("should not override user-set prefetch: %s", got)
		}
	})

	t.Run("non-url dsn passes through", func(t *testing.T) {
		got := InjectOracleParams("user/pass@ORCL")
		if got != "user/pass@ORCL" {
			t.Errorf("non-url DSN should pass through, got %q", got)
		}
	})
}
