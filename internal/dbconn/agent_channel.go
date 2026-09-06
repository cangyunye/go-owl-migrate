package dbconn

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/agent"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// Channel values accepted by config.DBConfig.Channel.
const (
	ChannelNative = "native"
	ChannelAgent  = "agent"
	ChannelAuto   = "auto"
)

// FallbackHook, when set, is notified when the auto channel selects the agent
// channel in place of an unavailable native driver. cmd wires it to the zap
// logger so "why did a JVM start" stays traceable.
var FallbackHook func(dbType, reason string)

// agentProfile describes how to reach one database type through the owljdbc
// channel: the JDBC driver class, how the JDBC URL is built from the native
// DSN fields, and which driver jar patterns the sidecar classpath needs.
// Profiles mirror the §11 registration matrix of
// docs/plans/2026-09-05-jdbc-agent-architecture.md; entries not yet exercised
// against a live database are marked registered-untested in their comment.
type agentProfile struct {
	driverClass string
	family      string // owljdbc connection family: mysql | oracle | postgres
	jarGlobs    []string
	urlFromDSN  bool // true: the configured DSN is already a JDBC URL, use verbatim
	buildURL    func(f dsnfields.Fields) (string, error)
}

// jdbcHost renders host[:port], omitting the port when the DSN has none.
func jdbcHost(f dsnfields.Fields) string {
	if f.Port == "" {
		return f.Host
	}
	return f.Host + ":" + f.Port
}

var agentProfiles = map[string]agentProfile{
	// 单 jar 双租户（已实测）：同一 driverClass 覆盖 MySQL 与 Oracle 兼容租户。
	"oceanbase-mysql": {
		driverClass: "com.oceanbase.jdbc.Driver",
		family:      "mysql",
		jarGlobs:    []string{"oceanbase-client-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:oceanbase://%s/%s?useSSL=false&characterEncoding=UTF-8", jdbcHost(f), f.Database), nil
		},
	},
	"oceanbase-oracle": {
		driverClass: "com.oceanbase.jdbc.Driver",
		family:      "oracle",
		jarGlobs:    []string{"oceanbase-client-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:oceanbase://%s?useSSL=false", jdbcHost(f)), nil
		},
	},
	"mysql": {
		driverClass: "com.mysql.cj.jdbc.Driver",
		family:      "mysql",
		jarGlobs:    []string{"mysql-connector-j-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:mysql://%s/%s?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8", jdbcHost(f), f.Database), nil
		},
	},
	"postgres": {
		driverClass: "org.postgresql.Driver",
		family:      "postgres",
		jarGlobs:    []string{"postgresql-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:postgresql://%s/%s", jdbcHost(f), f.Database), nil
		},
	},
	// 以下为登记未测 profile（对应数据库未接入验证）：driverClass/URL 来自
	// 架构文档 §11 与厂商公开文档，首次接入时按实测修订。
	"oracle": {
		driverClass: "oracle.jdbc.OracleDriver",
		family:      "oracle",
		jarGlobs:    []string{"ojdbc*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:oracle:thin:@//%s/%s", jdbcHost(f), f.Database), nil
		},
	},
	"goldendb-mysql": {
		driverClass: "com.mysql.cj.jdbc.Driver",
		family:      "mysql",
		jarGlobs:    []string{"mysql-connector-j-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:mysql://%s/%s?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8", jdbcHost(f), f.Database), nil
		},
	},
	"dm": {
		driverClass: "dm.jdbc.driver.DmDriver",
		family:      "oracle",
		jarGlobs:    []string{"DmJdbcDriver*.jar", "dm-jdbc-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:dm://%s", jdbcHost(f)), nil
		},
	},
	"kingbase": {
		driverClass: "com.kingbase8.Driver",
		family:      "postgres",
		jarGlobs:    []string{"kingbase8-*.jar"},
		buildURL: func(f dsnfields.Fields) (string, error) {
			return fmt.Sprintf("jdbc:kingbase8://%s/%s", jdbcHost(f), f.Database), nil
		},
	},
	"timesten": {
		driverClass: "com.timesten.jdbc.TimesTenDriver",
		family:      "oracle",
		// TimesTen DSN 是完整 JDBC URL（jdbc:timesten:client:dsn=...），原样透传。
		urlFromDSN: true,
		jarGlobs:   []string{"ttjdbc*.jar"},
	},
}

// resolveChannel decides which channel serves cfg under the native-first
// policy. linked reports whether a database/sql driver name is compiled into
// this binary (pass driverLinked from production code). "auto" falls back to
// the agent channel only when the native driver is unavailable — unknown type
// or not compiled in — and the owljdbc catalog covers the type; connection
// errors never trigger fallback (stage-2 plan §2.3).
func resolveChannel(cfg config.DBConfig, linked func(string) bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Channel)) {
	case "", ChannelNative:
		return ChannelNative, nil
	case ChannelAgent:
		return ChannelAgent, nil
	case ChannelAuto:
		// fall through to auto logic below
	default:
		return "", fmt.Errorf("invalid channel %q: want native, agent, or auto", cfg.Channel)
	}

	t := registry.Normalize(strings.ToLower(strings.TrimSpace(cfg.Type)))
	if _, known := knownTypes[t]; known {
		if drv, ok := nativeDriverName(t); ok && linked(drv) {
			return ChannelNative, nil
		}
	}
	if _, ok := agentProfiles[t]; ok {
		if FallbackHook != nil {
			reason := "native driver not available"
			if _, known := knownTypes[t]; known {
				reason = "native driver not compiled into this binary"
			}
			FallbackHook(t, reason)
		}
		return ChannelAgent, nil
	}
	// No owljdbc coverage: keep the native path so its exact error
	// ("unsupported database type" / "rebuild with -tags <tag>") surfaces.
	return ChannelNative, nil
}

// nativeDriverName reports which database/sql driver the native path would
// use for a known type, mirroring openNative's selection: oceanbase-oracle
// bypasses driverName because its URL DSN is rewritten for the obconnector-go
// fork (driver "oboracle"), while every other type goes through driverName.
func nativeDriverName(t string) (string, bool) {
	if t == "oceanbase-oracle" {
		return "oboracle", true
	}
	drv, err := driverName(t)
	return drv, err == nil
}

// buildAgentConfig translates a DBConfig into an owljdbc connection config
// using the type catalog: the native DSN is decomposed into fields, rebuilt as
// a JDBC URL, and driver jars are resolved from the configured search dirs.
func buildAgentConfig(cfg config.DBConfig) (agent.Config, error) {
	t := registry.Normalize(strings.ToLower(strings.TrimSpace(cfg.Type)))
	prof, ok := agentProfiles[t]
	if !ok {
		return agent.Config{}, fmt.Errorf("agent channel does not support database type %q: no owljdbc catalog profile", cfg.Type)
	}
	f, err := dsnfields.Decompose(t, cfg.DSN)
	if err != nil {
		return agent.Config{}, fmt.Errorf("agent channel: parse dsn: %w", err)
	}
	jdbcURL := strings.TrimSpace(cfg.DSN)
	if !prof.urlFromDSN {
		if f.Host == "" {
			return agent.Config{}, fmt.Errorf("agent channel: type %q requires a dsn with a host", cfg.Type)
		}
		jdbcURL, err = prof.buildURL(*f)
		if err != nil {
			return agent.Config{}, fmt.Errorf("agent channel: build jdbc url: %w", err)
		}
	}
	if jdbcURL == "" {
		return agent.Config{}, fmt.Errorf("agent channel: type %q requires a non-empty dsn", cfg.Type)
	}

	dirs := jarSearchDirs(cfg.Agent.JarsDir)
	driverJars, err := resolveDriverJars(dirs, prof.jarGlobs)
	if err != nil {
		return agent.Config{}, err
	}
	agentJar, err := resolveAgentJar(dirs, cfg.Agent.AgentJar)
	if err != nil {
		return agent.Config{}, err
	}

	// urlFromDSN profiles carry credentials inside the JDBC URL itself
	// (e.g. TimesTen uid/pwd); a decomposed userinfo would be meaningless.
	user, pass := f.Username, f.Password
	if prof.urlFromDSN {
		user, pass = "", ""
	}

	return agent.Config{
		DriverClass: prof.driverClass,
		URL:         jdbcURL,
		User:        user,
		Password:    pass,
		Family:      prof.family,
		Classpath:   driverJars,
		JavaHome:    cfg.Agent.JavaHome,
		AgentJar:    agentJar,
	}, nil
}

func jarSearchDirs(configured string) []string {
	dirs := []string{}
	if configured != "" {
		dirs = append(dirs, configured)
	}
	dirs = append(dirs, ".")
	return dirs
}

// resolveDriverJars locates the profile's driver jar: the globs are
// alternatives (vendor jar naming varies across releases), first match wins.
func resolveDriverJars(dirs []string, globs []string) ([]string, error) {
	for _, pattern := range globs {
		found, err := findFile(dirs, pattern)
		if err == nil {
			return []string{found}, nil
		}
	}
	return nil, fmt.Errorf("agent channel: no jar matching %s in %v; download the driver jar or set agent.jars_dir / agent_jar", strings.Join(globs, " | "), dirs)
}

func resolveAgentJar(dirs []string, configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("agent channel: agent jar %s: %w", configured, err)
		}
		return configured, nil
	}
	return findFile(dirs, "owl-agent*.jar")
}

func findFile(dirs []string, pattern string) (string, error) {
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("agent channel: no jar matching %s in %v; download the driver jar or set agent.jars_dir / agent_jar", pattern, dirs)
}

// openAgentChannel opens a *sql.DB through the owljdbc driver. The sidecar
// JVM is spawned lazily on first connection and shared per classpath profile.
func openAgentChannel(cfg config.DBConfig) (*sql.DB, error) {
	ac, err := buildAgentConfig(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("owljdbc", agent.EncodeDSN(ac))
	if err != nil {
		return nil, err
	}
	ConfigurePool(db, cfg)
	return db, nil
}
