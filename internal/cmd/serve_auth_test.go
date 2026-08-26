package cmd

import (
	"net"
	"testing"
)

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
		{"empty host no token refused", "", "", true},
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

func TestRequireBindHost_Resolved(t *testing.T) {
	cases := []struct {
		name    string
		addrs   []net.IP
		wantErr bool
	}{
		{"all loopback allowed", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, false},
		{"mixed loopback and public refused", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("10.0.0.5")}, true},
		{"only public refused", []net.IP{net.ParseIP("10.0.0.5")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := lookupIP
			lookupIP = func(host string) ([]net.IP, error) { return tc.addrs, nil }
			defer func() { lookupIP = orig }()
			err := requireBindHost("some.host.name", "")
			if tc.wantErr && err == nil {
				t.Errorf("requireBindHost(%q) error = nil, want error", "some.host.name")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("requireBindHost(%q) error = %v, want nil", "some.host.name", err)
			}
		})
	}
}
