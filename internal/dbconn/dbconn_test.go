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

// TestOptionalDriversMatchBuildTagMap guards the introspection used by
// `owl-migrate version` against drifting from the lookup used by Open.
func TestOptionalDriversMatchBuildTagMap(t *testing.T) {
	if len(optionalDrivers) != len(driverBuildTag) {
		t.Fatalf("optionalDrivers = %d entries, driverBuildTag holds %d", len(optionalDrivers), len(driverBuildTag))
	}
	for _, d := range optionalDrivers {
		if d.driver == "" || d.tag == "" {
			t.Errorf("incomplete optional driver entry: %+v", d)
		}
		if got := driverBuildTag[d.driver]; got != d.tag {
			t.Errorf("driverBuildTag[%q] = %q, want %q", d.driver, got, d.tag)
		}
	}
}

func TestLinkedDrivers(t *testing.T) {
	report := DriverReport()
	if len(report) != len(baseDrivers)+len(optionalDrivers) {
		t.Fatalf("DriverReport() = %d entries, want %d", len(report), len(baseDrivers)+len(optionalDrivers))
	}
	seen := map[string]bool{}
	for _, d := range report {
		if d.Driver == "" {
			t.Error("empty driver name in report")
		}
		if seen[d.Driver] {
			t.Errorf("driver %q reported twice", d.Driver)
		}
		seen[d.Driver] = true
		// Linked must mirror the registry lookup that Open uses.
		if d.Linked != DriverLinked(d.Driver) {
			t.Errorf("DriverReport(%q).Linked = %v, DriverLinked = %v", d.Driver, d.Linked, DriverLinked(d.Driver))
		}
		// Only build-tag-gated drivers carry a hint. Base drivers are always
		// compiled in — they register from blank imports in the command
		// packages, which a dbconn-only test binary does not carry, so their
		// Linked flag is meaningless here and only their tag matters.
		if tag, gated := driverBuildTag[d.Driver]; gated {
			if d.Tag != tag {
				t.Errorf("report tag for %q = %q, want %q", d.Driver, d.Tag, tag)
			}
		} else if d.Tag != "" {
			t.Errorf("base driver %q should carry no build tag, got %q", d.Driver, d.Tag)
		}
	}
	// Every driver the tool can select must be covered, otherwise a missing
	// build tag turns into a runtime connect error again.
	for name := range knownTypes {
		if drv, err := driverName(name); err == nil && !seen[drv] {
			t.Errorf("selectable driver %q (type %q) missing from DriverReport()", drv, name)
		}
	}
	if !seen["oboracle"] {
		t.Error("oboracle (oceanbase-oracle over the MySQL wire) missing from DriverReport()")
	}
	if DriverLinked("definitely-not-a-driver") {
		t.Error("DriverLinked(unknown) = true")
	}
}
