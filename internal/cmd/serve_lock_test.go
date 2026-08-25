package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestServeLock_AcquireReleaseCycle(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")

	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := acquireServeLock(lock); err == nil {
		t.Fatal("second acquire by live owner must fail")
	}
	releaseServeLock(lock)
	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	releaseServeLock(lock)
}

func TestServeLock_TakesOverStaleLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")
	// A PID that cannot exist on any sane system.
	if err := os.WriteFile(lock, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("stale lock takeover failed: %v", err)
	}
	data, _ := os.ReadFile(lock)
	if string(data) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock content = %q, want current pid", data)
	}
}
