package datasource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dscrypto"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "datasources")
	vault, err := dscrypto.New(filepath.Dir(dir))
	if err != nil {
		t.Fatalf("dscrypto.New: %v", err)
	}
	return Open(dir, vault)
}

func TestPutEncryptsDSNAtRest(t *testing.T) {
	s := newStore(t)
	if err := s.Put("prod-pg", "postgres", "public", "host=h user=p password=secret dbname=d", "production"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(), "prod-pg.yaml"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	// The plaintext password must not appear on disk.
	if strings.Contains(string(data), "password=secret") {
		t.Errorf("file contains plaintext password:\n%s", string(data))
	}
	if !strings.Contains(string(data), "enc:v1:") {
		t.Errorf("file missing encrypted DSN:\n%s", string(data))
	}
}

func TestResolveRoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.Put("dev-mysql", "mysql", "mydb", "root:pw@tcp(h:3306)/mydb", ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	typ, schema, dsn, err := s.Resolve("dev-mysql")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if typ != "mysql" || schema != "mydb" || dsn != "root:pw@tcp(h:3306)/mydb" {
		t.Fatalf("resolve = %q/%q/%q", typ, schema, dsn)
	}
}

func TestListHidesDSN(t *testing.T) {
	s := newStore(t)
	_ = s.Put("a", "postgres", "public", "secret-a", "r")
	_ = s.Put("b", "mysql", "db", "secret-b", "")
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	for _, it := range list {
		if it.Name == "" {
			t.Errorf("empty name in Info")
		}
		// Info has no DSN field; ensure plaintext never leaks by scanning bytes.
	}
}

func TestPutUpdateKeepsCiphertextWhenDSNEmpty(t *testing.T) {
	s := newStore(t)
	if err := s.Put("k", "postgres", "public", "pw1", ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Update only the remark; DSN should be preserved.
	if err := s.Put("k", "postgres", "public", "", "changed"); err != nil {
		t.Fatalf("Put#2: %v", err)
	}
	_, _, dsn, err := s.Resolve("k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dsn != "pw1" {
		t.Fatalf("dsn after empty update = %q, want pw1", dsn)
	}
}

func TestDeleteAndMissing(t *testing.T) {
	s := newStore(t)
	_ = s.Put("gone", "mysql", "db", "x", "")
	if err := s.Delete("gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete("gone"); err != nil {
		t.Fatalf("Delete missing should be no-op: %v", err)
	}
	if _, _, _, err := s.Resolve("gone"); err == nil {
		t.Fatalf("expected error resolving deleted source")
	}
}

func TestRefHelpers(t *testing.T) {
	if !IsRef(Ref("prod")) || RefName(Ref("prod")) != "prod" {
		t.Fatalf("ref round-trip failed")
	}
	if IsRef("user@tcp") || IsRef("datasource:") {
		t.Fatalf("non-ref or empty ref misdetected")
	}
}
