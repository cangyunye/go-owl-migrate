// Package main generates a test xlsx file for owl-migrate testing.
//
// Run from project root:
//   go run ./testdata/xlsx/gen_test_xlsx.go
//
// This creates testdata/xlsx/scott.xlsx containing:
//   - Metadata sheets: tables, columns, primary_keys, foreign_keys, indexes
//   - Data sheets: @EMP, @DEPT, @BONUS
package main

import (
	"fmt"
	"os"

	"github.com/xuri/excelize/v2"
)

func main() {
	const outPath = "./testdata/xlsx/scott.xlsx"

	f := excelize.NewFile()
	defer f.Close()

	// Remove default sheet at the end
	defaultSheet := "Sheet1"

	// ── Metadata sheet: tables ──
	writeSheet(f, "tables", [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "TABLE_COMMENT"},
		{"SCOTT", "EMP", "TABLE", "Employee table"},
		{"SCOTT", "DEPT", "TABLE", "Department table"},
		{"SCOTT", "BONUS", "TABLE", "Bonus table"},
	})

	// ── Metadata sheet: columns ──
	writeSheet(f, "columns", [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DEFAULT_VALUE", "COLUMN_COMMENT"},
		{"SCOTT", "EMP", "EMPNO", "1", "NUMBER", "22", "4", "0", "NO", "", "Employee number"},
		{"SCOTT", "EMP", "ENAME", "2", "VARCHAR2", "10", "", "", "YES", "", "Employee name"},
		{"SCOTT", "EMP", "JOB", "3", "VARCHAR2", "9", "", "", "YES", "", "Job title"},
		{"SCOTT", "EMP", "MGR", "4", "NUMBER", "22", "4", "0", "YES", "", "Manager ID"},
		{"SCOTT", "EMP", "HIREDATE", "5", "DATE", "", "", "", "YES", "", "Hire date"},
		{"SCOTT", "EMP", "SAL", "6", "NUMBER", "22", "7", "2", "YES", "", "Salary"},
		{"SCOTT", "EMP", "COMM", "7", "NUMBER", "22", "7", "2", "YES", "", "Commission"},
		{"SCOTT", "EMP", "DEPTNO", "8", "NUMBER", "22", "2", "0", "NO", "", "Department number"},
		{"SCOTT", "DEPT", "DEPTNO", "1", "NUMBER", "22", "2", "0", "NO", "", "Department number"},
		{"SCOTT", "DEPT", "DNAME", "2", "VARCHAR2", "14", "", "", "YES", "", "Department name"},
		{"SCOTT", "DEPT", "LOC", "3", "VARCHAR2", "13", "", "", "YES", "", "Location"},
		{"SCOTT", "BONUS", "ENAME", "1", "VARCHAR2", "10", "", "", "YES", "", "Employee name"},
		{"SCOTT", "BONUS", "JOB", "2", "VARCHAR2", "9", "", "", "YES", "", "Job"},
		{"SCOTT", "BONUS", "SAL", "3", "NUMBER", "22", "7", "2", "YES", "", "Salary"},
		{"SCOTT", "BONUS", "COMM", "4", "NUMBER", "22", "7", "2", "YES", "", "Commission"},
	})

	// ── Metadata sheet: primary_keys ──
	writeSheet(f, "primary_keys", [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME", "ORDINAL_POSITION"},
		{"SCOTT", "EMP", "PK_EMP", "EMPNO", "1"},
		{"SCOTT", "DEPT", "PK_DEPT", "DEPTNO", "1"},
	})

	// ── Metadata sheet: foreign_keys ──
	writeSheet(f, "foreign_keys", [][]string{
		{"CONSTRAINT_NAME", "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "REF_SCHEMA", "REF_TABLE", "REF_COLUMN", "DELETE_RULE"},
		{"FK_EMP_DEPT", "SCOTT", "EMP", "DEPTNO", "SCOTT", "DEPT", "DEPTNO", "CASCADE"},
		{"FK_EMP_MGR", "SCOTT", "EMP", "MGR", "SCOTT", "EMP", "EMPNO", "SET NULL"},
	})

	// ── Metadata sheet: indexes ──
	writeSheet(f, "indexes", [][]string{
		{"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "INDEX_TYPE", "UNIQUENESS"},
		{"SCOTT", "EMP", "IDX_EMP_ENAME", "ENAME", "1", "BTREE", "NONUNIQUE"},
		{"SCOTT", "EMP", "IDX_EMP_DEPTNO", "DEPTNO", "1", "BTREE", "NONUNIQUE"},
	})

	// ── Data sheet: @EMP ──
	writeSheet(f, "@EMP", [][]string{
		{"EMPNO", "ENAME", "JOB", "MGR", "HIREDATE", "SAL", "COMM", "DEPTNO"},
		{"7369", "SMITH", "CLERK", "7902", "1980-12-17", "800", "", "20"},
		{"7499", "ALLEN", "SALESMAN", "7698", "1981-02-20", "1600", "300", "30"},
		{"7521", "WARD", "SALESMAN", "7698", "1981-02-22", "1250", "500", "30"},
		{"7566", "JONES", "MANAGER", "7839", "1981-04-02", "2975", "", "20"},
		{"7654", "MARTIN", "SALESMAN", "7698", "1981-09-28", "1250", "1400", "30"},
		{"7698", "BLAKE", "MANAGER", "7839", "1981-05-01", "2850", "", "30"},
		{"7782", "CLARK", "MANAGER", "7839", "1981-06-09", "2450", "", "10"},
		{"7788", "SCOTT", "ANALYST", "7566", "1987-04-19", "3000", "", "20"},
		{"7839", "KING", "PRESIDENT", "", "1981-11-17", "5000", "", "10"},
		{"7844", "TURNER", "SALESMAN", "7698", "1981-09-08", "1500", "0", "30"},
		{"7876", "ADAMS", "CLERK", "7788", "1987-05-23", "1100", "", "20"},
		{"7900", "JAMES", "CLERK", "7698", "1981-12-03", "950", "", "30"},
		{"7902", "FORD", "ANALYST", "7566", "1981-12-03", "3000", "", "20"},
		{"7934", "MILLER", "CLERK", "7782", "1982-01-23", "1300", "", "10"},
	})

	// ── Data sheet: @DEPT ──
	writeSheet(f, "@DEPT", [][]string{
		{"DEPTNO", "DNAME", "LOC"},
		{"10", "ACCOUNTING", "NEW YORK"},
		{"20", "RESEARCH", "DALLAS"},
		{"30", "SALES", "CHICAGO"},
		{"40", "OPERATIONS", "BOSTON"},
	})

	// ── Data sheet: @BONUS ──
	writeSheet(f, "@BONUS", [][]string{
		{"ENAME", "JOB", "SAL", "COMM"},
		{"SMITH", "CLERK", "800", ""},
		{"ALLEN", "SALESMAN", "1600", "300"},
		{"WARD", "SALESMAN", "1250", "500"},
	})

	// Remove the default Sheet1
	f.DeleteSheet(defaultSheet)

	// Set the active sheet to tables
	idx, _ := f.GetSheetIndex("tables")
	f.SetActiveSheet(idx)

	if err := f.SaveAs(outPath); err != nil {
		fmt.Fprintf(os.Stderr, "save xlsx: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s\n", outPath)
}

func writeSheet(f *excelize.File, name string, rows [][]string) {
	if _, err := f.NewSheet(name); err != nil {
		fmt.Fprintf(os.Stderr, "create sheet %q: %v\n", name, err)
		os.Exit(1)
	}
	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := f.SetCellValue(name, cell, val); err != nil {
				fmt.Fprintf(os.Stderr, "set cell %q %s: %v\n", name, cell, err)
				os.Exit(1)
			}
		}
	}
}
