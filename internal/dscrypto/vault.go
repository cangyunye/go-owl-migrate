// Package dscrypto encrypts data-source DSNs at rest using AES-GCM.
//
// The key is a 32-byte secret loaded in this priority order:
//
//  1. The OWL_MIGRATE_DS_KEY environment variable (hex or base64, 32 bytes).
//  2. A key file `<keysDir>/.ds_key` (32 raw bytes, chmod 0600). If the file
//     does not exist it is generated with a cryptographically-random key.
//
// The key is held only in server memory; it is never returned by any API and
// never leaves the process. Decryption therefore happens only on the server,
// so a DSN password never round-trips through the browser.
package dscrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvKey overrides the key file location entirely (hosted/container use).
	EnvKey = "OWL_MIGRATE_DS_KEY"
	// KeyFileName is the key file placed under keysDir.
	KeyFileName = ".ds_key"
)

// preset is the magic prefix stamped on every encrypted value so the store can
// recognise and version ciphertext values.
const preset = "enc:v1:"

// IsEncrypted reports whether a value was produced by Encrypt.
func IsEncrypted(v string) bool { return strings.HasPrefix(v, preset) }

// Vault encrypts and decrypts values with a single AES-256 key.
type Vault struct {
	aead cipher.AEAD
}

// New loads the key from the environment or the key file (generating the file
// on first run) and returns a ready-to-use Vault.
func New(keysDir string) (*Vault, error) {
	key, err := loadKey(keysDir)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("dscrypto: invalid key length: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// Encrypt seals plaintext, returning "enc:v1:" + base64(nonce||ciphertext).
func (v *Vault) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := v.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return preset + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt. Values without the preset prefix
// are treated as already-plaintext (e.g. an older record) and returned as-is.
func (v *Vault) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" || ciphertext == "*" {
		return ciphertext, nil
	}
	if !strings.HasPrefix(ciphertext, preset) {
		return ciphertext, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, preset))
	if err != nil {
		return "", fmt.Errorf("dscrypto: bad base64: %w", err)
	}
	if len(raw) < v.aead.NonceSize() {
		return "", errors.New("dscrypto: ciphertext too short")
	}
	nonce, body := raw[:v.aead.NonceSize()], raw[v.aead.NonceSize():]
	open, err := v.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("dscrypto: decrypt: %w", err)
	}
	return string(open), nil
}

// loadKey resolves the 32-byte key: env first, then the on-disk key file.
func loadKey(keysDir string) ([]byte, error) {
	if env := os.Getenv(EnvKey); env != "" {
		return decodeKey(env)
	}
	if keysDir == "" {
		return nil, errors.New("dscrypto: empty keysDir and no " + EnvKey)
	}
	path := filepath.Join(keysDir, KeyFileName)
	key, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		key, err = generateKeyFile(path)
		if err != nil {
			return nil, err
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("dscrypto: key file %q must be 32 bytes, got %d", path, len(key))
	}
	return key, nil
}

func generateKeyFile(path string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

// decodeKey accepts a hex (64 chars) or base64 (44 chars) 32-byte key. A raw
// 32-byte string is accepted too for convenience.
func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("dscrypto: OWL_MIGRATE_DS_KEY must be 32 bytes (hex/base64)")
}
