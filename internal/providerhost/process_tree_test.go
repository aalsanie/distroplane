package providerhost

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/planner"
)

const (
	processTreeHelperEnv    = "DISTROPLANE_PROCESS_TREE_HELPER"
	processTreeChildEnv     = "DISTROPLANE_PROCESS_TREE_CHILD"
	processTreeHeartbeatEnv = "DISTROPLANE_PROCESS_TREE_HEARTBEAT"
)

func TestProcessTreeHelper(t *testing.T) {
	if os.Getenv(processTreeHelperEnv) != "1" {
		return
	}
	heartbeat := os.Getenv(processTreeHeartbeatEnv)
	if os.Getenv(processTreeChildEnv) == "1" {
		for {
			_ = os.WriteFile(heartbeat, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
			time.Sleep(10 * time.Millisecond)
		}
	}

	child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHelper$")
	child.Env = append(os.Environ(), processTreeChildEnv+"=1")
	if err := child.Start(); err != nil {
		os.Exit(71)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestClientCancellationKillsProcessTree(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
	default:
		t.Skip("process-tree termination is implemented for supported release platforms")
	}

	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	client, err := New(Options{
		Args: []string{"-test.run=^TestProcessTreeHelper$"},
		Environment: []string{
			processTreeHelperEnv + "=1",
			processTreeHeartbeatEnv + "=" + heartbeat,
		},
		WaitDelay: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	executable := helperExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, callErr := client.Describe(ctx, planner.Endpoint{Name: "fake", Executable: executable})
		result <- callErr
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if info, statErr := os.Stat(heartbeat); statErr == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("provider child process never produced a heartbeat")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case callErr := <-result:
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("err=%v", callErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("provider process did not terminate after cancellation")
	}

	time.Sleep(100 * time.Millisecond)
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("provider child process survived parent cancellation")
	}
}

func TestTerminateProcessTreeNil(t *testing.T) {
	if err := terminateProcessTree(nil); err != nil {
		t.Fatal(err)
	}
}
