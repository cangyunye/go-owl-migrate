package config

import "testing"

func TestMaskDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"postgres url", "postgres://scott:tiger@db.example:5432/app", "postgres://scott:******@db.example:5432/app"},
		{"oracle url", "oracle://scott:tiger@10.0.0.1:1521/ORCL", "oracle://scott:******@10.0.0.1:1521/ORCL"},
		{"mysql native", "scott:tiger@tcp(127.0.0.1:3306)/app", "scott:******@tcp(127.0.0.1:3306)/app"},
		{"mysql native, password with slash", "scott:my/pass@tcp(127.0.0.1:3306)/app", "scott:******@tcp(127.0.0.1:3306)/app"},
		{"url without password", "postgres://scott@db.example:5432/app", "postgres://scott@db.example:5432/app"},
		{"empty", "", ""},
		{"unrecognized", "some-host:1521", "some-host:1521"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskDSN(tc.in); got != tc.want {
				t.Errorf("MaskDSN(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReplaceDSNPassword: the plan protocol swaps a profile DSN's password
// for a sentinel — the swap must hit both recognized DSN shapes and never
// corrupt unrecognized ones.
func TestReplaceDSNPassword(t *testing.T) {
	cases := []struct {
		name, dsn, repl, want string
	}{
		{"url-form", "oracle://scott:tiger@127.0.0.1:1521/XEPDB1", "__PWD_oracle__", "oracle://scott:__PWD_oracle__@127.0.0.1:1521/XEPDB1"},
		{"mysql-native", "root:root123456@tcp(127.0.0.1:3306)/shop", "__PWD_mysql__", "root:__PWD_mysql__@tcp(127.0.0.1:3306)/shop"},
	}
	for _, c := range cases {
		if got := ReplaceDSNPassword(c.dsn, c.repl); got != c.want {
			t.Errorf("%s: ReplaceDSNPassword(%q) = %q, want %q", c.name, c.dsn, got, c.want)
		}
	}
	// Unrecognized shape must be returned unchanged (fail-closed handled by callers).
	opaque := "some opaque dsn string"
	if got := ReplaceDSNPassword(opaque, "__PWD_x__"); got != opaque {
		t.Errorf("opaque DSN must be unchanged, got %q", got)
	}
	if got := ReplaceDSNPassword("", "x"); got != "" {
		t.Errorf("empty DSN = %q", got)
	}
}
