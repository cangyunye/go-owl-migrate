package dbconn

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/cangyunye/owljdbc"

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
	if owljdbc.HasProfile(t) {
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

// buildAgentConfig translates a DBConfig into an owljdbc connection config:
// the native DSN is decomposed by dsnfields into an Endpoint, then the
// module-side catalog assembles the JDBC URL and resolves the sidecar and
// driver jars.
func buildAgentConfig(cfg config.DBConfig) (owljdbc.Config, error) {
	t := registry.Normalize(strings.ToLower(strings.TrimSpace(cfg.Type)))
	f, err := dsnfields.Decompose(t, cfg.DSN)
	if err != nil {
		return owljdbc.Config{}, fmt.Errorf("agent channel: parse dsn: %w", err)
	}
	endpoint := owljdbc.Endpoint{
		Host:     f.Host,
		Port:     f.Port,
		User:     f.Username,
		Password: f.Password,
		Database: f.Database,
	}
	return owljdbc.BuildConfig(t, endpoint, cfg.DSN, cfg.Agent.JarsDir, cfg.Agent.AgentJar, cfg.Agent.JavaHome)
}

// openAgentChannel opens a *sql.DB through the owljdbc driver. The sidecar
// JVM is spawned lazily on first connection and shared per classpath profile.
func openAgentChannel(cfg config.DBConfig) (*sql.DB, error) {
	ac, err := buildAgentConfig(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(ac))
	if err != nil {
		return nil, err
	}
	ConfigurePool(db, cfg)
	return db, nil
}
