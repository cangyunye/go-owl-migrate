package cmd

import (
	"testing"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{"empty returns fallback", "", 30 * time.Second, 30 * time.Second, false},
		{"valid seconds", "10s", 0, 10 * time.Second, false},
		{"valid minutes", "5m", 0, 5 * time.Minute, false},
		{"valid hours", "1h", 0, time.Hour, false},
		{"valid compound", "1h30m", 0, 90 * time.Minute, false},
		{"invalid returns error", "abc", 0, 0, true},
		{"negative valid", "-5s", 0, -5 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input, tt.fallback)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestConnectTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DBConfig
		want time.Duration
	}{
		{"default 30s", config.DBConfig{}, 30 * time.Second},
		{"configured", config.DBConfig{ConnectTimeout: "10s"}, 10 * time.Second},
		{"invalid falls back", config.DBConfig{ConnectTimeout: "bad"}, 30 * time.Second},
		{"zero falls back", config.DBConfig{ConnectTimeout: "0s"}, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectTimeout(tt.cfg); got != tt.want {
				t.Errorf("connectTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DBConfig
		want time.Duration
	}{
		{"default no timeout", config.DBConfig{}, 0},
		{"configured", config.DBConfig{QueryTimeout: "1h"}, time.Hour},
		{"invalid falls back to 0", config.DBConfig{QueryTimeout: "bad"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := queryTimeout(tt.cfg); got != tt.want {
				t.Errorf("queryTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigurePool_Defaults(t *testing.T) {
	db, err := openDB(config.DBConfig{Type: "postgres", DSN: "host=127.0.0.1 dbname=dummy"})
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 10 {
		t.Errorf("default MaxOpenConns = %d, want 10", stats.MaxOpenConnections)
	}
}

func TestConfigurePool_Custom(t *testing.T) {
	db, err := openDB(config.DBConfig{
		Type: "postgres",
		DSN:  "host=127.0.0.1 dbname=dummy",
		Pool: config.PoolConfig{
			MaxOpenConns:    25,
			MaxIdleConns:    12,
			ConnMaxLifetime: "1h",
			ConnMaxIdleTime: "15m",
		},
	})
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 25 {
		t.Errorf("MaxOpenConns = %d, want 25", stats.MaxOpenConnections)
	}
}

func TestConfigurePool_IdleClampedToOpen(t *testing.T) {
	db, err := openDB(config.DBConfig{
		Type: "postgres",
		DSN:  "host=127.0.0.1 dbname=dummy",
		Pool: config.PoolConfig{
			MaxOpenConns: 3,
			MaxIdleConns: 10,
		},
	})
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 3 {
		t.Errorf("MaxOpenConns = %d, want 3", stats.MaxOpenConnections)
	}
}

func TestOpenDB_UnsupportedType(t *testing.T) {
	_, err := openDB(config.DBConfig{Type: "mongodb", DSN: "localhost"})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}
