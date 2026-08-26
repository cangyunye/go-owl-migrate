package dscrypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	v, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := "user:secret@tcp(host:3306)/db?charset=utf8mb4"
	ct, err := v.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct == plain {
		t.Fatalf("ciphertext equals plaintext")
	}
	if !strings.HasPrefix(ct, "enc:v1:") {
		t.Fatalf("ciphertext missing preset: %q", ct)
	}
	got, err := v.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round-trip = %q, want %q", got, plain)
	}
}

func TestEncryptProducesFreshNonce(t *testing.T) {
	dir := t.TempDir()
	v, _ := New(dir)
	a, _ := v.Encrypt("same-secret")
	b, _ := v.Encrypt("same-secret")
	if a == b {
		t.Fatalf("two encryptions of same plaintext produced identical ciphertext")
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	dir := t.TempDir()
	v, _ := New(dir)
	for _, in := range []string{"", "*", "user:pass@tcp(h:1)/d"} {
		got, err := v.Decrypt(in)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", in, err)
		}
		if got != in {
			t.Fatalf("Decrypt(%q) = %q, want passthrough", in, got)
		}
	}
	// A malformed encrypted value must error rather than echo back.
	if _, err := v.Decrypt("enc:v1:"); err == nil {
		t.Fatalf("expected error for empty encrypted value")
	}
}

func TestKeyFileGeneratedAndReused(t *testing.T) {
	dir := t.TempDir()
	v1, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	keyPath := filepath.Join(dir, KeyFileName)
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not created: %v", err)
	}
	info, _ := os.Stat(keyPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("key file mode = %o, want 0600", info.Mode().Perm())
	}

	// A fresh vault reading the same dir must decrypt old ciphertext.
	v2, err := New(dir)
	if err != nil {
		t.Fatalf("New#2: %v", err)
	}
	ct, _ := v1.Encrypt("secret")
	got, err := v2.Decrypt(ct)
	if err != nil || got != "secret" {
		t.Fatalf("cross-vault decrypt = %q, %v", got, err)
	}
}

func TestEnvKeyOverride(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	t.Setenv(EnvKey, key)
	v, err := New("") // no key dir needed when env is set
	if err != nil {
		t.Fatalf("New with env: %v", err)
	}
	ct, _ := v.Encrypt("x")
	got, _ := v.Decrypt(ct)
	if got != "x" {
		t.Fatalf("decrypt = %q, want x", got)
	}
}

func TestBadEnvKey(t *testing.T) {
	t.Setenv(EnvKey, "too-short")
	if _, err := New(""); err == nil {
		t.Fatalf("expected error for bad env key")
	}
}
