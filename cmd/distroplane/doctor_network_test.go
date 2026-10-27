package main

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestDoctorSafeDialRejectsPrivateDNSResolution(t *testing.T) {
	resolved := false
	dialed := false
	resolve := func(_ context.Context, host string) ([]net.IPAddr, error) {
		resolved = true
		if host != "registry.example.net" {
			t.Fatalf("wrong DNS host: %s", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("10.4.3.2")}}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("should never dial private DNS")
	}
	conn, err := doctorSafeDialWith(context.Background(), "tcp", "registry.example.net:443", resolve, dial)
	if conn != nil {
		conn.Close()
	}
	if err == nil || !resolved || dialed || !strings.Contains(err.Error(), "no permitted public") {
		t.Fatalf("private DNS response was not blocked: resolved=%t dialed=%t error=%v", resolved, dialed, err)
	}
}

func TestDoctorSafeDialSelectsOnlyValidatedPublicAddress(t *testing.T) {
	visited := []string{}
	resolve := func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("169.254.169.254")},
			{IP: net.ParseIP("8.8.8.8")},
		}, nil
	}
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("invalid network: %q", network)
		}
		visited = append(visited, address)
		client, server := net.Pipe()
		server.Close()
		return client, nil
	}
	conn, err := doctorSafeDialWith(context.Background(), "tcp", "registry.example.net:443", resolve, dial)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if !reflect.DeepEqual(visited, []string{"8.8.8.8:443"}) {
		t.Fatalf("unexpected targets: %v", visited)
	}
}

func TestDoctorSafeDialPublicIPSkipsDNSAndSurfacesDialFailure(t *testing.T) {
	resolve := func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("literal IP requested DNS resolution")
		return nil, nil
	}
	calls := 0
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		calls++
		if network != "tcp" || address != "8.8.8.8:443" {
			t.Fatalf("wrong endpoint: %s %s", network, address)
		}
		return nil, errors.New("simulated external refusal")
	}
	conn, err := doctorSafeDialWith(context.Background(), "tcp", "8.8.8.8:443", resolve, dial)
	if conn != nil {
		conn.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "simulated external refusal") || calls != 1 {
		t.Fatalf("dial failure was swallowed: %v calls=%d", err, calls)
	}
}

func TestDoctorSafeDialReportsDNSFailuresAndEmptyAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(context.Context, string) ([]net.IPAddr, error)
		want    string
	}{
		{"DNS error", func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("resolver unavailable")
		}, "resolve diagnostic endpoint"},
		{"no IP answers", func(context.Context, string) ([]net.IPAddr, error) {
			return nil, nil
		}, "no permitted public address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := doctorSafeDialWith(context.Background(), "tcp", "registry.example.net:443", tc.resolve, func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("unexpected dial after DNS failure")
				return nil, nil
			})
			if conn != nil {
				conn.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestDoctorSafeDialHonorsCancellationAfterFailedDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolve := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	conn, err := doctorSafeDialWith(ctx, "tcp", "registry.example.net:443", resolve, func(context.Context, string, string) (net.Conn, error) {
		cancel()
		return nil, errors.New("connection interrupted")
	})
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost after failed dial: %v", err)
	}
}
