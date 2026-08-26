package cmd

import (
	"fmt"
	"net"
	"strings"
)

var lookupIP = net.LookupIP

// requireBindHost refuses to bind a non-loopback address when no token is set,
// so an unauthenticated server can never be exposed on a shared network.
func requireBindHost(host, token string) error {
	if token != "" {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		return refuseBind(host)
	}
	// Not a literal IP: "localhost" is loopback; other names must resolve to
	// addresses that are ALL loopback, otherwise refusing avoids exposing an
	// unauthenticated server on a non-loopback interface.
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if host == "" {
		return refuseBind(host)
	}
	addrs, err := lookupIP(host)
	if err == nil && len(addrs) > 0 {
		allLoopback := true
		for _, a := range addrs {
			if !a.IsLoopback() {
				allLoopback = false
				break
			}
		}
		if allLoopback {
			return nil
		}
	}
	return refuseBind(host)
}

func refuseBind(host string) error {
	return fmt.Errorf("refusing to bind %s without an auth token (use --token or OWL_MIGRATE_TOKEN)", host)
}
