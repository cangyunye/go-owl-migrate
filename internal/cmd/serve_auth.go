package cmd

import (
	"fmt"
	"net"
	"strings"
)

// requireBindHost refuses to bind a non-loopback address when no token is set,
// so an unauthenticated server can never be exposed on a shared network.
func requireBindHost(host, token string) error {
	if token != "" {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		// "localhost" and DNS names are treated as loopback-safe here; only
		// explicitly non-loopback IPs are refused.
		if host == "localhost" || host == "" {
			return nil
		}
		// Resolve; if it resolves to a loopback IP, allow.
		if addrs, err := net.LookupIP(host); err == nil {
			for _, a := range addrs {
				if a.IsLoopback() {
					return nil
				}
			}
		}
		return fmt.Errorf("refusing to bind %s without an auth token (use --token or OWL_MIGRATE_TOKEN); refusing to expose an unauthenticated server on a non-loopback address", host)
	}
	if ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to bind %s without an auth token (use --token or OWL_MIGRATE_TOKEN); refusing to expose an unauthenticated server on a non-loopback address", host)
}
