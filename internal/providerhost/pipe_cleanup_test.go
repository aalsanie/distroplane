package providerhost

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	pipeHolderPathEnv  = "DISTROPLANE_PIPE_HOLDER_PATH"
	pipeHolderChildEnv = "DISTROPLANE_PIPE_HOLDER_CHILD"
)

func TestPipeHolderHelper(t *testing.T) {
	path := os.Getenv(pipeHolderPathEnv)
	if path == "" {
		return
	}
	if os.Getenv(pipeHolderChildEnv) == "1" {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path + ".stop"); err == nil {
				os.Exit(0)
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(71)
			}
			_, err = file.WriteString("heartbeat\n")
			if closeErr := file.Close(); err != nil || closeErr != nil {
				os.Exit(71)
			}
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(0)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(71)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPipeHolderHelper$")
	child.Env = append(os.Environ(), pipeHolderChildEnv+"=1")
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(71)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	os.Exit(71)
}

func TestClientStopsDescendantHoldingOutputAfterParentExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeat")
	t.Cleanup(func() { _ = os.WriteFile(path+".stop", nil, 0600) })
	client, err := New(Options{
		Args:        []string{"-test.run=^TestPipeHolderHelper$"},
		Environment: []string{pipeHolderPathEnv + "=" + path},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = client.Describe(ctx, helperEndpoint(t))
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("inherited pipes should hit drain limit: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	before, err := os.ReadFile(path)
	if err != nil || len(before) == 0 {
		t.Fatalf("child heartbeat missing: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("descendant remained active after provider exit: %v", err)
	}
}
