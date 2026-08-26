package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestServeLock_ReleasePreservesForeignLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")
	foreign := strconv.Itoa(os.Getppid())
	if err := os.WriteFile(lock, []byte(foreign), 0644); err != nil {
		t.Fatal(err)
	}
	releaseServeLock(lock)
	data, err := os.ReadFile(lock)
	if err != nil {
		t.Fatalf("release deleted a foreign-owned lock: %v", err)
	}
	if strings.TrimSpace(string(data)) != foreign {
		t.Fatalf("lock content = %q, want foreign pid %s", data, foreign)
	}
}

func TestServeLock_AcquireExistingForeignRefused(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getppid())), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)
	if err := acquireServeLock(lock); err == nil {
		t.Fatal("acquire over a live foreign owner must fail")
	}
}
