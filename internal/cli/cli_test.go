package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/buildinfo"
)

func TestRun(t *testing.T) {
	info := buildinfo.Info{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-09-17T00:00:00Z"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no arguments shows help", wantCode: ExitOK, wantStdout: "Usage:"},
		{name: "help", args: []string{"help"}, wantCode: ExitOK, wantStdout: "Usage:"},
		{name: "short help", args: []string{"-h"}, wantCode: ExitOK, wantStdout: "Usage:"},
		{name: "long help", args: []string{"--help"}, wantCode: ExitOK, wantStdout: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: ExitOK, wantStdout: "distroplane 1.2.3\ncommit: abc123\nbuilt: 2026-09-17T00:00:00Z"},
		{name: "long version", args: []string{"--version"}, wantCode: ExitOK, wantStdout: "distroplane 1.2.3"},
		{name: "help rejects arguments", args: []string{"help", "extra"}, wantCode: ExitUsage, wantStderr: "help does not accept arguments"},
		{name: "version rejects arguments", args: []string{"version", "extra"}, wantCode: ExitUsage, wantStderr: "version does not accept arguments"},
		{name: "unknown command", args: []string{"publish"}, wantCode: ExitUsage, wantStderr: `unknown command "publish"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			gotCode := Run(tt.args, &stdout, &stderr, info)
			if gotCode != tt.wantCode {
				t.Fatalf("Run() code = %d, want %d", gotCode, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
