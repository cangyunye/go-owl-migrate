package dbconn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/owljdbc"
)

// linkedSet fakes driverLinked for decision-table tests: dbconn itself links
// no database/sql drivers — product binaries register them via internal/cmd
// build tags, so availability must be injected.
func linkedSet(names ...string) func(string) bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(name string) bool { return set[name] }
}

func TestResolveChannelNativeFirst(t *testing.T) {
	fallbacks := map[string]string{}
	FallbackHook = func(dbType, reason string) { fallbacks[dbType] = reason }
	defer func() { FallbackHook = nil }()

	cases := []struct {
		name         string
		cfg          config.DBConfig
		linked       []string
		want         string
		wantFallback string // "" = hook must not fire
	}{
		{"empty channel is native", config.DBConfig{Type: "mysql", Channel: ""}, []string{"mysql"}, ChannelNative, ""},
		{"explicit native", config.DBConfig{Type: "mysql", Channel: ChannelNative}, []string{}, ChannelNative, ""},
		{"agent forced", config.DBConfig{Type: "mysql", Channel: ChannelAgent}, []string{"mysql"}, ChannelAgent, ""},
		{"auto keeps linked native", config.DBConfig{Type: "mysql", Channel: ChannelAuto}, []string{"mysql"}, ChannelNative, ""},
		{"auto falls back when driver not compiled", config.DBConfig{Type: "oceanbase-oracle", Channel: ChannelAuto}, []string{"mysql", "postgres", "oracle"}, ChannelAgent, "native driver not compiled into this binary"},
		{"auto keeps native when compiled", config.DBConfig{Type: "oceanbase-oracle", Channel: ChannelAuto}, []string{"oboracle"}, ChannelNative, ""},
		{"auto falls back for catalog-only type", config.DBConfig{Type: "dm", Channel: ChannelAuto}, []string{"mysql"}, ChannelAgent, "native driver not available"},
		{"auto keeps native error when catalog lacks type", config.DBConfig{Type: "sqlite3", Channel: ChannelAuto}, []string{}, ChannelNative, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveChannel(tc.cfg, linkedSet(tc.linked...))
			if err != nil {
				t.Fatalf("resolveChannel: %v", err)
			}
			if got != tc.want {
				t.Fatalf("channel = %q, want %q", got, tc.want)
			}
			if tc.wantFallback == "" {
				if _, fired := fallbacks[tc.cfg.Type]; fired {
					t.Fatalf("fallback hook fired unexpectedly: %q", fallbacks[tc.cfg.Type])
				}
			} else if fallbacks[tc.cfg.Type] != tc.wantFallback {
				t.Fatalf("fallback reason = %q, want %q", fallbacks[tc.cfg.Type], tc.wantFallback)
			}
			delete(fallbacks, tc.cfg.Type)
		})
	}
}

func TestResolveChannelInvalid(t *testing.T) {
	_, err := resolveChannel(config.DBConfig{Type: "mysql", Channel: "always-jvm"}, linkedSet("mysql"))
	if err == nil || !strings.Contains(err.Error(), "invalid channel") {
		t.Fatalf("err = %v, want invalid channel error", err)
	}
}

func TestBuildAgentConfigProfiles(t *testing.T) {
	dir := t.TempDir()
	jars := []string{
		"owl-agent.jar",
		"mysql-connector-j-8.0.33.jar",
		"oceanbase-client-2.4.1.jar",
		"postgresql-42.7.13.jar",
		"ojdbc11.jar",
		"DmJdbcDriver18.jar",
		"kingbase8-9.0.jar",
		"ttjdbc16.jar",
	}
	for _, jar := range jars {
		if err := os.WriteFile(filepath.Join(dir, jar), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ag := config.AgentConfig{JarsDir: dir}

	cases := []struct {
		name       string
		cfg        config.DBConfig
		wantURL    string
		wantDriver string
		wantFamily string
		wantUser   string
	}{
		{
			"mysql wire dsn",
			config.DBConfig{Type: "mysql", DSN: "user:pw@tcp(db1:3306)/app?parseTime=true", Agent: ag},
			"jdbc:mysql://db1:3306/app?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8",
			"com.mysql.cj.jdbc.Driver", "mysql", "user",
		},
		{
			"oceanbase-mysql tenant dsn",
			config.DBConfig{Type: "oceanbase-mysql", DSN: "root:pw@tcp(127.0.0.1:2881)/app", Agent: ag},
			"jdbc:oceanbase://127.0.0.1:2881/app?useSSL=false&characterEncoding=UTF-8",
			"com.oceanbase.jdbc.Driver", "mysql", "root",
		},
		{
			"oceanbase-oracle keeps user@tenant",
			config.DBConfig{Type: "oceanbase-oracle", DSN: "oracle://sys%40oratest:pw@10.0.0.9:2881/oratest", Agent: ag},
			"jdbc:oceanbase://10.0.0.9:2881?useSSL=false",
			"com.oceanbase.jdbc.Driver", "oracle", "sys@oratest",
		},
		{
			"postgres url dsn",
			config.DBConfig{Type: "postgres", DSN: "postgres://u:p@h1:5432/db", Agent: ag},
			"jdbc:postgresql://h1:5432/db?stringtype=unspecified",
			"org.postgresql.Driver", "postgres", "u",
		},
		{
			"postgres keyword dsn",
			config.DBConfig{Type: "postgres", DSN: "host=h2 port=5433 user=u2 password=p2 dbname=db2", Agent: ag},
			"jdbc:postgresql://h2:5433/db2?stringtype=unspecified",
			"org.postgresql.Driver", "postgres", "u2",
		},
		{
			"oracle thin url",
			config.DBConfig{Type: "oracle", DSN: "oracle://scott:tiger@oradb:1521/ORCL", Agent: ag},
			"jdbc:oracle:thin:@//oradb:1521/ORCL",
			"oracle.jdbc.OracleDriver", "oracle", "scott",
		},
		{
			"dm host only dsn",
			config.DBConfig{Type: "dm", DSN: "dm://SYSDBA:pw@10.0.0.5", Agent: ag},
			"jdbc:dm://10.0.0.5",
			"dm.jdbc.driver.DmDriver", "oracle", "SYSDBA",
		},
		{
			"kingbase kv dsn",
			config.DBConfig{Type: "kingbase", DSN: "host=kb user=system password=p dbname=test", Agent: ag},
			"jdbc:kingbase8://kb/test",
			"com.kingbase8.Driver", "postgres", "system",
		},
		{
			"timesten dsn verbatim without decomposed credentials",
			config.DBConfig{Type: "timesten", DSN: "jdbc:timesten:client:dsn=tt_dsn;uid=u;pwd=p", Agent: ag},
			"jdbc:timesten:client:dsn=tt_dsn;uid=u;pwd=p",
			"com.timesten.jdbc.TimesTenDriver", "oracle", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac, err := buildAgentConfig(tc.cfg)
			if err != nil {
				t.Fatalf("buildAgentConfig: %v", err)
			}
			if ac.URL != tc.wantURL {
				t.Fatalf("url = %q, want %q", ac.URL, tc.wantURL)
			}
			if ac.DriverClass != tc.wantDriver {
				t.Fatalf("driverClass = %q, want %q", ac.DriverClass, tc.wantDriver)
			}
			if ac.Family != tc.wantFamily {
				t.Fatalf("family = %q, want %q", ac.Family, tc.wantFamily)
			}
			if ac.User != tc.wantUser {
				t.Fatalf("user = %q, want %q", ac.User, tc.wantUser)
			}
			if ac.AgentJar != filepath.Join(dir, "owl-agent.jar") {
				t.Fatalf("agentJar = %q, want %q", ac.AgentJar, filepath.Join(dir, "owl-agent.jar"))
			}
			if len(ac.Classpath) == 0 {
				t.Fatalf("classpath empty, want driver jar")
			}
			for _, jar := range ac.Classpath {
				if !strings.HasPrefix(jar, dir+string(filepath.Separator)) {
					t.Fatalf("classpath jar %q resolved outside jars_dir %q", jar, dir)
				}
			}
		})
	}
}

func TestBuildAgentConfigErrors(t *testing.T) {
	dir := t.TempDir()
	for _, jar := range []string{"owl-agent.jar", "postgresql-42.7.13.jar"} {
		if err := os.WriteFile(filepath.Join(dir, jar), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unsupported type", func(t *testing.T) {
		_, err := buildAgentConfig(config.DBConfig{Type: "yashandb", DSN: "x", Agent: config.AgentConfig{JarsDir: dir}})
		if err == nil || !strings.Contains(err.Error(), "no catalog profile") {
			t.Fatalf("err = %v, want catalog profile error (module wording)", err)
		}
	})

	t.Run("missing driver jar", func(t *testing.T) {
		_, err := buildAgentConfig(config.DBConfig{Type: "mysql", DSN: "u:p@tcp(h:3306)/d", Agent: config.AgentConfig{JarsDir: dir}})
		if err == nil || !strings.Contains(err.Error(), "no jar matching mysql-connector-j-*.jar") {
			t.Fatalf("err = %v, want missing jar error", err)
		}
	})

	t.Run("missing agent jar", func(t *testing.T) {
		noAgent := t.TempDir()
		if err := os.WriteFile(filepath.Join(noAgent, "postgresql-42.7.13.jar"), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := buildAgentConfig(config.DBConfig{Type: "postgres", DSN: "postgres://u:p@h:5432/d", Agent: config.AgentConfig{JarsDir: noAgent}})
		if err == nil || !strings.Contains(err.Error(), "owl-agent") {
			t.Fatalf("err = %v, want missing agent jar error", err)
		}
	})

	t.Run("empty dsn", func(t *testing.T) {
		_, err := buildAgentConfig(config.DBConfig{Type: "postgres", DSN: "   ", Agent: config.AgentConfig{JarsDir: dir}})
		if err == nil || !strings.Contains(err.Error(), "requires a dsn with a host") {
			t.Fatalf("err = %v, want missing host error", err)
		}
	})
}

func TestResolveAgentJarExplicitPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom", "owl-agent-1.0.jar")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := owljdbc.ResolveAgentJar([]string{dir}, custom)
	if err != nil {
		t.Fatalf("resolveAgentJar: %v", err)
	}
	if got != custom {
		t.Fatalf("agentJar = %q, want %q", got, custom)
	}
	if _, err := owljdbc.ResolveAgentJar([]string{dir}, filepath.Join(dir, "nope.jar")); err == nil {
		t.Fatalf("missing explicit agent jar must error")
	}
}
