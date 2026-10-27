package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Local release diagnostics may probe remote network access only when
// explicitly requested. Never dial private endpoints, even when a configured
// public hostname resolves to a private address.
func doctorPublicHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.ContainsAny(host, " /\\%@") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return doctorPublicIP(ip)
	}
	if !strings.Contains(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".test", ".invalid", ".example", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func doctorPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func doctorSafeDial(ctx context.Context, network, address string) (net.Conn, error) {
	return doctorSafeDialWith(ctx, network, address, net.DefaultResolver.LookupIPAddr, (&net.Dialer{}).DialContext)
}

func doctorSafeDialWith(ctx context.Context, network, address string, resolve func(context.Context, string) ([]net.IPAddr, error), dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if network != "tcp" {
		return nil, fmt.Errorf("diagnostic network must be TCP")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid remote diagnostic address: %w", err)
	}
	if !doctorPublicHost(host) {
		return nil, fmt.Errorf("private or unsupported diagnostic endpoint")
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		addresses, err = resolve(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve diagnostic endpoint: %w", err)
		}
	}
	var lastErr error
	for _, addr := range addresses {
		if !doctorPublicIP(addr.IP) {
			continue
		}
		conn, dialErr := dial(ctx, "tcp", net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("diagnostic endpoint has no permitted public address")
}
