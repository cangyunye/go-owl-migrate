package mapping

import (
	"strings"
	"testing"
)

func TestLoadTypeMapping(t *testing.T) {
	input := `
name: "Oracle to PostgreSQL"
version: "1.0"
source_db: oracle
target_db: postgresql

exact_mappings:
  "VARCHAR2": "VARCHAR"
  "CHAR": "CHAR"
  "NUMBER": "NUMERIC"
  "DATE": "TIMESTAMP"
  "CLOB": "TEXT"
  "BLOB": "BYTEA"

parameterized:
  "NUMBER":
    - condition: { scale: 0, max_precision: 4 }
      target: "SMALLINT"
    - condition: { scale: 0, max_precision: 9 }
      target: "INTEGER"
    - condition: { scale: 0, max_precision: 18 }
      target: "BIGINT"
    - condition: { scale_gt: 0 }
      target: "NUMERIC(%p,%s)"
    - condition: { default: true }
      target: "NUMERIC"

semantic_overrides:
  - pattern: ".*_FLAG$"
    condition: { type: "CHAR", length: 1 }
    target_type: "BOOLEAN"
  - pattern: ".*_TIME$|.*_DATE$"
    condition: { type: "DATE" }
    target_type: "TIMESTAMP"
`
	tm, err := LoadTypeMapping(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tm.Name != "Oracle to PostgreSQL" {
		t.Errorf("Name = %q", tm.Name)
	}
	if tm.SourceDB != "oracle" {
		t.Errorf("SourceDB = %q", tm.SourceDB)
	}

	// Exact mapping: Varchar2 → VARCHAR (length comes from source)
	target := tm.MapType("VARCHAR2", 100, 0, 0)
	if target != "VARCHAR" {
		t.Errorf("VARCHAR2(100) → %q, want VARCHAR", target)
	}

	// Parameterized mapping: NUMBER(4,0) → SMALLINT
	target = tm.MapType("NUMBER", 0, 4, 0)
	if target != "SMALLINT" {
		t.Errorf("NUMBER(4,0) → %q, want SMALLINT", target)
	}

	// NUMBER(9,0) → INTEGER (max_precision: 9)
	target = tm.MapType("NUMBER", 0, 9, 0)
	if target != "INTEGER" {
		t.Errorf("NUMBER(9,0) → %q, want INTEGER", target)
	}

	// NUMBER(10,0) → BIGINT (10 > max_precision:9, falls to max_precision:18)
	target = tm.MapType("NUMBER", 0, 10, 0)
	if target != "BIGINT" {
		t.Errorf("NUMBER(10,0) → %q, want BIGINT", target)
	}

	// NUMBER(25,0) → NUMERIC (25 > max_precision:18, falls to default)
	target = tm.MapType("NUMBER", 0, 25, 0)
	if !strings.Contains(target, "NUMERIC") {
		t.Errorf("NUMBER(25,0) → %q, want NUMERIC", target)
	}

	// NUMBER(10,2) → NUMERIC(10,2)
	target = tm.MapType("NUMBER", 0, 10, 2)
	if !strings.Contains(target, "NUMERIC") {
		t.Errorf("NUMBER(10,2) → %q, want NUMERIC(...)", target)
	}

	// Semantic override: *_TIME with DATE → TIMESTAMP
	target = tm.ApplySemanticOverride("CREATED_TIME", "DATE", 0, 0, 0)
	if target != "TIMESTAMP" {
		t.Errorf("CREATED_TIME DATE → %q, want TIMESTAMP", target)
	}
}

func TestLoadTypeMapping_Reverse(t *testing.T) {
	input := `
name: "PostgreSQL to Oracle"
source_db: postgresql
target_db: oracle

exact_mappings:
  "VARCHAR": "VARCHAR2"
  "TEXT": "CLOB"
  "BOOLEAN": "NUMBER(1)"
  "INTEGER": "NUMBER(10,0)"
  "BIGINT": "NUMBER(19,0)"
`
	tm, err := LoadTypeMapping(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	target := tm.MapType("BOOLEAN", 0, 0, 0)
	if target != "NUMBER(1)" {
		t.Errorf("BOOLEAN → %q, want NUMBER(1)", target)
	}

	target = tm.MapType("TEXT", 0, 0, 0)
	if target != "CLOB" {
		t.Errorf("TEXT → %q, want CLOB", target)
	}
}

func TestMapType_DefaultFallback(t *testing.T) {
	input := `
name: "test"
source_db: oracle
target_db: postgresql

exact_mappings:
  "VARCHAR2": "VARCHAR"
`
	tm, _ := LoadTypeMapping(strings.NewReader(input))

	// Unknown type → returns original with length if applicable
	target := tm.MapType("UNKNOWN_TYPE", 255, 0, 0)
	if target != "UNKNOWN_TYPE" {
		t.Errorf("unknown type → %q, want UNKNOWN_TYPE", target)
	}
}
