package providerhost

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const abortedInputCaptureEnv = "DISTROPLANE_TEST_ABORTED_INPUT_CAPTURE"

func TestAbortedInputChild(t *testing.T) {
	path := os.Getenv(abortedInputCaptureEnv)
	if path == "" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(71)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		os.Exit(71)
	}
	if len(data) != 0 {
		if err := os.WriteFile(path+".effect", []byte("apply"), 0600); err != nil {
			os.Exit(71)
		}
	}
	os.Exit(0)
}

func TestAbortedInputNeverReachesChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate := make(chan struct{})
	reader := &gatedReader{reader: strings.NewReader("apply"), gate: gate}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAbortedInputChild$")
	command.Env = append(os.Environ(), abortedInputCaptureEnv+"="+path)
	command.Stdin = reader
	command.WaitDelay = time.Second
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader.abort()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("aborted request reached child: %q", data)
	}
	if _, err := os.Stat(path + ".effect"); !os.IsNotExist(err) {
		t.Errorf("aborted request triggered child effect: %v", err)
	}
}
