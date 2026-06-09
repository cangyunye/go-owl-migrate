package csv

import (
	"fmt"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Validate performs CSV-level validation on the SchemaModel.
// This runs AFTER the model's own Validate() and adds CSV-specific checks.
func Validate(sm *md.SchemaModel) []*md.ValidationError {
	var errs []*md.ValidationError

	// Run the model's own validation first
	errs = append(errs, sm.Validate()...)

	// CSV-specific checks
	for _, tbl := range sm.GetTables() {
		// Every table must have at least one column
		if len(tbl.GetColumns()) == 0 {
			errs = append(errs, &md.ValidationError{
				Severity: "ERROR",
				Message:  fmt.Sprintf("table %s.%s has no columns", tbl.TableSchema, tbl.TableName),
			})
		}

		// Primary key columns must exist in the table
		for _, pk := range tbl.GetPrimaryKeys() {
			if tbl.GetColumn(pk.ColumnName) == nil {
				errs = append(errs, &md.ValidationError{
					Severity: "ERROR",
					Message:  fmt.Sprintf("primary key %s references non-existent column %s.%s.%s", pk.ConstraintName, tbl.TableSchema, tbl.TableName, pk.ColumnName),
				})
			}
		}

		// Index columns must exist
		for _, idx := range tbl.GetIndexes() {
			if tbl.GetColumn(idx.ColumnName) == nil {
				errs = append(errs, &md.ValidationError{
					Severity: "ERROR",
					Message:  fmt.Sprintf("index %s references non-existent column %s.%s.%s", idx.IndexName, tbl.TableSchema, tbl.TableName, idx.ColumnName),
				})
			}
		}

		// WARNING: non-PK identity columns (may indicate configuration issue)
		for _, col := range tbl.GetColumns() {
			if col.IsIdentityColumn() {
				isPK := false
				for _, pk := range tbl.GetPrimaryKeys() {
					if pk.ColumnName == col.ColumnName {
						isPK = true
						break
					}
				}
				if !isPK {
					errs = append(errs, &md.ValidationError{
						Severity: "WARNING",
						Message:  fmt.Sprintf("column %s.%s.%s is IDENTITY but not part of primary key", tbl.TableSchema, tbl.TableName, col.ColumnName),
					})
				}
			}
		}
	}

	return errs
}
