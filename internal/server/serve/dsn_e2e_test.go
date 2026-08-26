//go:build e2e

package serve

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// TestE2E_StructuredFill_ConnectsToLiveDB exercises the structured-fill flow
// end to end: for each live database family, a DSN is assembled from the
// structured connection components (user/password/host/port/db) using the same
// builder template the modal sends to the browser, then it is confirmed to be
// reachable through the backend /api/v1/conn/test endpoint.
//
// Requires the compose DBs (testdata/db/docker-compose.yaml) to be up.
func TestE2E_StructuredFill_ConnectsToLiveDB(t *testing.T) {
	ts, _, _ := newE2ERig(t)
	meta := service.DSNComponentMeta()
	families := service.DSNFamilies()

	cases := []struct {
		dbType string
		want   string
		comps  map[string]string
	}{
		{
			dbType: "mysql",
			want:   "root:root123456@tcp(127.0.0.1:3306)/default_db",
			comps:  map[string]string{"user": "root", "password": "root123456", "host": "127.0.0.1", "port": "3306", "db": "default_db"},
		},
		{
			dbType: "postgres",
			want:   "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable",
			comps:  map[string]string{"user": "postgres", "password": "postgres123", "host": "127.0.0.1", "port": "5432", "db": "postgres_db"},
		},
		{
			dbType: "oracle",
			want:   "oracle://scott:tiger@127.0.0.1:1521/XEPDB1",
			comps:  map[string]string{"user": "scott", "password": "tiger", "host": "127.0.0.1", "port": "1521", "db": "XEPDB1"},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.dbType, func(t *testing.T) {
			fam := families[c.dbType]
			m, ok := meta[fam]
			if !ok {
				t.Fatalf("%s: no DSN family meta for family %q", c.dbType, fam)
			}

			// Assemble the DSN the same way the modal does.
			got := applyDSNBuilder(m.Builder, c.comps, m.URLStyle)
			if got != c.want {
				t.Fatalf("%s: assembled DSN = %q, want %q", c.dbType, got, c.want)
			}

			// The assembled DSN must actually connect through the backend.
			body := fmt.Sprintf(`{"type":%q,"dsn":%q}`, c.dbType, got)
			resp, respBody := e2ePost(t, ts, "/api/v1/conn/test", body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: conn/test status %d, body %v", c.dbType, resp.StatusCode, respBody)
			}
			if ok, _ := respBody["ok"].(bool); !ok {
				t.Fatalf("%s: connect failed: %v", c.dbType, respBody["error"])
			}
			t.Logf("%s: connected OK via assembled DSN", c.dbType)
		})
	}
}

// applyDSNBuilder substitutes {key} placeholders in a builder template with the
// component values, URL-encoding user/password/db for URL-style families,
// mirroring the structured-fill modal in web/static/js/config.js.
func applyDSNBuilder(builder string, comps map[string]string, urlStyle bool) string {
	out := builder
	for k, v := range comps {
		if urlStyle && (k == "user" || k == "password" || k == "db") {
			v = encodeURIComponentJS(v)
		}
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// encodeURIComponentJS encodes a string using the same safeset as the
// frontend's encodeURIComponent, so the assembled DSN is byte-identical.
func encodeURIComponentJS(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(safe, r) {
			b.WriteRune(r)
		} else {
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}
