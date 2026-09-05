package main

// Reusable E2E dataset + smoke tool for cross-database migration testing
// (openGauss multi-compat-mode A/PG/B, MySQL, PostgreSQL, OceanBase
// Oracle/MySQL). Seeds the canonical dept/emp/special dataset (special chars,
// NULL forms, comma/newline edge cases — see docs/test-plans/e2e-dataset.md)
// into any target DB and verifies row counts / special-char fidelity.
// Uses dbconn.Open so each database's production driver path is exercised.
//
// DSNs come from env (see testdata/db/.local-dev.env; OS env wins):
//
//	OWL_E2E_OPGAUSS_DSN / OWL_E2E_OPGAUSS_ADMIN_DSN   openGauss miguser/ogadmin
//	OWL_E2E_MYSQL_DSN / OWL_E2E_PG_DSN                MySQL root / PostgreSQL superme
//	OWL_E2E_OB_ORACLE_DSN / OWL_E2E_OB_MYSQL_DSN      OceanBase tenants (sys / root)
//	OWL_E2E_OB_ORACLE_MIGSRC_DSN                      OB Oracle tenant source user MIGSRC
//
// Subcommands:
//
//	go run ./tools/ogtest seed                          # seed src_og/tgt_og, src_my/tgt_my, src_pg/tgt_pg
//	go run ./tools/ogtest seeddb <dbname>               # openGauss compat db og_pg|og_ora|og_mysql (src+tgt)
//	go run ./tools/ogtest seedob                        # OB Oracle (MIGSRC) + OB MySQL (ogtest)
//	go run ./tools/ogtest verify <type> <dsn> <schema>  # print table row counts
//	go run ./tools/ogtest verifyspecial <type> <dsn> <schema>  # print special table rows verbatim
//	go run ./tools/ogtest verifydb <dbname> <schema>    # verify openGauss db schema as miguser
//	go run ./tools/ogtest pgmk <schema...>              # create PG target schemas
//
// Migration configs (source/target DSNs + dialect) live in .tmp_ogtest/cfg/
// (git-ignored, real credentials) and are run via the owl-migrate binary:
//
//	/tmp/owl-migrate migrate -c .tmp_ogtest/cfg/<scenario>.yaml --temp-dir ... -r ...
//
// Tested-scenario matrix: docs/test-plans/e2e-dataset.md.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
)

const (
	srcOG = "src_og"
	srcMY = "src_my"
	srcPG = "src_pg"
	tgtOG = "tgt_og"
	tgtMY = "tgt_my"
	tgtPG = "tgt_pg"

	ogUser = "miguser"
)

var deptDDL = `CREATE TABLE %s.dept (
  deptno INT PRIMARY KEY,
  dname  VARCHAR(30) NOT NULL,
  loc    VARCHAR(30)
)`

var empDDL = `CREATE TABLE %s.emp (
  empno    INT PRIMARY KEY,
  ename    VARCHAR(30) NOT NULL,
  job      VARCHAR(20),
  mgr      INT,
  hiredate DATE,
  sal      DECIMAL(9,2),
  comm     DECIMAL(9,2),
  deptno   INT,
  CONSTRAINT fk_emp_dept FOREIGN KEY (deptno) REFERENCES %s.dept(deptno)
)`

var deptRows = []string{
	"(10,'ACCOUNTING','NEW YORK')",
	"(20,'RESEARCH','DALLAS')",
	"(30,'SALES','CHICAGO')",
	"(40,'OPERATIONS','BOSTON')",
}

var empRows = []string{
	"(7369,'SMITH','CLERK',7902,'1980-12-17',800.00,NULL,20)",
	"(7499,'ALLEN','SALESMAN',7698,'1981-02-20',1600.00,300.00,30)",
	"(7521,'WARD','SALESMAN',7698,'1981-02-22',1250.00,500.00,30)",
	"(7566,'JONES','MANAGER',7839,'1981-04-02',2975.00,NULL,20)",
	"(7698,'BLAKE','MANAGER',7839,'1981-05-01',2850.00,NULL,30)",
	"(7788,'SCOTT','ANALYST',7566,'1987-04-19',3000.00,NULL,20)",
	"(7839,'KING','PRESIDENT',NULL,'1981-11-17',5000.00,NULL,10)",
	"(7934,'MILLER','CLERK',7782,'1982-01-23',1300.00,NULL,10)",
}

var empRowsOracle = []string{
	"(7369,'SMITH','CLERK',7902,TO_DATE('1980-12-17','YYYY-MM-DD'),800.00,NULL,20)",
	"(7499,'ALLEN','SALESMAN',7698,TO_DATE('1981-02-20','YYYY-MM-DD'),1600.00,300.00,30)",
	"(7521,'WARD','SALESMAN',7698,TO_DATE('1981-02-22','YYYY-MM-DD'),1250.00,500.00,30)",
	"(7566,'JONES','MANAGER',7839,TO_DATE('1981-04-02','YYYY-MM-DD'),2975.00,NULL,20)",
	"(7698,'BLAKE','MANAGER',7839,TO_DATE('1981-05-01','YYYY-MM-DD'),2850.00,NULL,30)",
	"(7788,'SCOTT','ANALYST',7566,TO_DATE('1987-04-19','YYYY-MM-DD'),3000.00,NULL,20)",
	"(7839,'KING','PRESIDENT',NULL,TO_DATE('1981-11-17','YYYY-MM-DD'),5000.00,NULL,10)",
	"(7934,'MILLER','CLERK',7782,TO_DATE('1982-01-23','YYYY-MM-DD'),1300.00,NULL,10)",
}

var specialDDL = `CREATE TABLE %s.special (
  id INT PRIMARY KEY,
  q_col VARCHAR(100),
  s_col VARCHAR(100),
  c_col VARCHAR(100),
  b_col VARCHAR(100),
  n_col VARCHAR(100),
  u_col VARCHAR(100),
  num_col INT,
  date_col DATE
)`

// specialRows: covers double/single quotes, # @ ! %, comma/semicolon/colon,
// backslash, newline/tab, unicode, NULLs, and the literal strings "NULL" and
// "\N" (which collide with the default null markers).
var specialRows = []string{
	"(1, '\"quoted\" ''single''', '#hash @at !excl %pct', 'comma,semi;colon:', 'back\\\\slash', 'line1'||chr(10)||'line2'||chr(9)||'tab', '中文🎉', NULL, NULL)",
	"(2, NULL, 'NULL', '\\N', '', '', '', NULL, NULL)",
}

func seedSpecial(db *sql.DB, schema, dbType string) {
	// newline/tab concat differs between PG-family and MySQL.
	var nlExpr, tabExpr string
	if dbType == "mysql" {
		nlExpr, tabExpr = "CHAR(10)", "CHAR(9)"
	} else {
		nlExpr, tabExpr = "chr(10)", "chr(9)"
	}
	// nlv builds a newline-joined SQL value; parts are quoted string literals
	// and newline/tab function calls, interleaved.
	nlv := func(parts ...string) string {
		if dbType == "mysql" {
			return "CONCAT(" + strings.Join(parts, ",") + ")"
		}
		return strings.Join(parts, "||")
	}
	exec(db, fmt.Sprintf("DROP TABLE IF EXISTS %s.special", schema),
		fmt.Sprintf(specialDDL, schema))
	// row 1: special chars + newline/tab + unicode + NULLs
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (1, '\"quoted\" ''single''', '#hash @at !excl %%pct', 'comma,semi;colon:', 'back\\\\slash', %s, '中文🎉', NULL, NULL)", schema, nlv("'line1'", nlExpr, "'line2'", tabExpr, "'tab'")))
	// row 2: NULL + literal "NULL" + literal "\N" + empty strings
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (2, NULL, 'NULL', '\\N', '', '', '', NULL, NULL)", schema))
	// row 3: leading/trailing/consecutive commas; multiple newlines
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (3, NULL, NULL, ',a,,b,', NULL, %s, NULL, NULL, NULL)", schema, nlv("'l1'", nlExpr, "'l2'", nlExpr, "'l3'")))
	// row 4: comma-only; leading+trailing newline
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (4, NULL, NULL, ',', NULL, %s, NULL, NULL, NULL)", schema, nlv(nlExpr, "'lead'", nlExpr)))
	// row 5: many commas; trailing newline
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (5, NULL, NULL, 'a,b,c,d', NULL, %s, NULL, NULL, NULL)", schema, nlv("'trail'", nlExpr)))
	// row 6: lone comma; lone newline
	exec(db, fmt.Sprintf("INSERT INTO %s.special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (6, NULL, NULL, ',', NULL, %s, NULL, NULL, NULL)", schema, nlv(nlExpr)))
}

func open(typ, dsn string) *sql.DB {
	cfg := config.DBConfig{Type: typ, DSN: dsn, ConnectTimeout: "15s"}
	db, err := dbconn.Open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", typ, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping %s: %v\n", typ, err)
		os.Exit(1)
	}
	return db
}

func exec(db *sql.DB, stmts ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			fmt.Fprintf(os.Stderr, "exec %q: %v\n", s, err)
			os.Exit(1)
		}
	}
}

func seedPGTables(db *sql.DB, schema string) {
	exec(db,
		fmt.Sprintf(deptDDL, schema),
		fmt.Sprintf(empDDL, schema, schema),
		fmt.Sprintf("INSERT INTO %s.dept VALUES %s", schema, strings.Join(deptRows, ",")),
		fmt.Sprintf("INSERT INTO %s.emp VALUES %s", schema, strings.Join(empRows, ",")),
	)
	seedSpecial(db, schema, "pg")
}

func seedMySQLTables(db *sql.DB, dbname string) {
	exec(db,
		fmt.Sprintf(deptDDL, dbname),
		fmt.Sprintf(empDDL, dbname, dbname),
		fmt.Sprintf("INSERT INTO %s.dept VALUES %s", dbname, strings.Join(deptRows, ",")),
		fmt.Sprintf("INSERT INTO %s.emp VALUES %s", dbname, strings.Join(empRows, ",")),
	)
	seedSpecial(db, dbname, "mysql")
}

func seed() {
	ogAdmin := open("opengaussdb", os.Getenv("OWL_E2E_OPGAUSS_ADMIN_DSN"))
	my := open("mysql", os.Getenv("OWL_E2E_MYSQL_DSN"))
	pg := open("postgres", os.Getenv("OWL_E2E_PG_DSN"))
	defer ogAdmin.Close()
	defer my.Close()
	defer pg.Close()

	exec(ogAdmin,
		"DROP SCHEMA IF EXISTS src_og CASCADE",
		"CREATE SCHEMA src_og",
		"DROP SCHEMA IF EXISTS tgt_og CASCADE",
		"CREATE SCHEMA tgt_og",
	)
	seedPGTables(ogAdmin, srcOG)
	exec(ogAdmin,
		fmt.Sprintf("GRANT USAGE ON SCHEMA src_og TO %s", ogUser),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA src_og TO %s", ogUser),
		fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA tgt_og TO %s", ogUser),
	)

	exec(my,
		"DROP DATABASE IF EXISTS src_my",
		"CREATE DATABASE src_my",
		"DROP DATABASE IF EXISTS tgt_my",
		"CREATE DATABASE tgt_my",
	)
	seedMySQLTables(my, srcMY)

	exec(pg,
		"DROP SCHEMA IF EXISTS src_pg CASCADE",
		"CREATE SCHEMA src_pg",
		"DROP SCHEMA IF EXISTS tgt_pg CASCADE",
		"CREATE SCHEMA tgt_pg",
	)
	seedPGTables(pg, srcPG)

	fmt.Println("seeded src_og+tgt_og (openGauss), src_my+tgt_my (MySQL), src_pg+tgt_pg (PostgreSQL)")
}

// seeddb <dbname> seeds dept+emp into schema src of a specific openGauss
// compat-mode db. The schema is created by ogadmin, which grants miguser
// CREATE; the tables are created as miguser so the extraction user owns them
// and information_schema exposes all constraint metadata.
func seeddb(dbname string) {
	base := os.Getenv("OWL_E2E_OPGAUSS_ADMIN_DSN")
	og := open("opengaussdb", base+" dbname="+dbname)
	exec(og,
		"DROP SCHEMA IF EXISTS src CASCADE",
		"CREATE SCHEMA src",
		"DROP SCHEMA IF EXISTS tgt CASCADE",
		"CREATE SCHEMA tgt",
		fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA src TO %s", ogUser),
		fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA tgt TO %s", ogUser),
	)
	og.Close()

	og2 := open("opengaussdb", os.Getenv("OWL_E2E_OPGAUSS_DSN")+" dbname="+dbname)
	defer og2.Close()
	seedPGTables(og2, "src")
	fmt.Println("seeded src.dept/emp in openGauss db", dbname)
}

func verify(typ, dsn, schema string) {
	db := open(typ, dsn)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 ORDER BY table_name`, schema)
	if err != nil {
		rows, err = db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
			WHERE table_schema = ? ORDER BY table_name`, schema)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tables %s: %v\n", schema, err)
		os.Exit(1)
	}
	var tables []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tables = append(tables, t)
	}
	rows.Close()

	for _, t := range tables {
		var n int
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", schema, t)
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			fmt.Fprintf(os.Stderr, "count %s.%s: %v\n", schema, t, err)
			os.Exit(1)
		}
		fmt.Printf("%s.%s: %d rows\n", schema, t, n)
	}
}

func verifydb(dbname, schema string) {
	base := os.Getenv("OWL_E2E_OPGAUSS_DSN")
	verify("opengaussdb", base+" dbname="+dbname, schema)
}

// pgmk <schema...> creates target schemas in the PostgreSQL test db.
func pgmk(schemas ...string) {
	pg := open("postgres", os.Getenv("OWL_E2E_PG_DSN"))
	defer pg.Close()
	for _, s := range schemas {
		exec(pg, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", s),
			fmt.Sprintf("CREATE SCHEMA %s", s))
	}
	fmt.Println("pg target schemas ready:", strings.Join(schemas, " "))
}

// verifySpecial prints the special table rows for fidelity checking.
func verifySpecial(typ, dsn, schema string) {
	db := open(typ, dsn)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col FROM %s.special ORDER BY id", schema))
	if err != nil {
		fmt.Fprintf(os.Stderr, "special query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var q, s, c, b, n, u sql.NullString
		var num sql.NullInt64
		var dt sql.NullTime
		if err := rows.Scan(&id, &q, &s, &c, &b, &n, &u, &num, &dt); err != nil {
			fmt.Fprintf(os.Stderr, "special scan: %v\n", err)
			os.Exit(1)
		}
		qs := "NULL"
		if q.Valid {
			qs = fmt.Sprintf("%q", q.String)
		}
		ss := "NULL"
		if s.Valid {
			ss = fmt.Sprintf("%q", s.String)
		}
		cs := "NULL"
		if c.Valid {
			cs = fmt.Sprintf("%q", c.String)
		}
		bs := "NULL"
		if b.Valid {
			bs = fmt.Sprintf("%q", b.String)
		}
		ns := "NULL"
		if n.Valid {
			ns = fmt.Sprintf("%q", n.String)
		}
		us := "NULL"
		if u.Valid {
			us = fmt.Sprintf("%q", u.String)
		}
		nums := "NULL"
		if num.Valid {
			nums = fmt.Sprintf("%d", num.Int64)
		}
		dts := "NULL"
		if dt.Valid {
			dts = dt.Time.Format("2006-01-02")
		}
		fmt.Printf("id=%d q=%s s=%s c=%s b=%s n=%s u=%s num=%s date=%s\n", id, qs, ss, cs, bs, ns, us, nums, dts)
	}
}

// seedOBOracle seeds dept/emp/special into an OceanBase Oracle-mode tenant
// under the given owner/schema (Oracle NUMBER/VARCHAR2/DATE + CHR(10)/||).
func seedOBOracle(db *sql.DB, schema string) {
	// OB Oracle has no DROP ... IF EXISTS; drop and ignore "does not exist".
	for _, st := range []string{"DROP TABLE special", "DROP TABLE emp", "DROP TABLE dept"} {
		if _, err := db.Exec(st); err != nil && !strings.Contains(strings.ToUpper(err.Error()), "00942") {
			fmt.Fprintf(os.Stderr, "drop: %v\n", err)
		}
	}
	exec(db,
		"CREATE TABLE dept (deptno NUMBER(9,0) PRIMARY KEY, dname VARCHAR2(30) NOT NULL, loc VARCHAR2(30))",
		"CREATE TABLE emp (empno NUMBER(9,0) PRIMARY KEY, ename VARCHAR2(30) NOT NULL, job VARCHAR2(20), mgr NUMBER(9,0), hiredate DATE, sal NUMBER(9,2), comm NUMBER(9,2), deptno NUMBER(9,0), CONSTRAINT fk_emp_dept FOREIGN KEY (deptno) REFERENCES dept(deptno))",
		"CREATE TABLE special (id NUMBER(9,0) PRIMARY KEY, q_col VARCHAR2(100), s_col VARCHAR2(100), c_col VARCHAR2(100), b_col VARCHAR2(100), n_col VARCHAR2(100), u_col VARCHAR2(100), num_col NUMBER(9,0), date_col DATE)",
		fmt.Sprintf("INSERT INTO dept VALUES %s", strings.Join(deptRows, ",")),
		fmt.Sprintf("INSERT INTO emp VALUES %s", strings.Join(empRowsOracle, ",")),
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (1, '\"quoted\" ''single''', '#hash @at !excl %pct', 'comma,semi;colon:', 'back\\slash', 'line1'||CHR(10)||'line2'||CHR(9)||'tab', '中文🎉', NULL, NULL)",
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (2, NULL, 'NULL', '\\N', '', '', '', NULL, NULL)",
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (3, NULL, NULL, ',a,,b,', NULL, 'l1'||CHR(10)||'l2'||CHR(10)||'l3', NULL, NULL, NULL)",
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (4, NULL, NULL, ',', NULL, CHR(10)||'lead'||CHR(10), NULL, NULL, NULL)",
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (5, NULL, NULL, 'a,b,c,d', NULL, 'trail'||CHR(10), NULL, NULL, NULL)",
		"INSERT INTO special (id,q_col,s_col,c_col,b_col,n_col,u_col,num_col,date_col) VALUES (6, NULL, NULL, ',', NULL, CHR(10), NULL, NULL, NULL)",
	)
	// OB Oracle mode requires explicit COMMIT for DML (Oracle semantics).
	exec(db, "COMMIT")
	fmt.Println("seeded OB Oracle", schema)
}

// seedOBMySQL seeds dept/emp/special into an OceanBase MySQL-mode tenant db.
func seedOBMySQL(db *sql.DB, dbname string) {
	exec(db,
		fmt.Sprintf("DROP TABLE IF EXISTS %s.emp", dbname),
		fmt.Sprintf("DROP TABLE IF EXISTS %s.dept", dbname),
		fmt.Sprintf("DROP TABLE IF EXISTS %s.special", dbname),
		fmt.Sprintf(deptDDL, dbname),
		fmt.Sprintf(empDDL, dbname, dbname),
		fmt.Sprintf("INSERT INTO %s.dept VALUES %s", dbname, strings.Join(deptRows, ",")),
		fmt.Sprintf("INSERT INTO %s.emp VALUES %s", dbname, strings.Join(empRows, ",")),
	)
	seedSpecial(db, dbname, "mysql")
	fmt.Println("seeded OB MySQL", dbname)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seed | seeddb <dbname> | verify <type> <dsn> <schema> | verifydb <dbname> <schema>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "seed":
		seed()
	case "seeddb":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: seeddb <dbname>")
			os.Exit(2)
		}
		seeddb(os.Args[2])
	case "verify":
		if len(os.Args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: verify <type> <dsn> <schema>")
			os.Exit(2)
		}
		verify(os.Args[2], os.Args[3], os.Args[4])
	case "verifydb":
		if len(os.Args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: verifydb <dbname> <schema>")
			os.Exit(2)
		}
		verifydb(os.Args[2], os.Args[3])
	case "verifyspecial":
		if len(os.Args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: verifyspecial <type> <dsn> <schema>")
			os.Exit(2)
		}
		verifySpecial(os.Args[2], os.Args[3], os.Args[4])
	case "seedob":
		if len(os.Args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: seedob")
			os.Exit(2)
		}
		seedob()
	case "pgmk":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: pgmk <schema...>")
			os.Exit(2)
		}
		pgmk(os.Args[2:]...)
	default:
		fmt.Fprintf(os.Stderr, "unknown cmd %q\n", os.Args[1])
		os.Exit(2)
	}
}

// seedob seeds the reusable dataset into the OB Oracle tenant (MIGSRC) and OB
// MySQL tenant (ogtest db).
func seedob() {
	// OB Oracle: connect directly as MIGSRC (the owner/schema).
	obOra := open("oceanbase-oracle", os.Getenv("OWL_E2E_OB_ORACLE_MIGSRC_DSN"))
	obMy := open("oceanbase-mysql", os.Getenv("OWL_E2E_OB_MYSQL_DSN"))
	defer obOra.Close()
	defer obMy.Close()
	seedOBOracle(obOra, "MIGSRC")
	seedOBMySQL(obMy, "ogtest")
	// verify counts in the same process
	ctx, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	for _, t := range []string{"dept", "emp", "special"} {
		var n int
		if err := obOra.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			fmt.Printf("OB Oracle %s count ERR: %v\n", t, err)
		} else {
			fmt.Printf("OB Oracle %s rows=%d\n", t, n)
		}
	}
}
