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
