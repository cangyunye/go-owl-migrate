//go:build ob

package extractor

import "testing"

func TestOceanBaseOracleWireQuerierRegistered(t *testing.T) {
	q, err := Get("oceanbase-oracle-wire")
	if err != nil {
		t.Fatalf("Get(oceanbase-oracle-wire): %v", err)
	}
	if q.Type() != "oceanbase-oracle-wire" {
		t.Errorf("querier type = %q", q.Type())
	}
	// TNS-style oceanbase-oracle still routes to the native ":N" querier.
	if _, err := Get(normalizeDBType("oceanbase-oracle")); err != nil {
		t.Fatalf("normalized oceanbase-oracle lookup: %v", err)
	}
	if _, err := Get("oceanbase-mysql"); err != nil {
		t.Fatalf("Get(oceanbase-mysql): %v", err)
	}
}
