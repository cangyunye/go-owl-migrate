package metadata

import (
	"fmt"
	"strings"
)

// ProjectTable applies an ordered column projection and rename mapping to a
// table definition, producing a new *TableDef (input untouched):
//   - include defines BOTH the surviving columns AND their output order
//     (the CSV column order equals the config list order, not source order);
//     nil/empty include = all source columns in source order.
//   - rename maps source column name → output name, applied in place.
//   - primary keys, and index/foreign-key column references, are carried
//     through and renamed; a PK column dropped by include is an error
//     (keyset pagination and CDC both depend on the key).
// Matching is case-insensitive everywhere, mirroring ObjectSelector semantics.
func ProjectTable(tbl *TableDef, include []string, rename map[string]string) (*TableDef, error) {
	out := *tbl // shallow copy; slice/map fields rebuilt below

	// index source columns by lowercase name
	byLower := make(map[string]*ColumnDef, len(tbl.Columns))
	ordered := make([]*ColumnDef, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		c := c
		byLower[strings.ToLower(c.ColumnName)] = c
		ordered = append(ordered, c)
	}
	// PK set (lowercase)
	pkSet := map[string]bool{}
	for _, pk := range tbl.PrimaryKeys {
		pkSet[strings.ToLower(pk.ColumnName)] = true
	}
	dropPK := func(name string) []string {
		var missing []string
		for _, pk := range tbl.PrimaryKeys {
			pkLower := strings.ToLower(pk.ColumnName)
			dropped := true
			for _, inc := range include {
				if strings.EqualFold(strings.TrimSpace(inc), pkLower) {
					dropped = false
					break
				}
			}
			if dropped {
				missing = append(missing, pk.ColumnName)
			}
		}
		return missing
	}

	var selected []*ColumnDef
	if len(include) == 0 {
		selected = ordered
	} else {
		for _, inc := range include {
			name := strings.TrimSpace(inc)
			if name == "" {
				continue
			}
			c, ok := byLower[strings.ToLower(name)]
			if !ok {
				avail := make([]string, 0, len(ordered))
				for _, c := range ordered {
					avail = append(avail, c.ColumnName)
				}
				return nil, fmt.Errorf("table %s.%s: column %q not found (available: %s)",
					tbl.TableSchema, tbl.TableName, name, strings.Join(avail, ", "))
			}
			selected = append(selected, c)
		}
		if missing := dropPK(""); len(missing) > 0 {
			return nil, fmt.Errorf("table %s.%s: projection drops primary key column(s) %s — keyset pagination depends on the key; include it or drop the projection",
				tbl.TableSchema, tbl.TableName, strings.Join(missing, ", "))
		}
	}

	renameFor := func(name string) string {
		for src, dst := range rename {
			if strings.EqualFold(src, name) {
				return dst
			}
		}
		return name
	}

	newCols := make([]*ColumnDef, 0, len(selected))
	renames := map[string]string{} // lower(old) → new
	for i, c := range selected {
		nc := *c
		if newName := renameFor(c.ColumnName); newName != c.ColumnName {
			renames[strings.ToLower(c.ColumnName)] = newName
			nc.ColumnName = newName
		}
		// 序号重写为投影顺序：GetColumns() 恒按 OrdinalPosition 排序，
		// 不重写则 include 的顺序语义会被源表序覆盖。
		nc.OrdinalPosition = i + 1
		newCols = append(newCols, &nc)
	}

	// carry PKs (renamed, order preserved)
	newPKs := make([]*PrimaryKeyDef, 0, len(tbl.PrimaryKeys))
	for _, pk := range tbl.PrimaryKeys {
		npk := *pk
		if n, ok := renames[strings.ToLower(pk.ColumnName)]; ok {
			npk.ColumnName = n
		}
		newPKs = append(newPKs, &npk)
	}

	newIdx := make([]*IndexDef, 0, len(tbl.Indexes))
	for _, idx := range tbl.Indexes {
		nidx := *idx
		if n, ok := renames[strings.ToLower(idx.ColumnName)]; ok {
			nidx.ColumnName = n
		}
		newIdx = append(newIdx, &nidx)
	}
	newFKs := make([]*ForeignKeyDef, 0, len(tbl.ForeignKeys))
	for _, fk := range tbl.ForeignKeys {
		nfk := *fk
		if n, ok := renames[strings.ToLower(fk.ColumnName)]; ok {
			nfk.ColumnName = n
		}
		newFKs = append(newFKs, &nfk)
	}

	out.Columns = newCols
	out.PrimaryKeys = newPKs
	out.Indexes = newIdx
	out.ForeignKeys = newFKs
	return &out, nil
}
