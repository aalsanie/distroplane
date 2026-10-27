package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
)

func TestDoctorRemoteProbeValidatesNestedHosts(t *testing.T) {
	loaded := config.Loaded{Config: config.Config{
		Targets: []config.Target{
			{Provider: config.Provider{Name: "homebrew"}, Configuration: json.RawMessage(`{"manifest":{"url":"https://example.net/release.zip"},"repository":"https://github.com/example/homebrew-tap.git"}`)},
			{Provider: config.Provider{Name: "winget"}, Configuration: json.RawMessage(`{"installer":{"url":"https://example.net/release.zip"},"repository":"https://github.com/example/winget.git"}`)},
		},
	}}
	urls := doctorRemoteURLs(loaded)
	if len(urls) != 3 || urls[0] != "https://example.net/release.zip" {
		t.Fatalf("nested installer host missing or duplicated: %+v", urls)
	}
}

func TestDoctorRemoteWithoutHostIsSkipped(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	calls := 0
	d := inspectDoctor(context.Background(), path, doctorProbe{
		remote: true, timeout: time.Second,
		dial: func(context.Context, string, string) (net.Conn, error) {
			calls++
			return nil, errors.New("must not dial")
		},
	})
	if d.exit() != exitOK || calls != 0 || !hasDoctorCode(d.output, "REMOTE_SKIPPED", doctorSkip) {
		t.Fatalf("unnecessary remote probe: %+v, calls=%d", d, calls)
	}
}

func TestDoctorInvalidRemoteURLDoesNotMakeNetworkRequest(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published","registry":"https://example.net:badport/release"}`)
	calls := 0
	d := inspectDoctor(context.Background(), path, doctorProbe{
		remote: true, timeout: time.Second,
		dial: func(context.Context, string, string) (net.Conn, error) {
			calls++
			return nil, errors.New("unexpected dial")
		},
	})
	if d.exit() != exitOperational || calls != 0 || !hasDoctorCode(d.output, "REMOTE_INVALID", doctorFail) {
		t.Fatalf("invalid endpoint probe: %+v calls=%d", d, calls)
	}
}

func TestDoctorHonorsCancellationBetweenRemoteProbes(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published","registry":"https://example.net/","api":"https://example.org/"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	d := inspectDoctor(ctx, path, doctorProbe{
		remote: true, timeout: time.Second,
		dial: func(context.Context, string, string) (net.Conn, error) {
			calls++
			cancel()
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	})
	if calls != 1 || !hasDoctorCode(d.output, "CANCELLED", doctorFail) || d.exit() != exitInvalid {
		t.Fatalf("cancelled probe continued after termination: %+v calls=%d", d, calls)
	}
}

func TestDoctorNeverEchoesMalformedConfigurationContents(t *testing.T) {
	dir := t.TempDir()
	const token = "DOCTOR_UNSAFE_INLINE_TOKEN_DO_NOT_ECHO"
	file := filepath.Join(dir, "config.json")
	if err := os.WriteFile(file, []byte(`{"schemaVersion":"2","password":"`+token+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", file, "--json"}, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("invalid inline credential accepted: %d", code)
	}
	if strings.Contains(stdout.String()+stderr.String(), token) || !strings.Contains(stdout.String(), "INVALID_CONFIG") {
		t.Fatalf("private config value in diagnostics: %s %s", stdout.String(), stderr.String())
	}
}

func TestDoctorUnknownBinaryArchitectureRemainsUnverified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script launch is not portable to Windows")
	}
	dir, file := publishFixture(t, `{"mode":"published"}`)
	loaded, err := config.Load(file)
	if err != nil { t.Fatal(err) }
	provider := loaded.Config.Targets[0].Provider.Executable
	script := filepath.Join(dir, "distroplane-provider-fake")
	if err := os.Remove(provider); err != nil { t.Fatal(err) }
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil { t.Fatal(err) }
	report := doctorRun(t, exitInvalid, "--config", file, "--json")
	if !hasDoctorCode(report, "ARCH_UNKNOWN", doctorWarn) || !hasDoctorCode(report, "VERSION_MISMATCH", doctorFail) {
		t.Fatalf("foreign executable incorrectly treated as compatible: %+v", report)
	}
}
