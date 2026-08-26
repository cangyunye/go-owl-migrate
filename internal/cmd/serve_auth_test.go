package cmd

import "testing"

func TestRequireBindHost_Policy(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		token   string
		wantErr bool
	}{
		{"loopback no token ok", "127.0.0.1", "", false},
		{"loopback with token ok", "127.0.0.1", "s3cret", false},
		{"localhost no token ok", "localhost", "", false},
		{"non-loopback no token refused", "0.0.0.0", "", true},
		{"non-loopback with token ok", "0.0.0.0", "s3cret", false},
		{"private net no token refused", "192.168.1.10", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireBindHost(tc.host, tc.token)
			if tc.wantErr && err == nil {
				t.Errorf("requireBindHost(%q,%q) error = nil, want error", tc.host, tc.token)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("requireBindHost(%q,%q) error = %v, want nil", tc.host, tc.token, err)
			}
		})
	}
}
