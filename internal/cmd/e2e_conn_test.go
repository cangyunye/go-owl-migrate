//go:build e2e

package cmd

import (
	"context"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

const (
	pgDSN    = "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable"
	mysqlDSN = "root:root123456@tcp(127.0.0.1:3306)/default_db"
)

func TestOpenDB_Postgres_PoolApplied(t *testing.T) {
	db, err := openDB(config.DBConfig{
		Type: "postgres",
		DSN:  pgDSN,
		Pool: config.PoolConfig{
			MaxOpenConns:    8,
			MaxIdleConns:    4,
			ConnMaxLifetime: "10m",
			ConnMaxIdleTime: "2m",
		},
	})
	if err != nil {
		t.Fatalf("openDB postgres: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 8 {
		t.Errorf("MaxOpenConns = %d, want 8", stats.MaxOpenConnections)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("query postgres: %v", err)
	}
	if result != 1 {
		t.Errorf("SELECT 1 = %d, want 1", result)
	}
}

func TestOpenDB_MySQL_PoolApplied(t *testing.T) {
	db, err := openDB(config.DBConfig{
		Type: "mysql",
		DSN:  mysqlDSN,
		Pool: config.PoolConfig{
			MaxOpenConns: 6,
			MaxIdleConns: 3,
		},
	})
	if err != nil {
		t.Fatalf("openDB mysql: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 6 {
		t.Errorf("MaxOpenConns = %d, want 6", stats.MaxOpenConnections)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping mysql: %v", err)
	}

	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("query mysql: %v", err)
	}
	if result != 1 {
		t.Errorf("SELECT 1 = %d, want 1", result)
	}
}

func TestOpenDB_Postgres_DefaultPool(t *testing.T) {
	db, err := openDB(config.DBConfig{Type: "postgres", DSN: pgDSN})
	if err != nil {
		t.Fatalf("openDB postgres: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 10 {
		t.Errorf("default MaxOpenConns = %d, want 10", stats.MaxOpenConnections)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestOpenDB_Postgres_ConnectTimeout(t *testing.T) {
	cfg := config.DBConfig{
		Type:           "postgres",
		DSN:            pgDSN,
		ConnectTimeout: "5s",
	}

	db, err := openDB(cfg)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	timeout := connectTimeout(cfg)
	if timeout != 5*time.Second {
		t.Errorf("connectTimeout = %v, want 5s", timeout)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping with timeout: %v", err)
	}
}

func TestOpenDB_Postgres_ConnectTimeout_Expired(t *testing.T) {
	db, err := openDB(config.DBConfig{Type: "postgres", DSN: pgDSN})
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	if err := db.PingContext(ctx); err == nil {
		t.Error("expected ping to fail with expired context")
	}
}

func TestOpenDB_Postgres_ConcurrentQueries(t *testing.T) {
	db, err := openDB(config.DBConfig{
		Type: "postgres",
		DSN:  pgDSN,
		Pool: config.PoolConfig{MaxOpenConns: 4, MaxIdleConns: 4},
	})
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(n int) {
			var result int
			errCh <- db.QueryRowContext(ctx, "SELECT $1", n).Scan(&result)
		}(i)
	}

	for i := 0; i < 8; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent query %d failed: %v", i, err)
		}
	}

	stats := db.Stats()
	if stats.OpenConnections > 4 {
		t.Errorf("OpenConnections = %d, should not exceed MaxOpenConns 4", stats.OpenConnections)
	}
}
