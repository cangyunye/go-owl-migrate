package exporter

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Filter fragment rules. The fragment is a literal SQL WHERE body: the tool
// never parses it (the database is the validator — wrong columns/syntax fail
// the conditional-COUNT gate before any data moves), but a few constructs are
// rejected outright because they are always wrong here:
//   - ';'            → multi-statement
//   - '--' / '/*'    → comment truncation
//   - '?' / ':123'   → bind placeholders collide with the keyset-cursor args
// Volatile functions are warned about (not rejected): keyset pagination
// requires the predicate to be deterministic per row.
var (
	filterForbidden = []*regexp.Regexp{
		regexp.MustCompile(`;`),
		regexp.MustCompile(`--`),
		regexp.MustCompile(`/\*`),
		regexp.MustCompile(`\?`),
		regexp.MustCompile(`:[0-9]`), // Oracle/PG numbered binds
	}
	filterVolatile = regexp.MustCompile(`(?i)\b(NOW\(\)|CURRENT_DATE|CURRENT_TIMESTAMP|SYSDATE|SYSDATE\(\)|GETDATE\(\)|RANDOM\(\)|RAND\(\))`)
)

// ValidateFilterFragment applies the hard rules to one WHERE fragment.
// Exported so the cmd layer can pre-reject CLI --where values cleanly.
func ValidateFilterFragment(f string) error {
	trimmed := strings.TrimSpace(f)
	if trimmed == "" {
		return fmt.Errorf("filter fragment is empty")
	}
	for _, re := range filterForbidden {
		if loc := re.FindStringIndex(trimmed); loc != nil {
			bad := trimmed[loc[0]:min(loc[1], loc[0]+8)]
			return fmt.Errorf("filter fragment contains forbidden construct %q (offset %d): %s",
				bad, loc[0], truncateSnippet(trimmed))
		}
	}
	return nil
}

func truncateSnippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func globMatch(pattern, name string) bool {
	ok, _ := filepath.Match(strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(name))
	return ok
}

// resolveFilter picks the WHERE fragment for one table: an exact
// "schema.table" key (case-insensitive) wins; otherwise glob keys match the
// bare table name (no dot in key) or "schema.table" (dotted key) — matching
// more than one glob is an error (ambiguous semantics; fail fast instead of
// merging). No match returns "".
func (e *Exporter) resolveFilter(schema, table string) (string, error) {
	if len(e.cfg.Filters) == 0 {
		return "", nil
	}
	lowerTable := strings.ToLower(table)
	for k, f := range e.cfg.Filters {
		if strings.EqualFold(k, schema+"."+table) {
			return e.checkedFilter(f, schema+"."+table)
		}
	}
	var matched []string
	var frag string
	for k, f := range e.cfg.Filters {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		hit := false
		if strings.Contains(k, ".") {
			hit = globMatch(k, strings.ToLower(schema+"."+table))
		} else {
			hit = globMatch(k, lowerTable)
		}
		if hit {
			matched = append(matched, k)
			frag = f
		}
	}
	if len(matched) == 0 {
		return "", nil
	}
	if len(matched) > 1 {
		return "", fmt.Errorf("table %s.%s matches multiple export.filters keys (%s); make the patterns disjoint",
			schema, table, strings.Join(matched, ", "))
	}
	return e.checkedFilter(frag, schema+"."+table)
}

func (e *Exporter) checkedFilter(f, table string) (string, error) {
	if err := ValidateFilterFragment(f); err != nil {
		return "", fmt.Errorf("filter for %s: %w", table, err)
	}
	if filterVolatile.MatchString(f) && e.logger != nil {
		e.logger.Warn("filter predicate contains a volatile function; keyset pagination requires deterministic predicates — rows may drift between batches",
			zap.String("table", table))
	}
	return f, nil
}

// ValidateFilters is the pre-export conditional-COUNT gate: for every table
// with a resolved filter it runs SELECT COUNT(*) … WHERE <filter>. This makes
// the database the validator — wrong columns/syntax/permissions abort the job
// with the filter named — and records the source-side expected row count so
// reports can reconcile source vs CSV vs imported rows. Skipped entirely when
// cfg.FiltersCheck == "off".
func (e *Exporter) ValidateFilters(ctx context.Context, tables []*md.TableDef) error {
	if len(e.cfg.Filters) == 0 {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(e.cfg.FiltersCheck), "off") {
		e.logger.Info("export.filters_check=off: skipping the conditional-COUNT gate; report lacks the source-side expected count")
		return nil
	}
	for _, tbl := range tables {
		frag, err := e.resolveFilter(tbl.TableSchema, tbl.TableName)
		if err != nil {
			return err
		}
		if frag == "" {
			continue
		}
		from := fmt.Sprintf("%s.%s", e.quoteIdent(tbl.TableSchema), e.quoteIdent(tbl.TableName))
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", from, frag)
		var n int64
		if err := e.db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return fmt.Errorf("filter gate failed for %s.%s (filter %q): %w — fix export.filters and retry",
				tbl.TableSchema, tbl.TableName, frag, err)
		}
		key := strings.ToLower(tbl.TableSchema + "." + tbl.TableName)
		e.filterCounts[key] = n
		e.logger.Info("filter gate passed",
			zap.String("table", key), zap.Int64("count", n), zap.String("filter", frag))
	}
	return nil
}

// filterCountFor returns the gate's expected row count (0 = unknown).
func (e *Exporter) filterCountFor(schema, table string) int64 {
	return e.filterCounts[strings.ToLower(schema+"."+table)]
}

// whereFor resolves (and error-checks) the fragment for one table in the
// per-table export path.
func (e *Exporter) whereFor(tbl *md.TableDef) (string, error) {
	return e.resolveFilter(tbl.TableSchema, tbl.TableName)
}
