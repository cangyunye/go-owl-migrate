package serve

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// TestConnIdentityOf_DSNGrammars covers the endpoints the UI has to tell
// apart: same dialect, different machine/database/schema/user.
func TestConnIdentityOf_DSNGrammars(t *testing.T) {
	srv := newTestServer(t)

	tests := []struct {
		name    string
		cfg     config.DBConfig
		want    connIdentity
		unwants []string
	}{
		{
			name: "postgres keyword DSN",
			cfg: config.DBConfig{
				Type:   "postgres",
				DSN:    "host=10.0.0.9 port=5432 user=appuser password=secret dbname=appdb sslmode=disable",
				Schema: "public",
			},
			want: connIdentity{
				Type: "postgres", User: "appuser", Host: "10.0.0.9", Port: "5432",
				Database: "appdb", Schema: "public", Label: "appuser@10.0.0.9:5432/appdb",
			},
			unwants: []string{"secret"},
		},
		{
			name: "openGauss MySQL-compat DSN is parsed like its PG wire",
			cfg: config.DBConfig{
				Type:   "panweidb-mysql",
				DSN:    "host=10.0.0.5 port=5432 user=miguser password=pw dbname=og_mysql sslmode=disable",
				Schema: "public",
			},
			want: connIdentity{
				Type: "panweidb-mysql", User: "miguser", Host: "10.0.0.5", Port: "5432",
				Database: "og_mysql", Schema: "public", Label: "miguser@10.0.0.5:5432/og_mysql",
			},
			unwants: []string{"pw"},
		},
		{
			name: "mysql go-sql-driver DSN",
			cfg: config.DBConfig{
				Type:   "mysql",
				DSN:    "appuser:secret@tcp(10.0.0.7:3306)/appdb?charset=utf8mb4",
				Schema: "appdb",
			},
			want: connIdentity{
				Type: "mysql", User: "appuser", Host: "10.0.0.7", Port: "3306",
				Database: "appdb", Schema: "appdb", Label: "appuser@10.0.0.7:3306/appdb",
			},
			unwants: []string{"secret"},
		},
		{
			name: "oracle URL DSN keeps the tenant-qualified user",
			cfg: config.DBConfig{
				Type:   "oceanbase-oracle",
				DSN:    "oracle://sys@obtenant:secret@10.0.0.3:1521/ORCL",
				Schema: "APPUSER",
			},
			want: connIdentity{
				Type: "oceanbase-oracle", User: "sys@obtenant", Host: "10.0.0.3",
				Port: "1521", Database: "ORCL", Schema: "APPUSER",
				Label: "sys@obtenant@10.0.0.3:1521/ORCL",
			},
			unwants: []string{"secret"},
		},
		{
			name: "embedded database shows its file path",
			cfg:  config.DBConfig{Type: "sqlite3", DSN: "/data/app/db.sqlite", Schema: "main"},
			want: connIdentity{
				Type: "sqlite3", Database: "/data/app/db.sqlite", Schema: "main",
				Label: "/data/app/db.sqlite",
			},
		},
		{
			name: "unparseable DSN degrades to type and schema",
			cfg:  config.DBConfig{Type: "postgres", DSN: "not a dsn at all", Schema: "public"},
			want: connIdentity{Type: "postgres", Schema: "public"},
		},
		{
			name: "missing DSN keeps the type",
			cfg:  config.DBConfig{Type: "mysql", Schema: "appdb"},
			want: connIdentity{Type: "mysql", Schema: "appdb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.connIdentityOf(tt.cfg)
			if got != tt.want {
				t.Errorf("connIdentityOf() = %+v, want %+v", got, tt.want)
			}
			for _, secret := range tt.unwants {
				if secret != "" && strings.Contains(got.Label+got.User+got.Host+got.Database, secret) {
					t.Errorf("identity leaked %q: %+v", secret, got)
				}
			}
		})
	}
}

// TestConnIdentityOf_DataSourceRef asserts a stored profile is expanded (its
// schema fills an empty configured one) and that its name travels with the
// identity so the UI can name the profile.
func TestConnIdentityOf_DataSourceRef(t *testing.T) {
	srv := newTestServer(t)
	srv.dataSourcesDir = t.TempDir() + "/datasources"

	store, err := srv.dsStore()
	if err != nil {
		t.Fatalf("dsStore: %v", err)
	}
	if err := store.Put("prod-panwei", "panweidb-mysql", "public",
		"host=10.0.0.5 port=5432 user=miguser password=pw dbname=og_mysql sslmode=disable", ""); err != nil {
		t.Fatalf("put profile: %v", err)
	}

	got := srv.connIdentityOf(config.DBConfig{Type: "panweidb-mysql", DSN: "datasource:prod-panwei"})
	want := connIdentity{
		Type: "panweidb-mysql", Ref: "prod-panwei", User: "miguser", Host: "10.0.0.5",
		Port: "5432", Database: "og_mysql", Schema: "public",
		Label: "miguser@10.0.0.5:5432/og_mysql",
	}
	if got != want {
		t.Errorf("connIdentityOf() = %+v, want %+v", got, want)
	}
	if strings.Contains(got.Label, "pw") {
		t.Errorf("identity leaked the stored password: %+v", got)
	}
}

// TestHandler_ConfigStatus_CarriesEndpointIdentity pins the wire shape the
// pipeline board and the topbar read.
func TestHandler_ConfigStatus_CarriesEndpointIdentity(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg = &config.Config{
		Source: config.DBConfig{Type: "panweidb-mysql", Schema: "public",
			DSN: "host=10.0.0.5 port=5432 user=miguser password=pw dbname=og_mysql sslmode=disable"},
		Target: config.DBConfig{Type: "mysql", Schema: "appdb",
			DSN: "appuser:secret@tcp(10.0.0.7:3306)/appdb"},
	}

	w := doGet(t, srv, "/api/v1/config/status")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "pw") || strings.Contains(body, "secret") {
		t.Errorf("status body leaked a password: %s", body)
	}
	for _, want := range []string{
		`"label":"miguser@10.0.0.5:5432/og_mysql"`,
		`"label":"appuser@10.0.0.7:3306/appdb"`,
		`"schema":"public"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status body missing %s: %s", want, body)
		}
	}
}
