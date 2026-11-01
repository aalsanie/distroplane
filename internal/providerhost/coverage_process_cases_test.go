package providerhost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestProviderProcessDiagnosticAndEnvironmentBoundaries(t *testing.T) {
	var absent *ProcessError
	if absent.Error() != "" || absent.Unwrap() != nil {
		t.Fatal("nil process error should be harmless")
	}
	underlying := errors.New("provider exited")
	for _, tc := range []struct{ diagnostics string; truncated bool }{
		{"", false}, {"diagnostic", false}, {"", true}, {"diagnostic", true},
	} {
		err := &ProcessError{Executable: "/usr/bin/provider", Err: underlying, Diagnostics: tc.diagnostics, Truncated: tc.truncated}
		message := err.Error()
		if !errors.Is(err, underlying) || strings.Contains(message, "stderr truncated") != tc.truncated {
			t.Fatalf("unsafe process diagnostic: %q", message)
		}
		if tc.diagnostics != "" && !strings.Contains(message, tc.diagnostics) {
			t.Fatalf("diagnostic was omitted: %q", message)
		}
	}
	var providerErr *ProviderError
	if providerErr.Error() != "" {
		t.Fatal("nil structured provider error was formatted")
	}
	if _, err := decodeResponsePayload([]byte("{}"), &struct{}{}); err == nil {
		t.Fatal("unsupported protocol response type accepted")
	}
	for _, destination := range []any{
		&protocol.DescribeResponse{}, &protocol.PlanResponse{},
		&protocol.ApplyResponse{}, &protocol.ReconcileResponse{},
	} {
		if err := decodeResponsePayload([]byte("{"), destination); err == nil {
			t.Fatalf("malformed response accepted for %T", destination)
		}
	}
	if _, err := New(Options{MaxMessageBytes: 2048, MaxStderrBytes: 4096}); err != nil {
		t.Fatalf("nondefault framing limits rejected: %v", err)
	}
	var client *Client
	if _, err := client.processEnvironment(nil); err == nil {
		t.Fatal("nil client accepted environment")
	}
}

func TestProviderExecutablePathValidationRejectsUnsafeFiles(t *testing.T) {
	for _, value := range []string{"", " ", "relative", string([]byte{0xff}), filepath.Join(t.TempDir(), "missing")} {
		if _, err := validateExecutable(value); err == nil {
			t.Errorf("unsafe executable accepted: %q", value)
		}
	}
	if _, err := validateExecutable(t.TempDir()); err == nil {
		t.Fatal("provider executable directory accepted")
	}
	path := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(path, []byte("executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if absolute, err := validateExecutable(path); err != nil || absolute != path {
		t.Fatalf("valid executable rejected: %q, %v", absolute, err)
	}
	for _, value := range []string{"EMPTY", "PATH=override", "INVALID\x00NAME=x", "=empty"} {
		if _, _, err := parseEnvironmentEntry(value); err == nil {
			t.Errorf("unsafe environment entry accepted: %q", value)
		}
	}
	if entry, key, err := parseEnvironmentEntry("TOKEN=visible"); err != nil || entry.value != "visible" || key == "" {
		t.Fatalf("valid environment entry rejected: %+v %q %v", entry, key, err)
	}
}
