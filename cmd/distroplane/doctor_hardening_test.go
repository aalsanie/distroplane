package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func TestDoctorExplicitProtocolVersionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		versions   []string
		compatible bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"0.0.1"}, false},
		{[]string{"0.0.1", protocol.Version}, true},
		{[]string{protocol.Version}, true},
	} {
		if got := doctorSupportsProtocol(tc.versions); got != tc.compatible {
			t.Fatalf("protocol versions %v accepted=%t, want %t", tc.versions, got, tc.compatible)
		}
	}
}

func TestDoctorNetworkRejectsLocalAndSpecialAddresses(t *testing.T) {
	for _, host := range []string{
		"localhost", "localhost.", "service.local", "metadata.google.internal",
		"registry.lan", "service.home.arpa", "example.invalid", "example.test",
		"127.0.0.1", "127.1.2.3", "169.254.169.254", "10.5.3.1",
		"172.16.0.1", "192.168.1.1", "100.64.2.3", "192.0.2.1",
		"198.18.0.1", "198.51.100.9", "203.0.113.7", "::1",
		"fe80::1", "fc00::1", "2001:db8::1", "service",
	} {
		if doctorPublicHost(host) {
			t.Fatalf("unsafe diagnostic hostname accepted: %q", host)
		}
	}
	for _, host := range []string{
		"registry.npmjs.org", "api.github.com", "example.net", "8.8.8.8",
		"2001:4860:4860::8888",
	} {
		if !doctorPublicHost(host) {
			t.Fatalf("valid public endpoint rejected: %q", host)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "100.64.0.1", "::1", "fc00::1"} {
		if doctorPublicIP(net.ParseIP(ip)) {
			t.Fatalf("private IP accepted: %s", ip)
		}
	}
	if !doctorPublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP was rejected")
	}
	if doctorPublicIP(nil) {
		t.Fatal("invalid IP accepted")
	}
}

func TestDoctorNetworkDoesNotDialPrivateEndpoints(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		network string
		address string
	}{
		{"tcp", "127.0.0.1:80"},
		{"tcp", "169.254.169.254:80"},
		{"tcp", "10.2.3.4:443"},
		{"tcp", "[::1]:443"},
		{"tcp", "localhost:443"},
		{"tcp", "api.github.com"},
		{"udp", "8.8.8.8:443"},
	} {
		conn, err := doctorSafeDial(ctx, tc.network, tc.address)
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatalf("unexpected network connection to %s://%s", tc.network, tc.address)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	conn, err := doctorSafeDial(cancelled, "tcp", "8.8.8.8:443")
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check accessed network: %v", err)
	}
}

func TestDoctorReportsReservedRunWithoutCreatingJournal(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &diagnostic); code != exitOK {
		t.Fatalf("fixture plan failed: code=%d err=%s", code, diagnostic.String())
	}
	var planned planOutput
	if err := json.Unmarshal(out.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	missingJournal := filepath.Join(dir, "claimed-but-unwritten.journal")
	store := runstate.Store{Root: filepath.Join(dir, ".distroplane")}
	if err := store.ClaimRun(planned.Path, missingJournal, domain.RunID("run-doctor-reserved")); err != nil {
		t.Fatal(err)
	}
	report := inspectDoctor(context.Background(), cfg, doctorProbe{timeout: 3 * time.Second})
	if !hasDoctorCode(report.output, "RUN_RESERVED", doctorWarn) {
		t.Fatalf("reserved run missing diagnostic: %+v", report.output)
	}
	if _, err := os.Stat(missingJournal); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor recreated a missing journal: %v", err)
	}
}

func TestDoctorSignalsExternalReconciliationWithoutDispatch(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", cfg, "--yes", "--json"}, nil, &out, &diagnostic); code != exitPending {
		t.Fatalf("fixture pending publish failed: code=%d err=%s", code, diagnostic.String())
	}
	var published publishOutput
	if err := json.Unmarshal(out.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(published.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	report := inspectDoctor(context.Background(), cfg, doctorProbe{timeout: 3 * time.Second})
	if !hasDoctorCode(report.output, "RECONCILE_REQUIRED", doctorWarn) {
		t.Fatalf("pending run not flagged: %+v", report.output)
	}
	after, err := os.ReadFile(published.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("doctor mutated an unresolved run: %v", err)
	}
}
