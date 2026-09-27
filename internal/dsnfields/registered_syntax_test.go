package dsnfields

import "testing"

func TestRegisteredSyntaxes(t *testing.T) {
	RegisterSyntaxes(map[string]string{
		"orax": SyntaxURL,
		"myx":  SyntaxMySQLTCP,
		"ttx":  SyntaxKV,
	})

	// url 语法 + scheme 保留
	f, err := Decompose("orax", "oracle://u:p@h1:1521/SVC")
	if err != nil {
		t.Fatal(err)
	}
	if f.Host != "h1" || f.Port != "1521" || f.Username != "u" || f.Password != "p" || f.Database != "SVC" {
		t.Fatalf("url decompose: %+v", f)
	}
	built, err := Build("orax", *f, "oracle://u:p@h1:1521/SVC")
	if err != nil {
		t.Fatal(err)
	}
	if built[:9] != "oracle://" {
		t.Fatalf("url build must keep the native scheme, got %q", built)
	}

	// mysql-tcp 语法
	f2, err := Decompose("myx", "root:pw@tcp(127.0.0.1:3306)/testdb")
	if err != nil {
		t.Fatal(err)
	}
	if f2.Host != "127.0.0.1" || f2.Port != "3306" || f2.Database != "testdb" {
		t.Fatalf("mysql-tcp decompose: %+v", f2)
	}

	// kv 语法(分号,别名归一,大小写不敏感)
	f3, err := Decompose("ttx", "TTC_SERVER=h1;TCP_PORT=6625;TTC_SERVER_DSN=sampledb;uid=u;pwd=p")
	if err != nil {
		t.Fatal(err)
	}
	if f3.Host != "h1" || f3.Port != "6625" || f3.Username != "u" || f3.Password != "p" || f3.Database != "sampledb" {
		t.Fatalf("kv decompose: %+v", f3)
	}
	built3, err := Build("ttx", *f3, "")
	if err != nil {
		t.Fatal(err)
	}
	if built3 == "" {
		t.Fatal("kv build produced empty dsn")
	}

	// 未注册语法名的 type 被忽略,回落内置推断
	RegisterSyntaxes(map[string]string{"bogus": "no-such-syntax"})
	f4, err := Decompose("bogus", "host=h port=1 dbname=d")
	if err != nil {
		t.Fatal(err)
	}
	if f4.Host != "h" { // pg-kv 默认语法
		t.Fatalf("unregistered syntax must fall back to built-in inference: %+v", f4)
	}
}
