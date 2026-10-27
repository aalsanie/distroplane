//go:build !windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminationSignalCancelsCLIRuntime(t *testing.T) {
	const marker = "DISTROPLANE_SIGNAL_TEST_CHILD"
	if os.Getenv(marker) == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
		defer stop()
		fmt.Fprintln(os.Stdout, "READY")
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stdout, "CANCELLED")
		case <-time.After(5 * time.Second):
			t.Fatal("process ignored signal")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminationSignalCancelsCLIRuntime$")
	child.Env = append(os.Environ(), marker+"=1")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() || strings.TrimSpace(lines.Text()) != "READY" {
		_ = child.Process.Kill()
		t.Fatal("signal fixture did not become ready")
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		_ = child.Process.Kill()
		t.Fatal(err)
	}
	if !lines.Scan() || lines.Text() != "CANCELLED" {
		_ = child.Process.Kill()
		t.Fatal("SIGTERM did not cancel the process context")
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("signal handler exited unexpectedly: %v", err)
	}
}
