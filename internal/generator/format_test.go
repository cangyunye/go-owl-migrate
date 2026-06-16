package generator

import "testing"

// ── IsNumeric ──

func TestIsNumeric_positiveInt(t *testing.T) {
	if !IsNumeric("123") {
		t.Error("IsNumeric('123') should be true")
	}
}

func TestIsNumeric_negativeInt(t *testing.T) {
	if !IsNumeric("-42") {
		t.Error("IsNumeric('-42') should be true")
	}
}

func TestIsNumeric_positiveFloat(t *testing.T) {
	if !IsNumeric("3.14") {
		t.Error("IsNumeric('3.14') should be true")
	}
}

func TestIsNumeric_negativeFloat(t *testing.T) {
	if !IsNumeric("-0.5") {
		t.Error("IsNumeric('-0.5') should be true")
	}
}

func TestIsNumeric_empty(t *testing.T) {
	if IsNumeric("") {
		t.Error("IsNumeric('') should be false")
	}
}

func TestIsNumeric_nonNumeric(t *testing.T) {
	if IsNumeric("abc") {
		t.Error("IsNumeric('abc') should be false")
	}
}

func TestIsNumeric_mixed(t *testing.T) {
	if IsNumeric("123abc") {
		t.Error("IsNumeric('123abc') should be false")
	}
}

func TestIsNumeric_twoDots(t *testing.T) {
	if IsNumeric("1.2.3") {
		t.Error("IsNumeric('1.2.3') should be false")
	}
}

func TestIsNumeric_dashOnly(t *testing.T) {
	if IsNumeric("-") {
		t.Error("IsNumeric('-') should be false")
	}
}

func TestIsNumeric_zero(t *testing.T) {
	if !IsNumeric("0") {
		t.Error("IsNumeric('0') should be true")
	}
}

func TestIsNumeric_trailingDot(t *testing.T) {
	if !IsNumeric("42.") {
		t.Error("IsNumeric('42.') should be true")
	}
}

// ── FormatSQLValue ──

func TestFormatSQLValue_nullPostgres(t *testing.T) {
	got := FormatSQLValue("\\N", "\\N", "postgres")
	if got != "NULL" {
		t.Errorf("null marker should become NULL, got %q", got)
	}
}

func TestFormatSQLValue_nullOracle(t *testing.T) {
	got := FormatSQLValue("\\N", "\\N", "oracle")
	if got != "NULL" {
		t.Errorf("null marker should become NULL, got %q", got)
	}
}

func TestFormatSQLValue_nullMySQL(t *testing.T) {
	got := FormatSQLValue("NULL", "NULL", "mysql")
	if got != "NULL" {
		t.Errorf("null marker should become NULL, got %q", got)
	}
}

func TestFormatSQLValue_numericPostgres(t *testing.T) {
	got := FormatSQLValue("123", "\\N", "postgres")
	if got != "123" {
		t.Errorf("numeric should not be quoted, got %q", got)
	}
}

func TestFormatSQLValue_negativeNumeric(t *testing.T) {
	got := FormatSQLValue("-42.5", "\\N", "oracle")
	if got != "-42.5" {
		t.Errorf("negative numeric should not be quoted, got %q", got)
	}
}

func TestFormatSQLValue_string(t *testing.T) {
	got := FormatSQLValue("hello", "\\N", "postgres")
	if got != "'hello'" {
		t.Errorf("string should be single-quoted, got %q", got)
	}
}

func TestFormatSQLValue_escapeQuote(t *testing.T) {
	got := FormatSQLValue("it's", "\\N", "postgres")
	if got != "'it''s'" {
		t.Errorf("single quote should be escaped, got %q", got)
	}
}

func TestFormatSQLValue_oracleEmptyStringIsNull(t *testing.T) {
	got := FormatSQLValue("", "\\N", "oracle")
	if got != "NULL" {
		t.Errorf("Oracle empty string should be NULL, got %q", got)
	}
}

func TestFormatSQLValue_postgresEmptyString(t *testing.T) {
	got := FormatSQLValue("", "\\N", "postgres")
	if got != "''" {
		t.Errorf("PG empty string should be '', got %q", got)
	}
}

func TestFormatSQLValue_mysqlEmptyString(t *testing.T) {
	got := FormatSQLValue("", "\\N", "mysql")
	if got != "''" {
		t.Errorf("MySQL empty string should be '', got %q", got)
	}
}

// ── GetQuoter ──

func TestGetQuoter_mysql(t *testing.T) {
	q := GetQuoter("mysql", false)
	got := q("myTable")
	if got != "`myTable`" {
		t.Errorf("mysql quote should be backtick, got %q", got)
	}
}

func TestGetQuoter_postgres(t *testing.T) {
	q := GetQuoter("postgres", false)
	got := q("myTable")
	if got != `"myTable"` {
		t.Errorf("postgres quote should be double-quote, got %q", got)
	}
}

func TestGetQuoter_oracle(t *testing.T) {
	q := GetQuoter("oracle", false)
	got := q("MY_TABLE")
	if got != `"MY_TABLE"` {
		t.Errorf("oracle quote should be double-quote, got %q", got)
	}
}

func TestGetQuoter_noQuote(t *testing.T) {
	q := GetQuoter("postgres", true)
	got := q("myTable")
	if got != "myTable" {
		t.Errorf("noQuote=true should return bare name, got %q", got)
	}
}
