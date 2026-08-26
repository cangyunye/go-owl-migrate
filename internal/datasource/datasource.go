// Package datasource stores reusable database connection profiles for the web
// UI. Each profile records a database type, schema, and a DSN whose password is
// encrypted at rest with an AES-256 vault (internal/dscrypto).
//
// A data source is web-only: it exists to let an operator pick a known
// connection from the config form instead of retyping a DSN. The stored DSN is
// never returned by read endpoints and only decrypted server-side.
package datasource

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/dscrypto"
)

// ext is the on-disk file extension for a data-source profile.
const ext = ".yaml"

// refPrefix is the marker a config form stores in a DSN field to reference a
// stored data source by name. It is resolved server-side before a config is
// built, so the stored password never round-trips through the browser.
const refPrefix = "datasource:"

// Ref returns the reference value to place in a DSN field for a named source.
func Ref(name string) string { return refPrefix + name }

// IsRef reports whether a form DSN value is a data-source reference.
func IsRef(v string) bool { return strings.HasPrefix(v, refPrefix) && len(v) > len(refPrefix) }

// RefName extracts the data-source name from a reference value.
func RefName(v string) string { return strings.TrimPrefix(v, refPrefix) }

// Record is one data-source profile. DSN holds the encrypted ciphertext; it is
// rendered as "-" or omitted by read endpoints and decrypted only by Resolve/Get.
type Record struct {
	Name    string `json:"name" yaml:"name"`
	Type    string `json:"type" yaml:"type"`
	Schema  string `json:"schema" yaml:"schema"`
	DSN     string `json:"dsn" yaml:"dsn"`
	Remark  string `json:"remark" yaml:"remark"`
	Created string `json:"created" yaml:"created"`
	Updated string `json:"updated" yaml:"updated"`
}

// Info is the safe, DSN-free projection returned by List.
type Info struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Schema  string `json:"schema"`
	Remark  string `json:"remark"`
	Updated string `json:"updated"`
}

// Store is a directory of encrypted data-source profiles.
type Store struct {
	dir   string
	vault *dscrypto.Vault
}

// Open prepares a Store rooted at dir, encrypting/decrypting with vault.
func Open(dir string, vault *dscrypto.Vault) *Store {
	return &Store{dir: dir, vault: vault}
}

// Dir returns the store root directory.
func (s *Store) Dir() string { return s.dir }

// sanitizeName derives a safe in-directory name from arbitrary input.
func sanitizeName(raw string) string {
	base := strings.TrimSpace(filepath.Base(raw))
	if base == "." || base == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r == '\x00' {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(b.String())
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// safePath resolves a name to a path guaranteed to live inside the store dir.
func (s *Store) safePath(name string) (string, error) {
	clean := sanitizeName(name)
	if clean == "" {
		return "", errors.New("invalid data source name")
	}
	dirAbs, _ := filepath.Abs(s.dir)
	p := filepath.Join(dirAbs, clean+ext)
	if !strings.HasPrefix(p, dirAbs+string(os.PathSeparator)) {
		return "", errors.New("invalid data source name")
	}
	return p, nil
}

// List returns all profiles, newest first, with no DSN material.
func (s *Store) List() ([]Info, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Info{}, nil
		}
		return nil, err
	}
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		rec, err := s.read(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		if rec == nil {
			continue
		}
		out = append(out, Info{
			Name:    rec.Name,
			Type:    rec.Type,
			Schema:  rec.Schema,
			Remark:  rec.Remark,
			Updated: rec.Updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

// Get loads and decrypts a single profile.
func (s *Store) Get(name string) (*Record, error) {
	p, err := s.safePath(name)
	if err != nil {
		return nil, err
	}
	return s.read(p)
}

// Resolve decrypts a profile's DSN and returns the connection fields so the
// config builder can substitute them server-side.
func (s *Store) Resolve(name string) (typ, schema, dsn string, err error) {
	rec, err := s.Get(name)
	if err != nil {
		return "", "", "", err
	}
	dsn = rec.DSN
	if v := s.vault; v != nil {
		dsn, err = v.Decrypt(rec.DSN)
		if err != nil {
			return "", "", "", err
		}
	}
	return rec.Type, rec.Schema, dsn, nil
}

// Put creates or updates a profile, encrypting the DSN at rest.
func (s *Store) Put(name, typ, schema, dsn, remark string) error {
	clean := sanitizeName(name)
	if clean == "" {
		return errors.New("invalid data source name")
	}
	if typ == "" {
		return errors.New("type is required")
	}
	p, err := s.safePath(clean)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	rec := &Record{Name: clean, Type: typ, Schema: schema, Remark: remark, Updated: now}
	if existing, err := s.read(p); err == nil && existing != nil {
		rec.Created = existing.Created
		if dsn == "" {
			rec.DSN = existing.DSN
			rec.Type = typ
		}
	}
	if rec.Created == "" {
		rec.Created = now
	}
	if dsn != "" {
		if s.vault != nil && !dscrypto.IsEncrypted(dsn) {
			enc, err := s.vault.Encrypt(dsn)
			if err != nil {
				return err
			}
			rec.DSN = enc
		} else {
			rec.DSN = dsn
		}
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// Delete removes a profile. It is a no-op (no error) when the profile is absent.
func (s *Store) Delete(name string) error {
	p, err := s.safePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// read loads and decrypts one profile file, returning nil on parse failures.
func (s *Store) read(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse data source %q: %w", path, err)
	}
	if rec.Name == "" {
		rec.Name = strings.TrimSuffix(filepath.Base(path), ext)
	}
	return &rec, nil
}
