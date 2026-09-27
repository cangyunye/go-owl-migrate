package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/owljdbc"
)

func TestRegisterOwlJDBCProfiles_LoadFile(t *testing.T) {
	yamlText := `
owljdbc:
  profiles:
    orax:
      driver_class: oracle.jdbc.OracleDriver
      jar_globs: ["ojdbc*.jar"]
      family: oracle
      url_template: "jdbc:oracle:thin:@//{host}:{port}/{database}"
      dsn_syntax: url
general:
  log_level: info
metadata:
  type: database
source:
  type: postgres
  dsn: "host=127.0.0.1 port=5432 user=u password=p dbname=d sslmode=disable"
ddl:
  target_dialect: postgres
`
	path := filepath.Join(t.TempDir(), "migrate.yaml")
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load with owljdbc.profiles: %v", err)
	}
	if got := owljdbc.ProfileFamily("ORAX"); got != "oracle" {
		t.Fatalf("ProfileFamily = %q, want oracle (registration must run inside Load)", got)
	}
}

func TestRegisterOwlJDBCProfiles_InvalidSpecFails(t *testing.T) {
	cfg := &Config{
		OwlJDBC: OwlJDBCConfig{Profiles: map[string]OwlJDBCProfileSpec{
			"bad": {
				DriverClass: "x",
				JarGlobs:    []string{"a.jar"},
				Family:      "bogus",
				URLTemplate: "jdbc:// {host}",
			},
		}},
	}
	if err := cfg.RegisterOwlJDBCProfiles(); err == nil {
		t.Fatal("invalid spec must fail registration")
	}
	if owljdbc.HasProfile("bad") {
		t.Fatal("failed spec must not register")
	}
}

func TestOwlJDBCProfiles_SpecRoundTrip(t *testing.T) {
	cfg := &Config{
		OwlJDBC: OwlJDBCConfig{Profiles: map[string]OwlJDBCProfileSpec{
			"orax": {
				DriverClass: "oracle.jdbc.OracleDriver",
				JarGlobs:    []string{"ojdbc*.jar"},
				Family:      "oracle",
				URLTemplate: "jdbc:oracle:thin:@//{host}:{port}/{database}",
				DSNSyntax:   "url",
			},
		}},
	}
	if err := cfg.RegisterOwlJDBCProfiles(); err != nil {
		t.Fatalf("RegisterOwlJDBCProfiles: %v", err)
	}
	if got := owljdbc.ProfileFamily("ORAX"); got != "oracle" {
		t.Fatalf("ProfileFamily = %q, want oracle", got)
	}
	// 幂等:重复注册不报错。
	if err := cfg.RegisterOwlJDBCProfiles(); err != nil {
		t.Fatalf("second register: %v", err)
	}
	// yaml 往返不丢字段。
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	p := back.OwlJDBC.Profiles["orax"]
	if p.URLTemplate == "" || p.Family != "oracle" || len(p.JarGlobs) != 1 {
		t.Fatalf("yaml round trip lost spec fields: %+v", p)
	}
}
